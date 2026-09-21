package tinyagent

import (
	"context"
	"iter"
	"strings"
	"testing"
)

// noopRunner 是一个自定义 Runner，用于验证「整体替换运行循环」能力。
type noopRunner struct{}

func (noopRunner) Run(ctx context.Context, s *Session, input string) iter.Seq2[Event, error] {
	return func(yield func(Event, error) bool) {
		yield(Event{Type: EventRunStart}, nil)
		yield(Event{Type: EventText, Text: "from custom runner"}, nil)
		yield(Event{Type: EventRunEnd}, nil)
	}
}

func TestNew_RequiresModel(t *testing.T) {
	if _, err := New(Config{}); err == nil {
		t.Fatal("expected error when Model is nil")
	}
}

// TestNew_ZeroConfig 验证「几行代码接入」：只给 Model，其余全部走默认。
func TestNew_ZeroConfig(t *testing.T) {
	model := &scriptedModel{responses: []Response{
		{Message: Message{Role: RoleAssistant, Content: "你好"}},
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
	model := &scriptedModel{responses: []Response{
		{Message: Message{Role: RoleAssistant, Content: "a"}},
		{Message: Message{Role: RoleAssistant, Content: "b"}},
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
	store := NewMemoryStore()
	ctx := context.Background()
	if err := store.Replace(ctx, "s1", []Message{
		{Role: RoleUser, Content: "old"},
		{Role: RoleAssistant, Content: "old reply"},
	}); err != nil {
		t.Fatal(err)
	}

	model := &scriptedModel{responses: []Response{{Message: Message{Role: RoleAssistant, Content: "new"}}}}
	ag, _ := New(Config{Model: model, Store: store})

	s, err := ag.Session(ctx, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if s.Len() != 2 {
		t.Errorf("restored len = %d, want 2", s.Len())
	}
}

func TestAgent_RegisterTool(t *testing.T) {
	model := &scriptedModel{responses: []Response{
		{Message: Message{Role: RoleAssistant, ToolCalls: []ToolCall{{ID: "c1", Name: "echo"}}}},
		{Message: Message{Role: RoleAssistant, Content: "done"}},
	}}
	ag, _ := New(Config{Model: model})
	tool := mustNewTool(t, "echo", "echo", func(_ context.Context, _ weatherParams) (ToolResult, error) {
		return ToolResult{Content: "ok"}, nil
	})
	if err := ag.RegisterTool(tool); err != nil {
		t.Fatal(err)
	}

	events, err := collect(ag.Run(context.Background(), "s1", "go"))
	if err != nil {
		t.Fatal(err)
	}
	if countType(events, EventToolResult) != 1 {
		t.Errorf("expected 1 tool result, events = %v", events)
	}
}

func TestAgent_RegisterTool_CustomRunner(t *testing.T) {
	ag, _ := New(Config{Model: &scriptedModel{}, Runner: noopRunner{}})
	tool := mustNewTool(t, "echo", "echo", func(_ context.Context, _ weatherParams) (ToolResult, error) {
		return ToolResult{}, nil
	})
	if err := ag.RegisterTool(tool); err == nil {
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
	store := NewMemoryStore()
	model := &scriptedModel{responses: []Response{{Message: Message{Role: RoleAssistant, Content: "x"}}}}
	ag, _ := New(Config{Model: model, Store: store})
	ctx := context.Background()

	if _, err := ag.Chat(ctx, "s1", "hi"); err != nil {
		t.Fatal(err)
	}
	snap, err := store.Load(ctx, "s1")
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
