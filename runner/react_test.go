package runner

import (
	"context"
	"errors"
	"github.com/pcc-258/tinyagent/audit"
	"github.com/pcc-258/tinyagent/core"
	"github.com/pcc-258/tinyagent/ctxmgr"
	"github.com/pcc-258/tinyagent/hook"
	"github.com/pcc-258/tinyagent/model"
	"github.com/pcc-258/tinyagent/store"
	"github.com/pcc-258/tinyagent/tool"
	"iter"
	"strings"
	"testing"
)

type scriptedModel struct {
	responses []model.Response
	err       error
	calls     int
}

type lengthStreamModel struct{}

func (lengthStreamModel) Generate(context.Context, model.Request) (model.Response, error) {
	return model.Response{}, errors.New("unused")
}

func (lengthStreamModel) Stream(context.Context, model.Request) iter.Seq2[model.Chunk, error] {
	return func(yield func(model.Chunk, error) bool) {
		yield(model.Chunk{FinishReason: "length"}, nil)
	}
}

func (m *scriptedModel) Generate(ctx context.Context, req model.Request) (model.Response, error) {
	if m.err != nil {
		return model.Response{}, m.err
	}
	if m.calls >= len(m.responses) {
		return model.Response{Message: core.Message{Role: core.RoleAssistant, Content: "fallback"}}, nil
	}
	resp := m.responses[m.calls]
	m.calls++
	return resp, nil
}

