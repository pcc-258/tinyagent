package e2e_test

import (
	"context"
	"fmt"
	"io"
	"iter"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/pcc-258/tinyagent"
	"github.com/pcc-258/tinyagent/audit"
	"github.com/pcc-258/tinyagent/core"
	"github.com/pcc-258/tinyagent/hook"
	"github.com/pcc-258/tinyagent/model"
	"github.com/pcc-258/tinyagent/model/openai"
	"github.com/pcc-258/tinyagent/store"
	"github.com/pcc-258/tinyagent/tool"
)

type weatherParams struct {
	City string `json:"city" desc:"City name"`
	Days int    `json:"days" desc:"Forecast days"`
}

type scriptedModel struct {
	responses []model.Response
	next      int
}

func (m *scriptedModel) Generate(_ context.Context, _ model.Request) (model.Response, error) {
	if m.next >= len(m.responses) {
		return model.Response{Message: core.Message{Role: core.RoleAssistant, Content: "fallback"}}, nil
	}
	resp := m.responses[m.next]
	m.next++
	return resp, nil
}

func newWeatherTool(t *testing.T) tool.Tool {
	t.Helper()
	tl, err := tool.NewFuncTool("get_weather", "Get the current weather for a city",
		func(_ context.Context, args weatherParams) (core.ToolResult, error) {
			return core.ToolResult{Content: fmt.Sprintf("%s: sunny, %d days", args.City, args.Days)}, nil
		})
	if err != nil {
		t.Fatal(err)
	}
	return tl
}

func collectRun(seq iter.Seq2[core.Event, error]) ([]core.Event, error) {
	var events []core.Event
	var lastErr error
	for ev, err := range seq {
		if err != nil {
			lastErr = err
			continue
		}
		events = append(events, ev)
	}
	return events, lastErr
}

func countEventType(events []core.Event, typ core.EventType) int {
	n := 0
	for _, ev := range events {
		if ev.Type == typ {
			n++
		}
	}
	return n
}

func writeSSE(t *testing.T, w http.ResponseWriter, chunks ...string) {
	t.Helper()
	flusher, ok := w.(http.Flusher)
	if !ok {
		t.Fatal("response writer is not a flusher")
	}
	for _, chunk := range chunks {
		if _, err := io.WriteString(w, "data: "+chunk+"\n\n"); err != nil {
			t.Fatal(err)
		}
		flusher.Flush()
	}
}

