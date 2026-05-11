// Kiro /v1/responses 输出 shape 适配。
//
// KiroChatCompletions 写出的是 OpenAI chat-completion 协议（chat.completion / chat.completion.chunk）。
// /v1/responses 客户端期待 Responses 协议（object="response"，含 output[]、output_text，
// 流式则是 response.created / response.output_text.delta / response.completed 事件）。
//
// 本文件提供一个 gin.ResponseWriter 包装器，拦截下游写出的 chat 协议字节，
// 实时翻译成 Responses 协议再写到真正的 ResponseWriter。
package handler

import (
	"encoding/json"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/tidwall/gjson"
)

type respToolCall struct {
	itemID    string
	outputIdx int
	callID    string
	name      string
	args      strings.Builder
}

// responsesShapeWriter 拦截 KiroChatCompletions 的写出并翻译成 /v1/responses 协议。
type responsesShapeWriter struct {
	*sseShapeBase

	streamInited bool
	respID       string
	itemID       string
	model        string
	createdAt    int64
	outputIndex  int
	contentIndex int
	textBuilder  strings.Builder
	usageBuf     json.RawMessage
	finishReason string

	// 文本消息 item 是否已开（首个 text delta 时延迟打开）
	msgItemOpened bool
	msgItemClosed bool
	// tool_call 聚合：按 OpenAI delta 的 index 索引
	toolCalls map[int]*respToolCall
	toolOrder []int
}

func newResponsesShapeWriter(w gin.ResponseWriter, stream bool) *responsesShapeWriter {
	rw := &responsesShapeWriter{
		respID:    "resp_" + strings.ReplaceAll(uuid.NewString(), "-", ""),
		itemID:    "msg_" + strings.ReplaceAll(uuid.NewString(), "-", ""),
		toolCalls: map[int]*respToolCall{},
	}
	rw.sseShapeBase = newSSEShapeBase(w, stream, rw.handleStreamFrame)
	return rw
}

// handleStreamFrame 处理已解析出来的 SSE data；data == []byte("[DONE]") 是结束哨兵。
//
// 并发约束：仅由 sseShapeBase.Write 在持锁状态下调用；写 toolCalls/msgItemOpened 等字段
// 均在该锁保护内。请勿从其他 goroutine 直接调用本方法。
func (w *responsesShapeWriter) handleStreamFrame(data []byte) {
	if string(data) == "[DONE]" {
		w.emitStreamCompleted()
		w.writeRaw("data: [DONE]\n\n")
		w.flushUnderlying()
		return
	}

	if !gjson.ValidBytes(data) {
		return
	}

	// 第一帧：发 response.created（item.added 推迟到首个 text/tool delta）
	if !w.streamInited {
		w.streamInited = true
		w.model = gjson.GetBytes(data, "model").String()
		w.createdAt = gjson.GetBytes(data, "created").Int()
		w.emitResponseCreated()
	}

	choice := gjson.GetBytes(data, "choices.0")
	if choice.Exists() {
		if delta := choice.Get("delta.content"); delta.Exists() && delta.String() != "" {
			text := delta.String()
			if !w.msgItemOpened {
				w.msgItemOpened = true
				w.emitOutputItemAdded()
				w.emitContentPartAdded()
			}
			_, _ = w.textBuilder.WriteString(text)
			w.emitTextDelta(text)
		}
		// tool_calls deltas
		if tcs := choice.Get("delta.tool_calls"); tcs.IsArray() {
			for _, tc := range tcs.Array() {
				idx := int(tc.Get("index").Int())
				existing, ok := w.toolCalls[idx]
				if !ok {
					// 关闭尚未结束的文本 item，给 function_call 让位
					if w.msgItemOpened && !w.msgItemClosed {
						w.closeMessageItem()
					}
					// 找下一个 outputIndex
					nextIdx := w.outputIndex
					if w.msgItemClosed || w.msgItemOpened {
						nextIdx = w.outputIndex + 1
						w.outputIndex = nextIdx
					}
					existing = &respToolCall{
						itemID:    "fc_" + strings.ReplaceAll(uuid.NewString(), "-", ""),
						outputIdx: nextIdx,
						callID:    tc.Get("id").String(),
						name:      tc.Get("function.name").String(),
					}
					w.toolCalls[idx] = existing
					w.toolOrder = append(w.toolOrder, idx)
					w.emitFunctionCallItemAdded(existing)
				} else {
					if id := tc.Get("id").String(); id != "" {
						existing.callID = id
					}
					if n := tc.Get("function.name").String(); n != "" {
						existing.name = n
					}
				}
				if argDelta := tc.Get("function.arguments"); argDelta.Exists() {
					s := argDelta.String()
					if s != "" {
						_, _ = existing.args.WriteString(s)
						w.emitFunctionCallArgsDelta(existing, s)
					}
				}
			}
		}
		if fr := choice.Get("finish_reason"); fr.Exists() && fr.String() != "" {
			w.finishReason = fr.String()
		}
	}

	// 抽 usage（最后一个 chunk）
	if usage := gjson.GetBytes(data, "usage"); usage.Exists() {
		w.usageBuf = json.RawMessage(usage.Raw)
	}
}

