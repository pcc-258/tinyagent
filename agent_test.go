package tinyagent

import (
	"context"
	"iter"
	"strings"
	"testing"
	"github.com/pcc-258/tinyagent/core"
	"github.com/pcc-258/tinyagent/model"
	"github.com/pcc-258/tinyagent/store"
	"github.com/pcc-258/tinyagent/tool"
)

// noopRunner 是一个自定义 runner.Runner，用于验证「整体替换运行循环」能力。
type noopRunner struct{}

func (noopRunner) Run(ctx context.Context, s *store.Session, input string) iter.Seq2[core.Event, error] {
	return func(yield func(core.Event, error) bool) {
		yield(core.Event{Type: core.EventRunStart}, nil)
		yield(core.Event{Type: core.EventText, Text: "from custom runner"}, nil)
		yield(core.Event{Type: core.EventRunEnd}, nil)
	}
}

type scriptedModel struct {
	responses []model.Response
	err       error
	calls     int
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

func TestNew_RequiresModel(t *testing.T) {
	if _, err := New(Config{}); err == nil {
		t.Fatal("expected error when Model is nil")
	}
}

// TestNew_ZeroConfig 验证「几行代码接入」：只给 Model，其余全部走默认。
func TestNew_ZeroConfig(t *testing.T) {
	model := &scriptedModel{responses: []model.Response{
		{Message: core.Message{Role: core.RoleAssistant, Content: "你好"}},
	}}
	ag, err := New(Config{Model: model})
	if err != nil {
		t.Fatal(err)
	}
	if ag.runner == nil || ag.store == nil || ag.logger == nil {
		t.Fatal("defaults not wired")
	}

	reply, err := ag.Chat(context.Background(), "s1", "hi")
	if err != nil {
		t.Fatal(err)
	}
	if reply != "你好" {
		t.Errorf("reply = %q, want 你好", reply)
	}
}

func TestAgent_SessionPersistsAcrossRuns(t *testing.T) {
	model := &scriptedModel{responses: []model.Response{
		{Message: core.Message{Role: core.RoleAssistant, Content: "a"}},
		{Message: core.Message{Role: core.RoleAssistant, Content: "b"}},
	}}
	ag, _ := New(Config{Model: model})
	ctx := context.Background()

	if _, err := ag.Chat(ctx, "s1", "first"); err != nil {
		t.Fatal(err)
	}
	if _, err := ag.Chat(ctx, "s1", "second"); err != nil {
		t.Fatal(err)
	}

	s, _ := ag.Session(ctx, "s1")
	if s.Len() != 4 {
		t.Errorf("session len = %d, want 4", s.Len())
	}
}

func TestAgent_SessionRestoredFromStore(t *testing.T) {
	st := store.NewMemoryStore()
	ctx := context.Background()
	if err := st.Replace(ctx, "s1", []core.Message{
		{Role: core.RoleUser, Content: "old"},
		{Role: core.RoleAssistant, Content: "old reply"},
	}); err != nil {
		t.Fatal(err)
	}

	model := &scriptedModel{responses: []model.Response{{Message: core.Message{Role: core.RoleAssistant, Content: "new"}}}}
	ag, _ := New(Config{Model: model, Store: st})

	s, err := ag.Session(ctx, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if s.Len() != 2 {
		t.Errorf("restored len = %d, want 2", s.Len())
	}
}

func TestAgent_RegisterTool(t *testing.T) {
	model := &scriptedModel{responses: []model.Response{
		{Message: core.Message{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{{ID: "c1", Name: "echo"}}}},
		{Message: core.Message{Role: core.RoleAssistant, Content: "done"}},
	}}
	ag, _ := New(Config{Model: model})
	tl := mustNewTool(t, "echo", "echo", func(_ context.Context, _ weatherParams) (core.ToolResult, error) {
		return core.ToolResult{Content: "ok"}, nil
	})
	if err := ag.RegisterTool(tl); err != nil {
		t.Fatal(err)
	}

	events, err := collect(ag.Run(context.Background(), "s1", "go"))
	if err != nil {
		t.Fatal(err)
	}
	if countType(events, core.EventToolResult) != 1 {
		t.Errorf("expected 1 tool result, events = %v", events)
	}
}

func TestAgent_RegisterTool_CustomRunner(t *testing.T) {
	ag, _ := New(Config{Model: &scriptedModel{}, Runner: noopRunner{}})
	tl := mustNewTool(t, "echo", "echo", func(_ context.Context, _ weatherParams) (core.ToolResult, error) {
		return core.ToolResult{}, nil
	})
	if err := ag.RegisterTool(tl); err == nil {
		t.Fatal("expected error when Runner is custom")
	}
}

func TestAgent_CustomRunner(t *testing.T) {
	ag, _ := New(Config{Model: &scriptedModel{}, Runner: noopRunner{}})
	reply, err := ag.Chat(context.Background(), "s1", "hi")
	if err != nil {
		t.Fatal(err)
	}
	if reply != "from custom runner" {
		t.Errorf("reply = %q, want custom runner output", reply)
	}
}

func TestAgent_PersistAfterRun(t *testing.T) {
	st := store.NewMemoryStore()
	model := &scriptedModel{responses: []model.Response{{Message: core.Message{Role: core.RoleAssistant, Content: "x"}}}}
	ag, _ := New(Config{Model: model, Store: st})
	ctx := context.Background()

	if _, err := ag.Chat(ctx, "s1", "hi"); err != nil {
		t.Fatal(err)
	}
	snap, err := st.Load(ctx, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Messages) != 2 {
		t.Errorf("persisted len = %d, want 2", len(snap.Messages))
	}
	if !strings.Contains(snap.Messages[1].Content, "x") {
		t.Errorf("persisted messages = %+v", snap.Messages)
	}
}
