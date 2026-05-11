// Kiro 平台的 Anthropic /v1/messages 适配。
//
// 策略：
//  1. 把 Anthropic Messages 请求转换为 OpenAI chat-completions 请求（system→role:system，
//     content 数组转换）
//  2. 调用 KiroChatCompletions（已对接 CodeWhisperer + 限流隔离 + RecordUsage）
//  3. 用 anthropicShapeWriter 拦截输出，把 chat-completion / chat.completion.chunk
//     翻译为 Anthropic Messages 协议（content[]/usage + 流式 message_start/
//     content_block_delta/message_stop 等事件链）
package handler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// KiroMessages 处理 /v1/messages 当 group platform = kiro。
func (h *OpenAIGatewayHandler) KiroMessages(c *gin.Context) {
	body, err := pkghttputil.ReadRequestBodyWithPrealloc(c.Request)
	if err != nil {
		if maxErr, ok := extractMaxBytesError(err); ok {
			h.errorResponse(c, http.StatusRequestEntityTooLarge, "invalid_request_error", buildBodyTooLargeMessage(maxErr.Limit))
			return
		}
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "Failed to read request body")
		return
	}
	if len(body) == 0 || !gjson.ValidBytes(body) {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "Failed to parse request body")
		return
	}

	chatBody, convErr := convertAnthropicMessagesToChat(body)
	if convErr != nil {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", convErr.Error())
		return
	}
	stream := gjson.GetBytes(chatBody, "stream").Bool()

	c.Request.Body = io.NopCloser(bytes.NewReader(chatBody))
	c.Request.ContentLength = int64(len(chatBody))

	originalWriter := c.Writer
	shapeWriter := newAnthropicShapeWriter(originalWriter, stream)
	c.Writer = shapeWriter
	defer func() {
		shapeWriter.finalize()
		c.Writer = originalWriter
	}()

	h.KiroChatCompletions(c)
}

