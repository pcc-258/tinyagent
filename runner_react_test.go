package tinyagent

import (
	"context"
	"iter"
	"strings"
	"testing"
)

type scriptedModel struct {
	responses []Response
	err       error
	calls     int
}

func (m *scriptedModel) Generate(ctx context.Context, req Request) (Response, error) {
	if m.err != nil {
		return Response{}, m.err
	}
	if m.calls >= len(m.responses) {
		return Response{Message: Message{Role: RoleAssistant, Content: "fallback"}}, nil
	}
	resp := m.responses[m.calls]
	m.calls++
	return resp, nil
}

func collect(seq iter.Seq2[Event, error]) ([]Event, error) {
	var events []Event
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

func countType(events []Event, typ EventType) int {
	n := 0
	for _, e := range events {
		if e.Type == typ {
			n++
		}
	}
	return n
}

func mustRunner(t *testing.T, opts RunnerOptions) *ReActRunner {
	t.Helper()
	r, err := NewReActRunner(opts)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestNewReActRunner_RequiresModel(t *testing.T) {
	if _, err := NewReActRunner(RunnerOptions{}); err == nil {
		t.Fatal("expected error when Model is nil")
	}
}

func TestReActRunner_PlainResponse(t *testing.T) {
	model := &scriptedModel{responses: []Response{
		{Message: Message{Role: RoleAssistant, Content: "hello"}, Usage: Usage{TotalTokens: 10}},
	}}
	r := mustRunner(t, RunnerOptions{Model: model})
	s := NewSession("s1")

	events, err := collect(r.Run(context.Background(), s, "hi"))
	if err != nil {
		t.Fatal(err)
	}
	if countType(events, EventRunStart) != 1 || countType(events, EventText) != 1 || countType(events, EventRunEnd) != 1 {
		t.Errorf("unexpected events: %v", events)
	}
	if s.Len() != 2 {
		t.Errorf("session len = %d, want 2 (user + assistant)", s.Len())
	}
}

func TestReActRunner_ToolLoop(t *testing.T) {
	tool := mustNewTool(t, "echo", "echo", func(_ context.Context, p weatherParams) (ToolResult, error) {
		return ToolResult{Content: "echo:" + p.City}, nil
	})
	model := &scriptedModel{responses: []Response{
		{Message: Message{Role: RoleAssistant, ToolCalls: []ToolCall{
			{ID: "c1", Name: "echo", Arguments: []byte(`{"city":"北京","days":1}`)},
		}}},
		{Message: Message{Role: RoleAssistant, Content: "done"}},
	}}
	r := mustRunner(t, RunnerOptions{Model: model, Tools: []Tool{tool}})
	s := NewSession("s1")

	events, err := collect(r.Run(context.Background(), s, "weather?"))
	if err != nil {
		t.Fatal(err)
	}
	if countType(events, EventToolCall) != 1 || countType(events, EventToolResult) != 1 {
		t.Errorf("expected 1 tool call/result, events = %v", events)
	}

	var got ToolResult
	for _, e := range events {
		if e.Type == EventToolResult {
			got = *e.ToolResult
		}
	}
	if got.Content != "echo:北京" {
		t.Errorf("tool result = %+v", got)
	}
	if s.Len() != 4 {
		t.Errorf("session len = %d, want 4 (user, assistant+call, tool, assistant)", s.Len())
	}
}

func TestReActRunner_UnknownTool(t *testing.T) {
	model := &scriptedModel{responses: []Response{
		{Message: Message{Role: RoleAssistant, ToolCalls: []ToolCall{{ID: "c1", Name: "nope"}}}},
		{Message: Message{Role: RoleAssistant, Content: "ok"}},
	}}
	r := mustRunner(t, RunnerOptions{Model: model})
	s := NewSession("s1")

	events, err := collect(r.Run(context.Background(), s, "x"))
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, e := range events {
		if e.Type == EventToolResult && strings.Contains(e.ToolResult.Error, "unknown tool") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected unknown-tool soft error, events = %v", events)
	}
}

// TestReActRunner_ToolPanicRecovered 是「崩溃不带崩主程序」的端到端验证。
func TestReActRunner_ToolPanicRecovered(t *testing.T) {
	tool := mustNewTool(t, "boom", "panics", func(_ context.Context, _ weatherParams) (ToolResult, error) {
		panic("tool exploded")
	})
	model := &scriptedModel{responses: []Response{
		{Message: Message{Role: RoleAssistant, ToolCalls: []ToolCall{{ID: "c1", Name: "boom"}}}},
		{Message: Message{Role: RoleAssistant, Content: "recovered"}},
	}}
	r := mustRunner(t, RunnerOptions{Model: model, Tools: []Tool{tool}})
	s := NewSession("s1")

	events, err := collect(r.Run(context.Background(), s, "go"))
	if err != nil {
		t.Fatalf("run must survive a tool panic: %v", err)
	}
	if countType(events, EventPanic) != 1 {
		t.Errorf("panic events = %d, want 1", countType(events, EventPanic))
	}

	var sawSoftError bool
	for _, e := range events {
		if e.Type == EventToolResult && strings.Contains(e.ToolResult.Error, "tool exploded") {
			sawSoftError = true
		}
	}
	if !sawSoftError {
		t.Error("panic must surface as a tool soft error, not vanish")
	}
}

func TestReActRunner_ToolPanicFailRun(t *testing.T) {
	tool := mustNewTool(t, "boom", "panics", func(_ context.Context, _ weatherParams) (ToolResult, error) {
		panic("fatal")
	})
	model := &scriptedModel{responses: []Response{
		{Message: Message{Role: RoleAssistant, ToolCalls: []ToolCall{{ID: "c1", Name: "boom"}}}},
	}}
	r := mustRunner(t, RunnerOptions{Model: model, Tools: []Tool{tool}, PanicPolicy: PanicFailRun})
	s := NewSession("s1")

	if _, err := collect(r.Run(context.Background(), s, "go")); err == nil {
		t.Fatal("expected run to fail under PanicFailRun")
	}
}

func TestReActRunner_MaxIterations(t *testing.T) {
	looping := Response{Message: Message{Role: RoleAssistant, ToolCalls: []ToolCall{{ID: "c", Name: "noop"}}}}
	tool := mustNewTool(t, "noop", "noop", func(_ context.Context, _ weatherParams) (ToolResult, error) {
		return ToolResult{Content: "again"}, nil
	})
	model := &scriptedModel{responses: []Response{looping, looping, looping, looping, looping, looping, looping, looping}}
	r := mustRunner(t, RunnerOptions{Model: model, Tools: []Tool{tool}, MaxIterations: 3})
	s := NewSession("s1")

	_, err := collect(r.Run(context.Background(), s, "go"))
	if err != ErrMaxIterations {
		t.Fatalf("err = %v, want ErrMaxIterations", err)
	}
}

func TestReActRunner_SessionBusy(t *testing.T) {
	model := &scriptedModel{responses: []Response{{Message: Message{Role: RoleAssistant, Content: "x"}}}}
	r := mustRunner(t, RunnerOptions{Model: model})
	s := NewSession("s1")
	if !s.tryAcquire() {
		t.Fatal("setup acquire failed")
	}
	defer s.release()

	_, err := collect(r.Run(context.Background(), s, "hi"))
	if err != ErrSessionBusy {
		t.Fatalf("err = %v, want ErrSessionBusy", err)
	}
}

func TestReActRunner_HookRejectsTool(t *testing.T) {
	tool := mustNewTool(t, "secret", "secret", func(_ context.Context, _ weatherParams) (ToolResult, error) {
		return ToolResult{Content: "should not run"}, nil
	})
	model := &scriptedModel{responses: []Response{
		{Message: Message{Role: RoleAssistant, ToolCalls: []ToolCall{{ID: "c1", Name: "secret"}}}},
		{Message: Message{Role: RoleAssistant, Content: "ok"}},
	}}
	hook := HookFuncs{OnBeforeToolCall: func(_ context.Context, call *ToolCall) error {
		return ErrHookRejected
	}}
	r := mustRunner(t, RunnerOptions{Model: model, Tools: []Tool{tool}, Hooks: []Hook{hook}})
	s := NewSession("s1")

	events, err := collect(r.Run(context.Background(), s, "go"))
	if err != nil {
		t.Fatal(err)
	}
	var rejected bool
	for _, e := range events {
		if e.Type == EventToolResult && strings.Contains(e.ToolResult.Error, "rejected") {
			rejected = true
			if strings.Contains(e.ToolResult.Content, "should not run") {
				t.Error("rejected tool must not execute")
			}
		}
	}
	if !rejected {
		t.Errorf("expected rejection, events = %v", events)
	}
}

func TestReActRunner_ContextAction(t *testing.T) {
	model := &scriptedModel{responses: []Response{{Message: Message{Role: RoleAssistant, Content: "ok"}}}}
	r := mustRunner(t, RunnerOptions{
		Model:         model,
		Counter:       NewSimpleCounter(),
		Context:       NewDefaultContextManager(NewSimpleCounter()),
		ContextBudget: 50,
	})
	s := NewSession("s1")
	for i := 0; i < 40; i++ {
		s.Append(Message{Role: RoleUser, Content: strings.Repeat("x", 100)})
	}

	events, err := collect(r.Run(context.Background(), s, "go"))
	if err != nil {
		t.Fatal(err)
	}
	if countType(events, EventContextAction) == 0 {
		t.Errorf("expected context actions under tight budget, events = %v", events)
	}
}

func TestReActRunner_AuditRecords(t *testing.T) {
	audit := NewMemoryAudit()
	tool := mustNewTool(t, "echo", "echo", func(_ context.Context, _ weatherParams) (ToolResult, error) {
		return ToolResult{Content: "ok"}, nil
	})
	model := &scriptedModel{responses: []Response{
		{Message: Message{Role: RoleAssistant, ToolCalls: []ToolCall{{ID: "c1", Name: "echo"}}}},
		{Message: Message{Role: RoleAssistant, Content: "done"}},
	}}
	r := mustRunner(t, RunnerOptions{Model: model, Tools: []Tool{tool}, Audit: audit})
	s := NewSession("s1")

	if _, err := collect(r.Run(context.Background(), s, "go")); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	for _, kind := range []AuditRecordKind{AuditRunStart, AuditRunEnd, AuditModelCall, AuditToolCall} {
		recs, err := audit.Query(ctx, AuditQuery{Kind: kind})
		if err != nil {
			t.Fatal(err)
		}
		if len(recs) == 0 {
			t.Errorf("no audit records of kind %q", kind)
		}
	}
}

func TestReActRunner_ModelError(t *testing.T) {
	model := &scriptedModel{err: context.DeadlineExceeded}
	r := mustRunner(t, RunnerOptions{Model: model})
	s := NewSession("s1")

	_, err := collect(r.Run(context.Background(), s, "hi"))
	if err == nil {
		t.Fatal("expected model error to surface")
	}
}