func (w *responsesShapeWriter) emitSSE(event string, payload any) {
	b, err := json.Marshal(payload)
	if err != nil {
		return
	}
	w.writeRaw("event: " + event + "\ndata: " + string(b) + "\n\n")
	w.flushUnderlying()
}

func (w *responsesShapeWriter) emitResponseCreated() {
	resp := w.buildResponseObject(false, "in_progress")
	w.emitSSE("response.created", map[string]any{
		"type":     "response.created",
		"response": resp,
	})
}

func (w *responsesShapeWriter) emitOutputItemAdded() {
	w.emitSSE("response.output_item.added", map[string]any{
		"type":         "response.output_item.added",
		"output_index": w.outputIndex,
		"item": map[string]any{
			"id":      w.itemID,
			"type":    "message",
			"role":    "assistant",
			"status":  "in_progress",
			"content": []any{},
		},
	})
}

func (w *responsesShapeWriter) emitContentPartAdded() {
	w.emitSSE("response.content_part.added", map[string]any{
		"type":          "response.content_part.added",
		"item_id":       w.itemID,
		"output_index":  w.outputIndex,
		"content_index": w.contentIndex,
		"part": map[string]any{
			"type": "output_text",
			"text": "",
		},
	})
}

func (w *responsesShapeWriter) emitTextDelta(text string) {
	w.emitSSE("response.output_text.delta", map[string]any{
		"type":          "response.output_text.delta",
		"item_id":       w.itemID,
		"output_index":  w.outputIndex,
		"content_index": w.contentIndex,
		"delta":         text,
	})
}

func (w *responsesShapeWriter) closeMessageItem() {
	if !w.msgItemOpened || w.msgItemClosed {
		return
	}
	finalText := w.textBuilder.String()
	w.emitSSE("response.output_text.done", map[string]any{
		"type":          "response.output_text.done",
		"item_id":       w.itemID,
		"output_index":  w.outputIndex,
		"content_index": w.contentIndex,
		"text":          finalText,
	})
	w.emitSSE("response.content_part.done", map[string]any{
		"type":          "response.content_part.done",
		"item_id":       w.itemID,
		"output_index":  w.outputIndex,
		"content_index": w.contentIndex,
		"part": map[string]any{
			"type": "output_text",
			"text": finalText,
		},
	})
	w.emitSSE("response.output_item.done", map[string]any{
		"type":         "response.output_item.done",
		"output_index": w.outputIndex,
		"item": map[string]any{
			"id":     w.itemID,
			"type":   "message",
			"role":   "assistant",
			"status": "completed",
			"content": []any{
				map[string]any{"type": "output_text", "text": finalText},
			},
		},
	})
	w.msgItemClosed = true
}

func (w *responsesShapeWriter) emitFunctionCallItemAdded(tc *respToolCall) {
	w.emitSSE("response.output_item.added", map[string]any{
		"type":         "response.output_item.added",
		"output_index": tc.outputIdx,
		"item": map[string]any{
			"id":        tc.itemID,
			"type":      "function_call",
			"status":    "in_progress",
			"call_id":   tc.callID,
			"name":      tc.name,
			"arguments": "",
		},
	})
}

func (w *responsesShapeWriter) emitFunctionCallArgsDelta(tc *respToolCall, delta string) {
	w.emitSSE("response.function_call_arguments.delta", map[string]any{
		"type":         "response.function_call_arguments.delta",
		"item_id":      tc.itemID,
		"output_index": tc.outputIdx,
		"delta":        delta,
	})
}

func (w *responsesShapeWriter) closeFunctionCallItem(tc *respToolCall) {
	args := tc.args.String()
	w.emitSSE("response.function_call_arguments.done", map[string]any{
		"type":         "response.function_call_arguments.done",
		"item_id":      tc.itemID,
		"output_index": tc.outputIdx,
		"arguments":    args,
	})
	w.emitSSE("response.output_item.done", map[string]any{
		"type":         "response.output_item.done",
		"output_index": tc.outputIdx,
		"item": map[string]any{
			"id":        tc.itemID,
			"type":      "function_call",
			"status":    "completed",
			"call_id":   tc.callID,
			"name":      tc.name,
			"arguments": args,
		},
	})
}

func (w *responsesShapeWriter) emitStreamCompleted() {
	// 仅有 tool_calls 而无文本时，避免发空 message item
	if w.msgItemOpened && !w.msgItemClosed {
		w.closeMessageItem()
	}
	for _, idx := range w.toolOrder {
		if tc, ok := w.toolCalls[idx]; ok {
			w.closeFunctionCallItem(tc)
		}
	}
	resp := w.buildResponseObject(true, "completed")
	w.emitSSE("response.completed", map[string]any{
		"type":     "response.completed",
		"response": resp,
	})
}