// convertAnthropicMessagesToChat 把 Anthropic Messages 请求转换为 OpenAI Chat Completions 请求。
//   - system: string | [{type,text}] → role:"system" 单条消息
//   - messages[].content: string | [{type:"text",text}|{type:"image",source:{...}}] → chat content
//   - max_tokens / temperature / top_p / stream 透传
func convertAnthropicMessagesToChat(body []byte) ([]byte, error) {
	model := gjson.GetBytes(body, "model").String()
	if model == "" {
		return nil, fmt.Errorf("model is required")
	}

	chatMsgs := make([]map[string]any, 0)

	// system → 第一条消息
	if sys := gjson.GetBytes(body, "system"); sys.Exists() {
		var sysText string
		if sys.Type == gjson.String {
			sysText = sys.String()
		} else if sys.IsArray() {
			var sb strings.Builder
			for _, p := range sys.Array() {
				if p.Get("type").String() == "text" {
					_, _ = sb.WriteString(p.Get("text").String())
				}
			}
			sysText = sb.String()
		}
		if sysText != "" {
			chatMsgs = append(chatMsgs, map[string]any{"role": "system", "content": sysText})
		}
	}

	// messages
	msgsRes := gjson.GetBytes(body, "messages")
	if !msgsRes.IsArray() {
		return nil, fmt.Errorf("messages is required and must be array")
	}
	for _, m := range msgsRes.Array() {
		role := m.Get("role").String()
		if role == "" {
			role = "user"
		}
		content := m.Get("content")
		// Anthropic 在 user 角色里用 tool_result block 表示工具结果；
		// 在 assistant 里用 tool_use block 表示工具调用。需要拆出来转 OpenAI 形态。
		if content.IsArray() && role == "assistant" {
			textParts := make([]string, 0)
			toolCalls := make([]map[string]any, 0)
			for _, p := range content.Array() {
				switch p.Get("type").String() {
				case "text":
					textParts = append(textParts, p.Get("text").String())
				case "tool_use":
					inputRaw := p.Get("input").Raw
					if inputRaw == "" {
						inputRaw = "{}"
					}
					toolCalls = append(toolCalls, map[string]any{
						"id":   p.Get("id").String(),
						"type": "function",
						"function": map[string]any{
							"name":      p.Get("name").String(),
							"arguments": inputRaw,
						},
					})
				}
			}
			out := map[string]any{"role": "assistant", "content": strings.Join(textParts, "")}
			if len(toolCalls) > 0 {
				out["tool_calls"] = toolCalls
				if len(textParts) == 0 {
					out["content"] = nil
				}
			}
			chatMsgs = append(chatMsgs, out)
			continue
		}
		if content.IsArray() && role == "user" {
			// 把 tool_result block 拆成单独的 role:tool 消息（OpenAI 形态）
			textParts := make([]map[string]any, 0)
			toolResults := make([]map[string]any, 0)
			for _, p := range content.Array() {
				switch p.Get("type").String() {
				case "text":
					textParts = append(textParts, map[string]any{"type": "text", "text": p.Get("text").String()})
				case "image":
					src := p.Get("source")
					if src.Get("type").String() == "base64" {
						url := "data:" + src.Get("media_type").String() + ";base64," + src.Get("data").String()
						textParts = append(textParts, map[string]any{
							"type":      "image_url",
							"image_url": map[string]any{"url": url},
						})
					} else if u := src.Get("url").String(); u != "" {
						textParts = append(textParts, map[string]any{
							"type":      "image_url",
							"image_url": map[string]any{"url": u},
						})
					}
				case "tool_result":
					tc := p.Get("content")
					var contentText string
					if tc.Type == gjson.String {
						contentText = tc.String()
					} else if tc.IsArray() {
						var sb strings.Builder
						for _, sub := range tc.Array() {
							if sub.Get("type").String() == "text" {
								_, _ = sb.WriteString(sub.Get("text").String())
							}
						}
						contentText = sb.String()
					}
					toolResults = append(toolResults, map[string]any{
						"role":         "tool",
						"tool_call_id": p.Get("tool_use_id").String(),
						"content":      contentText,
					})
				}
			}
			// 先 append tool 结果消息，再 append 剩余 user 文本/图片
			chatMsgs = append(chatMsgs, toolResults...)
			if len(textParts) > 0 {
				chatMsgs = append(chatMsgs, map[string]any{"role": "user", "content": textParts})
			}
			continue
		}

		out := map[string]any{"role": role}
		switch {
		case content.Type == gjson.String:
			out["content"] = content.String()
		case content.IsArray():
			parts := make([]map[string]any, 0)
			for _, p := range content.Array() {
				switch p.Get("type").String() {
				case "text":
					parts = append(parts, map[string]any{"type": "text", "text": p.Get("text").String()})
				case "image":
					src := p.Get("source")
					if src.Get("type").String() == "base64" {
						url := "data:" + src.Get("media_type").String() + ";base64," + src.Get("data").String()
						parts = append(parts, map[string]any{
							"type":      "image_url",
							"image_url": map[string]any{"url": url},
						})
					} else if u := src.Get("url").String(); u != "" {
						parts = append(parts, map[string]any{
							"type":      "image_url",
							"image_url": map[string]any{"url": u},
						})
					}
				}
			}
			out["content"] = parts
		default:
			out["content"] = ""
		}
		chatMsgs = append(chatMsgs, out)
	}

	chatBody, _ := json.Marshal(map[string]any{
		"model":    model,
		"messages": chatMsgs,
	})
	if v := gjson.GetBytes(body, "max_tokens"); v.Exists() {
		chatBody, _ = sjson.SetBytes(chatBody, "max_tokens", v.Int())
	}
	if v := gjson.GetBytes(body, "temperature"); v.Exists() {
		chatBody, _ = sjson.SetBytes(chatBody, "temperature", v.Float())
	}
	if v := gjson.GetBytes(body, "top_p"); v.Exists() {
		chatBody, _ = sjson.SetBytes(chatBody, "top_p", v.Float())
	}
	if v := gjson.GetBytes(body, "stream"); v.Exists() {
		chatBody, _ = sjson.SetBytes(chatBody, "stream", v.Bool())
	}
	if v := gjson.GetBytes(body, "stop_sequences"); v.Exists() {
		chatBody, _ = sjson.SetRawBytes(chatBody, "stop", []byte(v.Raw))
	}
	// tools: Anthropic [{name,description,input_schema}] → OpenAI [{type:"function",function:{name,description,parameters}}]
	if v := gjson.GetBytes(body, "tools"); v.IsArray() {
		converted := make([]map[string]any, 0, len(v.Array()))
		for _, t := range v.Array() {
			fn := map[string]any{
				"name":        t.Get("name").String(),
				"description": t.Get("description").String(),
			}
			if schema := t.Get("input_schema"); schema.Exists() {
				var schemaObj any
				if err := json.Unmarshal([]byte(schema.Raw), &schemaObj); err == nil {
					fn["parameters"] = schemaObj
				}
			}
			converted = append(converted, map[string]any{"type": "function", "function": fn})
		}
		raw, _ := json.Marshal(converted)
		chatBody, _ = sjson.SetRawBytes(chatBody, "tools", raw)
	}
	if v := gjson.GetBytes(body, "tool_choice"); v.Exists() {
		// Anthropic: {type:"auto"|"any"|"tool", name?:string} → OpenAI: "auto"|"none"|"required"|{type:"function",function:{name}}
		switch v.Get("type").String() {
		case "auto":
			chatBody, _ = sjson.SetBytes(chatBody, "tool_choice", "auto")
		case "any":
			chatBody, _ = sjson.SetBytes(chatBody, "tool_choice", "required")
		case "tool":
			tc := map[string]any{"type": "function", "function": map[string]any{"name": v.Get("name").String()}}
			raw, _ := json.Marshal(tc)
			chatBody, _ = sjson.SetRawBytes(chatBody, "tool_choice", raw)
		}
	}
	return chatBody, nil
}