func collect(seq iter.Seq2[core.Event, error]) ([]core.Event, error) {
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

func countType(events []core.Event, typ core.EventType) int {
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

type weatherParams struct {
	City string `json:"city" desc:"城市名"`
	Unit string `json:"unit,omitempty" desc:"温度单位"`
	Days int    `json:"days" desc:"预报天数"`
}

func mustNewTool[T any](
	t *testing.T,
	name, desc string,
	fn func(context.Context, T) (core.ToolResult, error),
) *tool.FuncTool[T] {
	t.Helper()
	tl, err := tool.NewFuncTool(name, desc, fn)
	if err != nil {
		t.Fatal(err)
	}
	return tl
}

func TestNewReActRunner_RequiresModel(t *testing.T) {
	if _, err := NewReActRunner(RunnerOptions{}); err == nil {
		t.Fatal("expected error when Model is nil")
	}
}

func TestReActRunner_PlainResponse(t *testing.T) {
	model := &scriptedModel{responses: []model.Response{
		{Message: core.Message{Role: core.RoleAssistant, Content: "hello"}, Usage: core.Usage{TotalTokens: 10}},
	}}
	r := mustRunner(t, RunnerOptions{Model: model})
	s := store.NewSession("s1")

	events, err := collect(r.Run(context.Background(), s, "hi"))
	if err != nil {
		t.Fatal(err)
	}
	if countType(events, core.EventRunStart) != 1 || countType(events, core.EventText) != 1 || countType(events, core.EventRunEnd) != 1 {
		t.Errorf("unexpected events: %v", events)
	}
	if s.Len() != 2 {
		t.Errorf("session len = %d, want 2 (user + assistant)", s.Len())
	}
}

func TestReActRunner_EmptyLengthStreamFails(t *testing.T) {
	r := mustRunner(t, RunnerOptions{Model: lengthStreamModel{}})
	s := store.NewSession("s1")

	_, err := collect(r.Run(context.Background(), s, "hi"))
	if err == nil || !strings.Contains(err.Error(), "output truncated") {
		t.Fatalf("err = %v, want output truncated error", err)
	}
}

func TestReActRunner_ToolLoop(t *testing.T) {
	tl := mustNewTool(t, "echo", "echo", func(_ context.Context, p weatherParams) (core.ToolResult, error) {
		return core.ToolResult{Content: "echo:" + p.City}, nil
	})
	model := &scriptedModel{responses: []model.Response{
		{Message: core.Message{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{
			{ID: "c1", Name: "echo", Arguments: []byte(`{"city":"北京","days":1}`)},
		}}},
		{Message: core.Message{Role: core.RoleAssistant, Content: "done"}},
	}}
	r := mustRunner(t, RunnerOptions{Model: model, Tools: []tool.Tool{tl}})
	s := store.NewSession("s1")

	events, err := collect(r.Run(context.Background(), s, "weather?"))
	if err != nil {
		t.Fatal(err)
	}
	if countType(events, core.EventToolCall) != 1 || countType(events, core.EventToolResult) != 1 {
		t.Errorf("expected 1 tool call/result, events = %v", events)
	}

	var got core.ToolResult
	for _, e := range events {
		if e.Type == core.EventToolResult {
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
	model := &scriptedModel{responses: []model.Response{
		{Message: core.Message{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{{ID: "c1", Name: "nope"}}}},
		{Message: core.Message{Role: core.RoleAssistant, Content: "ok"}},
	}}
	r := mustRunner(t, RunnerOptions{Model: model})
	s := store.NewSession("s1")

	events, err := collect(r.Run(context.Background(), s, "x"))
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, e := range events {
		if e.Type == core.EventToolResult && strings.Contains(e.ToolResult.Error, "unknown tool") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected unknown-tool soft error, events = %v", events)
	}
}

// TestReActRunner_ToolPanicRecovered 是「崩溃不带崩主程序」的端到端验证。
func TestReActRunner_ToolPanicRecovered(t *testing.T) {
	tl := mustNewTool(t, "boom", "panics", func(_ context.Context, _ weatherParams) (core.ToolResult, error) {
		panic("tool exploded")
	})
	model := &scriptedModel{responses: []model.Response{
		{Message: core.Message{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{{ID: "c1", Name: "boom"}}}},
		{Message: core.Message{Role: core.RoleAssistant, Content: "recovered"}},
	}}
	r := mustRunner(t, RunnerOptions{Model: model, Tools: []tool.Tool{tl}})
	s := store.NewSession("s1")

	events, err := collect(r.Run(context.Background(), s, "go"))
	if err != nil {
		t.Fatalf("run must survive a tool panic: %v", err)
	}
	if countType(events, core.EventPanic) != 1 {
		t.Errorf("panic events = %d, want 1", countType(events, core.EventPanic))
	}

	var sawSoftError bool
	for _, e := range events {
		if e.Type == core.EventToolResult && strings.Contains(e.ToolResult.Error, "tool exploded") {
			sawSoftError = true
		}
	}
	if !sawSoftError {
		t.Error("panic must surface as a tool soft error, not vanish")
	}
}

func TestReActRunner_ToolPanicFailRun(t *testing.T) {
	tl := mustNewTool(t, "boom", "panics", func(_ context.Context, _ weatherParams) (core.ToolResult, error) {
		panic("fatal")
	})
	model := &scriptedModel{responses: []model.Response{
		{Message: core.Message{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{{ID: "c1", Name: "boom"}}}},
	}}
	r := mustRunner(t, RunnerOptions{Model: model, Tools: []tool.Tool{tl}, PanicPolicy: core.PanicFailRun})
	s := store.NewSession("s1")

	if _, err := collect(r.Run(context.Background(), s, "go")); err == nil {
		t.Fatal("expected run to fail under core.PanicFailRun")
	}
}

func TestReActRunner_MaxIterations(t *testing.T) {
	looping := model.Response{Message: core.Message{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{{ID: "c", Name: "noop"}}}}
	tl := mustNewTool(t, "noop", "noop", func(_ context.Context, _ weatherParams) (core.ToolResult, error) {
		return core.ToolResult{Content: "again"}, nil
	})
	model := &scriptedModel{responses: []model.Response{looping, looping, looping, looping, looping, looping, looping, looping}}
	r := mustRunner(t, RunnerOptions{Model: model, Tools: []tool.Tool{tl}, MaxIterations: 3})
	s := store.NewSession("s1")

	_, err := collect(r.Run(context.Background(), s, "go"))
	if err != core.ErrMaxIterations {
		t.Fatalf("err = %v, want core.ErrMaxIterations", err)
	}
}

func TestReActRunner_SessionBusy(t *testing.T) {
	model := &scriptedModel{responses: []model.Response{{Message: core.Message{Role: core.RoleAssistant, Content: "x"}}}}
	r := mustRunner(t, RunnerOptions{Model: model})
	s := store.NewSession("s1")
	if !s.TryAcquire() {
		t.Fatal("setup acquire failed")
	}
	defer s.Release()

	_, err := collect(r.Run(context.Background(), s, "hi"))
	if err != core.ErrSessionBusy {
		t.Fatalf("err = %v, want core.ErrSessionBusy", err)
	}
}

func TestReActRunner_HookRejectsTool(t *testing.T) {
	tl := mustNewTool(t, "secret", "secret", func(_ context.Context, _ weatherParams) (core.ToolResult, error) {
		return core.ToolResult{Content: "should not run"}, nil
	})
	model := &scriptedModel{responses: []model.Response{
		{Message: core.Message{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{{ID: "c1", Name: "secret"}}}},
		{Message: core.Message{Role: core.RoleAssistant, Content: "ok"}},
	}}
	hk := hook.HookFuncs{OnBeforeToolCall: func(_ context.Context, call *core.ToolCall) error {
		return core.ErrHookRejected
	}}
	r := mustRunner(t, RunnerOptions{Model: model, Tools: []tool.Tool{tl}, Hooks: []hook.Hook{hk}})
	s := store.NewSession("s1")

	events, err := collect(r.Run(context.Background(), s, "go"))
	if err != nil {
		t.Fatal(err)
	}
	var rejected bool
	for _, e := range events {
		if e.Type == core.EventToolResult && strings.Contains(e.ToolResult.Error, "rejected") {
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
	model := &scriptedModel{responses: []model.Response{{Message: core.Message{Role: core.RoleAssistant, Content: "ok"}}}}
	r := mustRunner(t, RunnerOptions{
		Model:         model,
		Counter:       ctxmgr.NewSimpleCounter(),
		Context:       ctxmgr.NewDefaultContextManager(ctxmgr.NewSimpleCounter()),
		ContextBudget: 50,
	})
	s := store.NewSession("s1")
	for i := 0; i < 40; i++ {
		s.Append(core.Message{Role: core.RoleUser, Content: strings.Repeat("x", 100)})
	}

	events, err := collect(r.Run(context.Background(), s, "go"))
	if err != nil {
		t.Fatal(err)
	}
	if countType(events, core.EventContextAction) == 0 {
		t.Errorf("expected context actions under tight budget, events = %v", events)
	}
}

func TestReActRunner_AuditRecords(t *testing.T) {
	ad := audit.NewMemoryAudit()
	tl := mustNewTool(t, "echo", "echo", func(_ context.Context, _ weatherParams) (core.ToolResult, error) {
		return core.ToolResult{Content: "ok"}, nil
	})
	model := &scriptedModel{responses: []model.Response{
		{Message: core.Message{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{{ID: "c1", Name: "echo"}}}},
		{Message: core.Message{Role: core.RoleAssistant, Content: "done"}},
	}}
	r := mustRunner(t, RunnerOptions{Model: model, Tools: []tool.Tool{tl}, Audit: ad})
	s := store.NewSession("s1")

	if _, err := collect(r.Run(context.Background(), s, "go")); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	for _, kind := range []audit.AuditRecordKind{audit.AuditRunStart, audit.AuditRunEnd, audit.AuditModelCall, audit.AuditToolCall} {
		recs, err := ad.Query(ctx, audit.AuditQuery{Kind: kind})
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
	s := store.NewSession("s1")

	_, err := collect(r.Run(context.Background(), s, "hi"))
	if err == nil {
		t.Fatal("expected model error to surface")
	}
}
