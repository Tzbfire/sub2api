package handler

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/tidwall/gjson"
)

// ============ convertResponsesInputToChatMessages ============

func TestConvertResponsesInput_StringForm(t *testing.T) {
	body := []byte(`{"input":"hello"}`)
	msgs, err := convertResponsesInputToChatMessages(body)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(msgs) != 1 || msgs[0]["role"] != "user" || msgs[0]["content"] != "hello" {
		t.Fatalf("got %+v", msgs)
	}
}

func TestConvertResponsesInput_ContentPartArray(t *testing.T) {
	body := []byte(`{"input":[{"type":"input_text","text":"hi"},{"type":"input_text","text":" there"}]}`)
	msgs, err := convertResponsesInputToChatMessages(body)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(msgs) != 1 || msgs[0]["role"] != "user" {
		t.Fatalf("expected single user msg, got %+v", msgs)
	}
	parts, ok := msgs[0]["content"].([]map[string]any)
	if !ok || len(parts) != 2 {
		t.Fatalf("expected 2 parts, got %T %+v", msgs[0]["content"], msgs[0]["content"])
	}
	if parts[0]["text"] != "hi" || parts[1]["text"] != " there" {
		t.Fatalf("unexpected parts: %+v", parts)
	}
}

func TestConvertResponsesInput_MessageArray(t *testing.T) {
	body := []byte(`{"input":[
        {"role":"system","content":"sys"},
        {"role":"user","content":"q"}
    ]}`)
	msgs, err := convertResponsesInputToChatMessages(body)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("got %d msgs", len(msgs))
	}
	if msgs[0]["role"] != "system" || msgs[1]["role"] != "user" {
		t.Fatalf("roles: %+v", msgs)
	}
}

func TestConvertResponsesInput_FunctionCallAndResult(t *testing.T) {
	body := []byte(`{"input":[
        {"role":"user","content":"What time?"},
        {"type":"function_call","call_id":"c1","name":"get_time","arguments":"{\"city\":\"NYC\"}"},
        {"type":"function_call_output","call_id":"c1","output":"10:00"}
    ]}`)
	msgs, err := convertResponsesInputToChatMessages(body)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(msgs) != 3 {
		t.Fatalf("expected 3 msgs, got %d: %+v", len(msgs), msgs)
	}
	// user
	if msgs[0]["role"] != "user" {
		t.Fatalf("msg0 role: %v", msgs[0])
	}
	// assistant tool_call
	if msgs[1]["role"] != "assistant" {
		t.Fatalf("msg1 role: %v", msgs[1])
	}
	calls, ok := msgs[1]["tool_calls"].([]any)
	if !ok || len(calls) != 1 {
		t.Fatalf("expected 1 tool_call, got %T %+v", msgs[1]["tool_calls"], msgs[1]["tool_calls"])
	}
	tc, ok := calls[0].(map[string]any)
	if !ok {
		t.Fatalf("calls[0] not map: %T", calls[0])
	}
	if tc["id"] != "c1" {
		t.Fatalf("call_id: %v", tc)
	}
	fn, ok := tc["function"].(map[string]any)
	if !ok {
		t.Fatalf("function not map: %T", tc["function"])
	}
	if fn["name"] != "get_time" || fn["arguments"] != `{"city":"NYC"}` {
		t.Fatalf("function: %+v", fn)
	}
	// tool result
	if msgs[2]["role"] != "tool" || msgs[2]["tool_call_id"] != "c1" || msgs[2]["content"] != "10:00" {
		t.Fatalf("tool msg: %+v", msgs[2])
	}
}

func TestConvertResponsesInput_FirstItemIsFunctionCall(t *testing.T) {
	// edge case: array starts with function_call (no role/content)
	body := []byte(`{"input":[{"type":"function_call","call_id":"c","name":"f","arguments":"{}"}]}`)
	msgs, err := convertResponsesInputToChatMessages(body)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(msgs) != 1 || msgs[0]["role"] != "assistant" {
		t.Fatalf("got %+v", msgs)
	}
}

func TestConvertResponsesInput_MissingInput(t *testing.T) {
	_, err := convertResponsesInputToChatMessages([]byte(`{}`))
	if err == nil {
		t.Fatal("expected error")
	}
}

// ============ convertAnthropicMessagesToChat ============

func TestConvertAnthropicMessages_Plain(t *testing.T) {
	body := []byte(`{"model":"claude-sonnet-4.5","max_tokens":100,"messages":[{"role":"user","content":"hi"}]}`)
	out, err := convertAnthropicMessagesToChat(body)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if gjson.GetBytes(out, "model").String() != "claude-sonnet-4.5" {
		t.Fatal("model")
	}
	if gjson.GetBytes(out, "max_tokens").Int() != 100 {
		t.Fatal("max_tokens")
	}
	if gjson.GetBytes(out, "messages.0.role").String() != "user" {
		t.Fatal("role")
	}
}

func TestConvertAnthropicMessages_SystemString(t *testing.T) {
	body := []byte(`{"model":"x","system":"you are helpful","messages":[{"role":"user","content":"q"}]}`)
	out, err := convertAnthropicMessagesToChat(body)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if gjson.GetBytes(out, "messages.0.role").String() != "system" ||
		gjson.GetBytes(out, "messages.0.content").String() != "you are helpful" {
		t.Fatalf("system: %s", string(out))
	}
}