// ============== anthropicShapeWriter ==============

// anthropicShapeWriter 拦截 KiroChatCompletions 输出，翻译为 Anthropic Messages 协议。
type anthropicShapeWriter struct {
	*sseShapeBase

	streamInited   bool
	msgID          string
	model          string
	textBuilder    strings.Builder
	finishReason   string
	usageBuf       json.RawMessage
	contentStarted bool

	// tool_use 流式状态：
	//  - openIdx 当前已开启的最高 content_block 索引（-1 表示无）
	//  - toolBlocks 按 OpenAI tool_calls[].index → 我们自己的 anthropic block index 的映射
	openIdx    int
	toolBlocks map[int]int // openai_index → anthropic_block_index
}

func newAnthropicShapeWriter(w gin.ResponseWriter, stream bool) *anthropicShapeWriter {
	aw := &anthropicShapeWriter{
		msgID:      "msg_" + strings.ReplaceAll(uuid.NewString(), "-", ""),
		openIdx:    -1,
		toolBlocks: map[int]int{},
	}
	aw.sseShapeBase = newSSEShapeBase(w, stream, aw.handleFrame)
	return aw
}

// handleFrame 处理已解析出来的 SSE data 帧；data == []byte("[DONE]") 是结束哨兵。
//
// 并发约束：仅由 sseShapeBase.Write 在持锁状态下调用；写 toolBlocks/contentStarted 等
// 状态字段均在该锁保护内。请勿从其他 goroutine 直接调用本方法。
func (w *anthropicShapeWriter) handleFrame(data []byte) {
	if string(data) == "[DONE]" {
		w.emitStreamCompleted()
		return
	}
	if !gjson.ValidBytes(data) {
		return
	}

	if !w.streamInited {
		w.streamInited = true
		w.model = gjson.GetBytes(data, "model").String()
		// 提取首帧的 usage（如果有 prompt_tokens）作为 input_tokens
		var inputTokens int64
		if u := gjson.GetBytes(data, "usage.prompt_tokens"); u.Exists() {
			inputTokens = u.Int()
		}
		w.emitMessageStart(inputTokens)
	}

	choice := gjson.GetBytes(data, "choices.0")
	if choice.Exists() {
		if delta := choice.Get("delta.content"); delta.Exists() && delta.String() != "" {
			text := delta.String()
			if !w.contentStarted {
				w.contentStarted = true
				w.openIdx = 0
				w.emitContentBlockStart()
			}
			_, _ = w.textBuilder.WriteString(text)
			w.emitContentBlockDelta(text)
		}
		// tool_calls 流式：每个 tool_call delta → tool_use content_block
		if tcalls := choice.Get("delta.tool_calls"); tcalls.IsArray() {
			for _, tc := range tcalls.Array() {
				openaiIdx := int(tc.Get("index").Int())
				anthIdx, ok := w.toolBlocks[openaiIdx]
				if !ok {
					// 关闭上一个 content block（text 或前一个 tool_use）
					if w.contentStarted || w.openIdx >= 0 {
						w.emitContentBlockStop(w.openIdx)
					}
					w.openIdx++
					anthIdx = w.openIdx
					w.toolBlocks[openaiIdx] = anthIdx
					name := tc.Get("function.name").String()
					id := tc.Get("id").String()
					w.emitToolUseStart(anthIdx, id, name)
				}
				if args := tc.Get("function.arguments"); args.Exists() && args.String() != "" {
					w.emitToolUseDelta(anthIdx, args.String())
				}
			}
		}
		if fr := choice.Get("finish_reason"); fr.Exists() && fr.String() != "" {
			w.finishReason = fr.String()
		}
	}
	if usage := gjson.GetBytes(data, "usage"); usage.Exists() {
		w.usageBuf = json.RawMessage(usage.Raw)
	}
}