func (w *responsesShapeWriter) buildResponseObject(includeOutput bool, status string) map[string]any {
	obj := map[string]any{
		"id":         w.respID,
		"object":     "response",
		"created_at": w.createdAt,
		"status":     status,
		"model":      w.model,
	}
	if includeOutput {
		out := make([]any, 0, 2)
		text := w.textBuilder.String()
		if w.msgItemOpened || (text != "" && len(w.toolOrder) == 0) {
			out = append(out, map[string]any{
				"id":     w.itemID,
				"type":   "message",
				"role":   "assistant",
				"status": "completed",
				"content": []any{
					map[string]any{"type": "output_text", "text": text},
				},
			})
		}
		for _, idx := range w.toolOrder {
			tc := w.toolCalls[idx]
			if tc == nil {
				continue
			}
			out = append(out, map[string]any{
				"id":        tc.itemID,
				"type":      "function_call",
				"status":    "completed",
				"call_id":   tc.callID,
				"name":      tc.name,
				"arguments": tc.args.String(),
			})
		}
		if len(out) == 0 {
			out = append(out, map[string]any{
				"id":     w.itemID,
				"type":   "message",
				"role":   "assistant",
				"status": "completed",
				"content": []any{
					map[string]any{"type": "output_text", "text": ""},
				},
			})
		}
		obj["output"] = out
		obj["output_text"] = text
	}
	if len(w.usageBuf) > 0 {
		var u map[string]any
		if err := json.Unmarshal(w.usageBuf, &u); err == nil {
			// chat usage 字段名映射到 responses 字段名
			out := map[string]any{}
			if v, ok := u["prompt_tokens"]; ok {
				out["input_tokens"] = v
			}
			if v, ok := u["completion_tokens"]; ok {
				out["output_tokens"] = v
			}
			if v, ok := u["total_tokens"]; ok {
				out["total_tokens"] = v
			}
			obj["usage"] = out
		}
	}
	return obj
}

// finalize 在非流式模式下，由调用方在请求结束后调用，
// 把缓冲的 chat JSON 翻译成 Responses JSON 并写到底层 writer。
func (w *responsesShapeWriter) finalize() {
	if w.stream {
		return
	}
	body := w.buf.Bytes()
	if !gjson.ValidBytes(body) {
		// 不是合法 JSON（可能是上游错误响应），原样写出
		w.ResponseWriter.WriteHeader(w.statusCode)
		_, _ = w.ResponseWriter.Write(body)
		return
	}
	// 错误响应（含 error 字段）原样透传
	if gjson.GetBytes(body, "error").Exists() {
		w.ResponseWriter.Header().Set("Content-Type", "application/json")
		w.ResponseWriter.WriteHeader(w.statusCode)
		_, _ = w.ResponseWriter.Write(body)
		return
	}

	model := gjson.GetBytes(body, "model").String()
	created := gjson.GetBytes(body, "created").Int()
	text := gjson.GetBytes(body, "choices.0.message.content").String()

	itemID := w.itemID
	respID := w.respID
	output := make([]any, 0, 2)
	if text != "" {
		output = append(output, map[string]any{
			"id":     itemID,
			"type":   "message",
			"role":   "assistant",
			"status": "completed",
			"content": []any{
				map[string]any{"type": "output_text", "text": text},
			},
		})
	}
	// tool_calls → Responses function_call items
	if calls := gjson.GetBytes(body, "choices.0.message.tool_calls"); calls.IsArray() {
		for _, tc := range calls.Array() {
			id := tc.Get("id").String()
			output = append(output, map[string]any{
				"id":        "fc_" + strings.ReplaceAll(uuid.NewString(), "-", ""),
				"type":      "function_call",
				"status":    "completed",
				"call_id":   id,
				"name":      tc.Get("function.name").String(),
				"arguments": tc.Get("function.arguments").String(),
			})
		}
	}
	if len(output) == 0 {
		output = append(output, map[string]any{
			"id":     itemID,
			"type":   "message",
			"role":   "assistant",
			"status": "completed",
			"content": []any{
				map[string]any{"type": "output_text", "text": ""},
			},
		})
	}

	resp := map[string]any{
		"id":          respID,
		"object":      "response",
		"created_at":  created,
		"status":      "completed",
		"model":       model,
		"output":      output,
		"output_text": text,
	}
	if usage := gjson.GetBytes(body, "usage"); usage.Exists() {
		resp["usage"] = map[string]any{
			"input_tokens":  usage.Get("prompt_tokens").Int(),
			"output_tokens": usage.Get("completion_tokens").Int(),
			"total_tokens":  usage.Get("total_tokens").Int(),
		}
	}
	out, _ := json.Marshal(resp)
	w.ResponseWriter.Header().Set("Content-Type", "application/json")
	w.ResponseWriter.WriteHeader(w.statusCode)
	_, _ = w.ResponseWriter.Write(out)
}
