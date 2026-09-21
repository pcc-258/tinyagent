package openai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"tinyagent"
)

func TestGenerate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Errorf("path = %s, want /chat/completions", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("auth = %q, want Bearer test-key", got)
		}

		var req wireRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if req.Model != "test-model" {
			t.Errorf("model = %q, want test-model", req.Model)
		}
		if len(req.Messages) != 1 || req.Messages[0].Content != "hi" {
			t.Errorf("messages = %+v", req.Messages)
		}

		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{
			"choices":[{"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":5,"completion_tokens":2,"total_tokens":7}
		}`)
	}))
	defer srv.Close()

	c := New(Config{APIKey: "test-key", BaseURL: srv.URL, Model: "test-model"})
	resp, err := c.Generate(context.Background(), tinyagent.Request{
		Messages: []tinyagent.Message{{Role: tinyagent.RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Message.Content != "hello" {
		t.Errorf("content = %q, want hello", resp.Message.Content)
	}
	if resp.Usage.TotalTokens != 7 {
		t.Errorf("usage = %+v, want total 7", resp.Usage)
	}
	if resp.FinishReason != "stop" {
		t.Errorf("finish reason = %q, want stop", resp.FinishReason)
	}
}

func TestGenerate_ToolCalls(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{
			"choices":[{"message":{"role":"assistant","tool_calls":[
				{"id":"call_1","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"北京\"}"}}
			]},"finish_reason":"tool_calls"}]
		}`)
	}))
	defer srv.Close()

	c := New(Config{BaseURL: srv.URL})
	resp, err := c.Generate(context.Background(), tinyagent.Request{})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Message.ToolCalls) != 1 {
		t.Fatalf("tool calls = %d, want 1", len(resp.Message.ToolCalls))
	}
	tc := resp.Message.ToolCalls[0]
	if tc.ID != "call_1" || tc.Name != "get_weather" {
		t.Errorf("tool call = %+v", tc)
	}
	if string(tc.Arguments) != `{"city":"北京"}` {
		t.Errorf("arguments = %s", tc.Arguments)
	}
}

func TestGenerate_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		io.WriteString(w, `{"error":{"message":"invalid api key"}}`)
	}))
	defer srv.Close()

	c := New(Config{BaseURL: srv.URL})
	if _, err := c.Generate(context.Background(), tinyagent.Request{}); err == nil {
		t.Fatal("expected error on HTTP 401")
	}
}

func TestStream_Text(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("response writer is not a flusher")
		}
		for _, s := range []string{
			`{"choices":[{"delta":{"content":"he"}}]}`,
			`{"choices":[{"delta":{"content":"llo"}}]}`,
			`{"choices":[{"delta":{},"finish_reason":"stop"}]}`,
			`[DONE]`,
		} {
			io.WriteString(w, "data: "+s+"\n\n")
			flusher.Flush()
		}
	}))
	defer srv.Close()

	c := New(Config{BaseURL: srv.URL})
	var text, finish string
	for chunk, err := range c.Stream(context.Background(), tinyagent.Request{}) {
		if err != nil {
			t.Fatal(err)
		}
		text += chunk.TextDelta
		if chunk.FinishReason != "" {
			finish = chunk.FinishReason
		}
	}
	if text != "hello" {
		t.Errorf("text = %q, want hello", text)
	}
	if finish != "stop" {
		t.Errorf("finish reason = %q, want stop", finish)
	}
}

// TestStream_ToolCallFragments 验证分片到达的工具调用能被正确聚合。
func TestStream_ToolCallFragments(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("response writer is not a flusher")
		}
		for _, s := range []string{
			`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"get_weather"}}]}}]}`,
			`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"city\""}}]}}]}`,
			`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":":\"北京\"}"}}]}}]}`,
			`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
			`[DONE]`,
		} {
			io.WriteString(w, "data: "+s+"\n\n")
			flusher.Flush()
		}
	}))
	defer srv.Close()

	c := New(Config{BaseURL: srv.URL})
	acc := tinyagent.NewToolCallAccumulator()
	for chunk, err := range c.Stream(context.Background(), tinyagent.Request{}) {
		if err != nil {
			t.Fatal(err)
		}
		acc.Add(chunk.ToolCall)
	}

	calls := acc.Calls()
	if len(calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(calls))
	}
	if calls[0].ID != "call_1" || calls[0].Name != "get_weather" {
		t.Errorf("call = %+v", calls[0])
	}
	if string(calls[0].Arguments) != `{"city":"北京"}` {
		t.Errorf("arguments = %s, want {\"city\":\"北京\"}", calls[0].Arguments)
	}
}