func (w *anthropicShapeWriter) emitSSE(event string, payload any) {
	b, err := json.Marshal(payload)
	if err != nil {
		return
	}
	w.writeRaw("event: " + event + "\ndata: " + string(b) + "\n\n")
	w.flushUnderlying()
}

func (w *anthropicShapeWriter) emitMessageStart(inputTokens int64) {
	w.emitSSE("message_start", map[string]any{
		"type": "message_start",
		"message": map[string]any{
			"id":            w.msgID,
			"type":          "message",
			"role":          "assistant",
			"model":         w.model,
			"content":       []any{},
			"stop_reason":   nil,
			"stop_sequence": nil,
			"usage": map[string]any{
				"input_tokens":  inputTokens,
				"output_tokens": 0,
			},
		},
	})
}

func (w *anthropicShapeWriter) emitContentBlockStart() {
	w.emitSSE("content_block_start", map[string]any{
		"type":          "content_block_start",
		"index":         0,
		"content_block": map[string]any{"type": "text", "text": ""},
	})
}

func (w *anthropicShapeWriter) emitContentBlockDelta(text string) {
	w.emitSSE("content_block_delta", map[string]any{
		"type":  "content_block_delta",
		"index": 0,
		"delta": map[string]any{"type": "text_delta", "text": text},
	})
}

func (w *anthropicShapeWriter) emitToolUseStart(idx int, id, name string) {
	w.emitSSE("content_block_start", map[string]any{
		"type":  "content_block_start",
		"index": idx,
		"content_block": map[string]any{
			"type":  "tool_use",
			"id":    id,
			"name":  name,
			"input": map[string]any{},
		},
	})
}

func (w *anthropicShapeWriter) emitToolUseDelta(idx int, partialJSON string) {
	w.emitSSE("content_block_delta", map[string]any{
		"type":  "content_block_delta",
		"index": idx,
		"delta": map[string]any{"type": "input_json_delta", "partial_json": partialJSON},
	})
}

func (w *anthropicShapeWriter) emitContentBlockStop(idx int) {
	w.emitSSE("content_block_stop", map[string]any{
		"type":  "content_block_stop",
		"index": idx,
	})
}