func TestConvertAnthropicMessages_SystemArray(t *testing.T) {
	body := []byte(`{"model":"x","system":[{"type":"text","text":"a"},{"type":"text","text":"b"}],"messages":[{"role":"user","content":"q"}]}`)
	out, err := convertAnthropicMessagesToChat(body)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if gjson.GetBytes(out, "messages.0.content").String() != "ab" {
		t.Fatalf("system: %s", string(out))
	}
}

func TestConvertAnthropicMessages_AssistantToolUse(t *testing.T) {
	body := []byte(`{"model":"x","messages":[
        {"role":"user","content":"weather?"},
        {"role":"assistant","content":[
            {"type":"text","text":"checking"},
            {"type":"tool_use","id":"u1","name":"get_weather","input":{"city":"SF"}}
        ]},
        {"role":"user","content":[
            {"type":"tool_result","tool_use_id":"u1","content":"sunny"}
        ]}
    ]}`)
	out, err := convertAnthropicMessagesToChat(body)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if gjson.GetBytes(out, "messages.1.role").String() != "assistant" {
		t.Fatal("assistant role")
	}
	if gjson.GetBytes(out, "messages.1.tool_calls.0.id").String() != "u1" {
		t.Fatalf("tool_call id missing: %s", string(out))
	}
	if gjson.GetBytes(out, "messages.1.tool_calls.0.function.name").String() != "get_weather" {
		t.Fatal("tool name")
	}
	args := gjson.GetBytes(out, "messages.1.tool_calls.0.function.arguments").String()
	if !strings.Contains(args, `"city"`) || !strings.Contains(args, `"SF"`) {
		t.Fatalf("args: %s", args)
	}
	// tool result becomes role:tool
	if gjson.GetBytes(out, "messages.2.role").String() != "tool" {
		t.Fatalf("expected role:tool, got: %s", string(out))
	}
	if gjson.GetBytes(out, "messages.2.tool_call_id").String() != "u1" {
		t.Fatal("tool_call_id")
	}
	if gjson.GetBytes(out, "messages.2.content").String() != "sunny" {
		t.Fatal("tool result content")
	}
}

func TestConvertAnthropicMessages_Tools(t *testing.T) {
	body := []byte(`{"model":"x","messages":[{"role":"user","content":"q"}],"tools":[
        {"name":"get_x","description":"get x","input_schema":{"type":"object","properties":{"k":{"type":"string"}}}}
    ]}`)
	out, err := convertAnthropicMessagesToChat(body)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if gjson.GetBytes(out, "tools.0.type").String() != "function" {
		t.Fatalf("type: %s", string(out))
	}
	if gjson.GetBytes(out, "tools.0.function.name").String() != "get_x" {
		t.Fatal("name")
	}
	if gjson.GetBytes(out, "tools.0.function.parameters.type").String() != "object" {
		t.Fatal("parameters")
	}
}

func TestConvertAnthropicMessages_ToolChoice(t *testing.T) {
	cases := []struct {
		choice string
		expect string
	}{
		{`{"type":"auto"}`, `"auto"`},
		{`{"type":"any"}`, `"required"`},
	}
	for _, c := range cases {
		body := []byte(`{"model":"x","messages":[{"role":"user","content":"q"}],"tool_choice":` + c.choice + `}`)
		out, err := convertAnthropicMessagesToChat(body)
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		got := gjson.GetBytes(out, "tool_choice").Raw
		if got != c.expect {
			t.Fatalf("choice %s: got %s want %s", c.choice, got, c.expect)
		}
	}

	// type:tool with name
	body := []byte(`{"model":"x","messages":[{"role":"user","content":"q"}],"tool_choice":{"type":"tool","name":"get_x"}}`)
	out, _ := convertAnthropicMessagesToChat(body)
	if gjson.GetBytes(out, "tool_choice.type").String() != "function" ||
		gjson.GetBytes(out, "tool_choice.function.name").String() != "get_x" {
		t.Fatalf("tool choice tool: %s", string(out))
	}
}

func TestConvertAnthropicMessages_ImageBase64(t *testing.T) {
	body := []byte(`{"model":"x","messages":[{"role":"user","content":[
        {"type":"image","source":{"type":"base64","media_type":"image/png","data":"AAA"}},
        {"type":"text","text":"describe"}
    ]}]}`)
	out, err := convertAnthropicMessagesToChat(body)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	url := gjson.GetBytes(out, "messages.0.content.0.image_url.url").String()
	if url != "data:image/png;base64,AAA" {
		t.Fatalf("url: %s", url)
	}
}

func TestConvertAnthropicMessages_MissingModel(t *testing.T) {
	_, err := convertAnthropicMessagesToChat([]byte(`{"messages":[]}`))
	if err == nil {
		t.Fatal("expected model error")
	}
}

// quick sanity that the produced body is valid JSON
func TestConvertAnthropicMessages_OutputIsValidJSON(t *testing.T) {
	body := []byte(`{"model":"x","temperature":0.7,"top_p":0.9,"stream":true,"messages":[{"role":"user","content":"hi"}]}`)
	out, err := convertAnthropicMessagesToChat(body)
	if err != nil {
		t.Fatal(err)
	}
	var v any
	if err := json.Unmarshal(out, &v); err != nil {
		t.Fatalf("invalid json: %s", string(out))
	}
}