// TestAgentEndToEndToolLoop drives a full agent run through the real
// OpenAI-compatible adapter, streaming tool calls, tool execution, and the
// final answer, then checks store, audit, and hook side effects.
func TestAgentEndToEndToolLoop(t *testing.T) {
	var mu sync.Mutex
	call := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Errorf("path = %s, want /chat/completions", r.URL.Path)
		}
		mu.Lock()
		call++
		current := call
		mu.Unlock()

		if current == 1 {
			writeSSE(t, w,
				`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"get_weather"}}]}}]}`,
				`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"city\":\"Beijing\""}}]}}]}`,
				`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":",\"days\":1}"}}]}}]}`,
				`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
				`[DONE]`,
			)
			return
		}
		writeSSE(t, w,
			`{"choices":[{"delta":{"content":"Final"}}]}`,
			`{"choices":[{"delta":{"content":" answer"}}]}`,
			`{"choices":[{"delta":{},"finish_reason":"stop"}]}`,
			`[DONE]`,
		)
	}))
	defer srv.Close()

	st := store.NewMemoryStore()
	ad := audit.NewMemoryAudit()
	var sawToolCall string
	ag, err := tinyagent.New(tinyagent.Config{
		Model: openai.New(openai.Config{
			APIKey:  "test-key",
			BaseURL: srv.URL,
			Model:   "test-model",
		}),
		Tools: []tool.Tool{newWeatherTool(t)},
		Store: st,
		Audit: ad,
		Hooks: []hook.Hook{hook.HookFuncs{
			OnBeforeToolCall: func(_ context.Context, call *core.ToolCall) error {
				sawToolCall = call.Name
				return nil
			},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	var reply strings.Builder
	events, err := collectRun(ag.Run(ctx, "e2e", "What is the weather in Beijing?"))
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range events {
		if ev.Type == core.EventText {
			reply.WriteString(ev.Text)
		}
	}

	if reply.String() != "Final answer" {
		t.Errorf("reply = %q, want Final answer", reply.String())
	}
	if countEventType(events, core.EventToolCall) != 1 || countEventType(events, core.EventToolResult) != 1 {
		t.Errorf("expected one tool call/result, events = %v", events)
	}
	if sawToolCall != "get_weather" {
		t.Errorf("hook saw tool %q, want get_weather", sawToolCall)
	}

	snap, err := st.Load(ctx, "e2e")
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Messages) != 4 {
		t.Errorf("persisted messages = %d, want 4", len(snap.Messages))
	}
	recs, err := ad.Query(ctx, audit.AuditQuery{Kind: audit.AuditToolCall})
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 {
		t.Errorf("audit tool records = %d, want 1", len(recs))
	}
}

// TestAgentEndToEndMultiTurnPersists verifies two chat turns share one session
// through the public Agent facade and that a second Agent restores the same
// history from the Store.
func TestAgentEndToEndMultiTurnPersists(t *testing.T) {
	var mu sync.Mutex
	call := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		call++
		current := call
		mu.Unlock()
		reply := "one"
		if current == 2 {
			reply = "two"
		}
		writeSSE(t, w,
			`{"choices":[{"delta":{"content":"`+reply+`"}}]}`,
			`{"choices":[{"delta":{},"finish_reason":"stop"}]}`,
			`[DONE]`,
		)
	}))
	defer srv.Close()

	st := store.NewMemoryStore()
	mdl := openai.New(openai.Config{APIKey: "test-key", BaseURL: srv.URL, Model: "test-model"})
	ag, err := tinyagent.New(tinyagent.Config{Model: mdl, Store: st})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	first, err := ag.Chat(ctx, "multi", "first")
	if err != nil {
		t.Fatal(err)
	}
	second, err := ag.Chat(ctx, "multi", "second")
	if err != nil {
		t.Fatal(err)
	}
	if first != "one" || second != "two" {
		t.Errorf("replies = %q, %q; want one, two", first, second)
	}

	s, err := ag.Session(ctx, "multi")
	if err != nil {
		t.Fatal(err)
	}
	if s.Len() != 4 {
		t.Errorf("session len = %d, want 4", s.Len())
	}

	ag2, err := tinyagent.New(tinyagent.Config{
		Model: openai.New(openai.Config{APIKey: "test-key", BaseURL: srv.URL, Model: "test-model"}),
		Store: st,
	})
	if err != nil {
		t.Fatal(err)
	}
	restored, err := ag2.Session(ctx, "multi")
	if err != nil {
		t.Fatal(err)
	}
	if restored.Len() != 4 {
		t.Errorf("restored len = %d, want 4", restored.Len())
	}
}

// TestAgentEndToEndPanicRecovery verifies the public Agent facade survives a
// panicking tool, reports it through the event stream, and keeps the run going.
func TestAgentEndToEndPanicRecovery(t *testing.T) {
	type emptyArgs struct{}
	boom, err := tool.NewFuncTool("boom", "always panics",
		func(_ context.Context, _ emptyArgs) (core.ToolResult, error) {
			panic("tool exploded")
		})
	if err != nil {
		t.Fatal(err)
	}

	mdl := &scriptedModel{responses: []model.Response{
		{Message: core.Message{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{
			{ID: "c1", Name: "boom", Arguments: []byte(`{}`)},
		}}},
		{Message: core.Message{Role: core.RoleAssistant, Content: "recovered"}},
	}}
	ag, err := tinyagent.New(tinyagent.Config{Model: mdl, Tools: []tool.Tool{boom}})
	if err != nil {
		t.Fatal(err)
	}

	events, err := collectRun(ag.Run(context.Background(), "panic", "go"))
	if err != nil {
		t.Fatalf("run must survive a tool panic: %v", err)
	}
	if countEventType(events, core.EventPanic) != 1 {
		t.Errorf("panic events = %d, want 1", countEventType(events, core.EventPanic))
	}
	var sawSoftError bool
	for _, ev := range events {
		if ev.Type == core.EventToolResult && strings.Contains(ev.ToolResult.Error, "tool exploded") {
			sawSoftError = true
		}
	}
	if !sawSoftError {
		t.Error("panic must surface as a tool soft error")
	}
}