func (w *anthropicShapeWriter) emitStreamCompleted() {
	if w.openIdx >= 0 {
		w.emitContentBlockStop(w.openIdx)
	}
	stopReason := mapFinishReasonToAnthropic(w.finishReason)
	deltaPayload := map[string]any{
		"type": "message_delta",
		"delta": map[string]any{
			"stop_reason":   stopReason,
			"stop_sequence": nil,
		},
	}
	if len(w.usageBuf) > 0 {
		var u map[string]any
		if err := json.Unmarshal(w.usageBuf, &u); err == nil {
			out := map[string]any{}
			if v, ok := u["completion_tokens"]; ok {
				out["output_tokens"] = v
			}
			deltaPayload["usage"] = out
		}
	}
	w.emitSSE("message_delta", deltaPayload)
	w.emitSSE("message_stop", map[string]any{"type": "message_stop"})
}

func (w *anthropicShapeWriter) finalize() {
	if w.stream {
		return
	}
	body := w.buf.Bytes()
	if !gjson.ValidBytes(body) {
		w.ResponseWriter.WriteHeader(w.statusCode)
		_, _ = w.ResponseWriter.Write(body)
		return
	}
	if gjson.GetBytes(body, "error").Exists() {
		// 错误：转 Anthropic 错误格式
		errObj := gjson.GetBytes(body, "error")
		anthErr := map[string]any{
			"type": "error",
			"error": map[string]any{
				"type":    coalesceStr(errObj.Get("type").String(), "api_error"),
				"message": errObj.Get("message").String(),
			},
		}
		out, _ := json.Marshal(anthErr)
		w.ResponseWriter.Header().Set("Content-Type", "application/json")
		w.ResponseWriter.WriteHeader(w.statusCode)
		_, _ = w.ResponseWriter.Write(out)
		return
	}

	model := gjson.GetBytes(body, "model").String()
	text := gjson.GetBytes(body, "choices.0.message.content").String()
	finishReason := gjson.GetBytes(body, "choices.0.finish_reason").String()
	stopReason := mapFinishReasonToAnthropic(finishReason)

	contentBlocks := make([]map[string]any, 0, 2)
	if text != "" {
		contentBlocks = append(contentBlocks, map[string]any{"type": "text", "text": text})
	}
	if calls := gjson.GetBytes(body, "choices.0.message.tool_calls"); calls.IsArray() {
		for _, tc := range calls.Array() {
			argsRaw := tc.Get("function.arguments").String()
			var input any = map[string]any{}
			if argsRaw != "" {
				_ = json.Unmarshal([]byte(argsRaw), &input)
			}
			contentBlocks = append(contentBlocks, map[string]any{
				"type":  "tool_use",
				"id":    tc.Get("id").String(),
				"name":  tc.Get("function.name").String(),
				"input": input,
			})
		}
	}
	if len(contentBlocks) == 0 {
		contentBlocks = append(contentBlocks, map[string]any{"type": "text", "text": ""})
	}

	resp := map[string]any{
		"id":            w.msgID,
		"type":          "message",
		"role":          "assistant",
		"model":         model,
		"content":       contentBlocks,
		"stop_reason":   stopReason,
		"stop_sequence": nil,
	}
	if usage := gjson.GetBytes(body, "usage"); usage.Exists() {
		resp["usage"] = map[string]any{
			"input_tokens":  usage.Get("prompt_tokens").Int(),
			"output_tokens": usage.Get("completion_tokens").Int(),
		}
	}
	out, _ := json.Marshal(resp)
	w.ResponseWriter.Header().Set("Content-Type", "application/json")
	w.ResponseWriter.WriteHeader(w.statusCode)
	_, _ = w.ResponseWriter.Write(out)
}

func mapFinishReasonToAnthropic(fr string) any {
	switch fr {
	case "stop":
		return "end_turn"
	case "length":
		return "max_tokens"
	case "tool_calls":
		return "tool_use"
	case "content_filter":
		return "stop_sequence"
	case "":
		return nil
	default:
		return "end_turn"
	}
}

func coalesceStr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
