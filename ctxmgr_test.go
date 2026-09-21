package tinyagent

import (
	"context"
	"strings"
	"testing"
)

func TestSimpleCounter_Basic(t *testing.T) {
	c := NewSimpleCounter()
	ctx := context.Background()

	empty, err := c.Count(ctx, CountRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if empty.Total != 0 {
		t.Errorf("empty total = %d, want 0", empty.Total)
	}

	en, err := c.Count(ctx, CountRequest{Messages: []Message{{Role: RoleUser, Content: strings.Repeat("a", 400)}}})
	if err != nil {
		t.Fatal(err)
	}
	zh, err := c.Count(ctx, CountRequest{Messages: []Message{{Role: RoleUser, Content: strings.Repeat("中", 300)}}})
	if err != nil {
		t.Fatal(err)
	}

	if en.Total < 50 || en.Total > 200 {
		t.Errorf("english estimate = %d, expected roughly 100", en.Total)
	}
	if zh.Total < 100 || zh.Total > 400 {
		t.Errorf("chinese estimate = %d, expected roughly 200", zh.Total)
	}
	if zh.Total <= en.Total {
		t.Errorf("CJK should cost more per char: zh=%d en=%d", zh.Total, en.Total)
	}
}

func TestSimpleCounter_IncludesTools(t *testing.T) {
	c := NewSimpleCounter()
	ctx := context.Background()

	without, err := c.Count(ctx, CountRequest{Messages: []Message{{Content: "hi"}}})
	if err != nil {
		t.Fatal(err)
	}
	with, err := c.Count(ctx, CountRequest{
		Messages: []Message{{Content: "hi"}},
		Tools: []ToolInfo{{
			Name:        "get_weather",
			Description: strings.Repeat("描述", 50),
			Parameters:  []byte(`{"type":"object","properties":{"city":{"type":"string"}}}`),
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if with.Total <= without.Total {
		t.Errorf("tools should add tokens: with=%d without=%d", with.Total, without.Total)
	}
	if with.ByPart["tools"] == 0 {
		t.Error("ByPart[tools] should be non-zero")
	}
}

func TestSimpleCounter_ContextCanceled(t *testing.T) {
	c := NewSimpleCounter()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.Count(ctx, CountRequest{}); err != context.Canceled {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}

func TestTruncateStrategy_KeepsRecent(t *testing.T) {
	msgs := make([]Message, 30)
	for i := range msgs {
		msgs[i] = Message{Role: RoleUser, Content: "m"}
	}
	s := &TruncateStrategy{KeepRecent: 10}
	out, action, err := s.Apply(context.Background(), msgs, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 10 {
		t.Errorf("len = %d, want 10", len(out))
	}
	if action == nil || action.Strategy != "truncate" {
		t.Errorf("action = %+v", action)
	}
}

func TestTruncateStrategy_NoOpWhenShort(t *testing.T) {
	msgs := []Message{{Role: RoleUser, Content: "a"}}
	s := &TruncateStrategy{KeepRecent: 10}
	out, action, err := s.Apply(context.Background(), msgs, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || action != nil {
		t.Errorf("expected no-op, got len=%d action=%+v", len(out), action)
	}
}

// TestTruncateStrategy_PreservesToolPairing 是最关键的用例：
// 裁剪点绝不能落在工具结果上，否则 OpenAI 兼容端点直接返回 400。
func TestTruncateStrategy_PreservesToolPairing(t *testing.T) {
	msgs := []Message{
		{Role: RoleUser, Content: "u1"},
		{Role: RoleAssistant, Content: "a1"},
		{Role: RoleUser, Content: "u2"},
		{Role: RoleAssistant, ToolCalls: []ToolCall{
			{ID: "c1", Name: "t1"}, {ID: "c2", Name: "t2"}, {ID: "c3", Name: "t3"},
		}},
		{Role: RoleTool, ToolCallID: "c1", Content: "r1"},
		{Role: RoleTool, ToolCallID: "c2", Content: "r2"},
		{Role: RoleTool, ToolCallID: "c3", Content: "r3"},
		{Role: RoleAssistant, Content: "final"},
	}

	// KeepRecent=4 → 候选起点 index 4（RoleTool）→ 必须前移到 index 3。
	s := &TruncateStrategy{KeepRecent: 4}
	out, action, err := s.Apply(context.Background(), msgs, 100)
	if err != nil {
		t.Fatal(err)
	}
	if action == nil {
		t.Fatal("expected an action")
	}
	if out[0].Role == RoleTool {
		t.Fatalf("cut left a dangling tool result: %+v", out[0])
	}
	if out[0].Role != RoleAssistant || len(out[0].ToolCalls) != 3 {
		t.Errorf("expected cut at the assistant carrying tool_calls, got %+v", out[0])
	}
	if len(out) != 5 {
		t.Errorf("len = %d, want 5", len(out))
	}
}

func TestOffloadStrategy(t *testing.T) {
	off := NewMemoryOffloader()
	s := &OffloadStrategy{Threshold: 100, Offloader: off}

	big := strings.Repeat("x", 500)
	msgs := []Message{
		{Role: RoleTool, ToolCallID: "c1", Content: big},
		{Role: RoleTool, ToolCallID: "c2", Content: "small"},
	}

	out, action, err := s.Apply(context.Background(), msgs, 100)
	if err != nil {
		t.Fatal(err)
	}
	if action == nil || action.Strategy != "offload" {
		t.Fatalf("action = %+v", action)
	}
	if !strings.Contains(out[0].Content, "[offloaded:") {
		t.Errorf("big result not offloaded: %q", out[0].Content)
	}
	if out[1].Content != "small" {
		t.Errorf("small result should be untouched, got %q", out[1].Content)
	}

	ref := strings.TrimPrefix(strings.Split(out[0].Content, "]")[0], "[offloaded:")
	got, err := off.Retrieve(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	if got != big {
		t.Errorf("retrieved content mismatch: got len=%d want len=%d", len(got), len(big))
	}
}

func TestOffloadStrategy_NoOffloader(t *testing.T) {
	s := &OffloadStrategy{Threshold: 1}
	out, action, err := s.Apply(context.Background(), []Message{{Role: RoleTool, Content: "x"}}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if action != nil || out[0].Content != "x" {
		t.Error("expected no-op without offloader")
	}
}

func TestMemoryOffloader_NotFound(t *testing.T) {
	off := NewMemoryOffloader()
	if _, err := off.Retrieve(context.Background(), "nope"); err != ErrNotFound {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestDefaultContextManager_WithinBudget(t *testing.T) {
	m := NewDefaultContextManager(NewSimpleCounter())
	msgs := []Message{{Role: RoleUser, Content: "hi"}}
	out, actions, err := m.Prepare(context.Background(), msgs, nil, "", 10000)
	if err != nil {
		t.Fatal(err)
	}
	if len(actions) != 0 {
		t.Errorf("expected no actions within budget, got %+v", actions)
	}
	if len(out) != 1 {
		t.Error("messages changed unexpectedly")
	}
}

func TestDefaultContextManager_AppliesStrategies(t *testing.T) {
	m := NewDefaultContextManager(NewSimpleCounter())
	msgs := make([]Message, 200)
	for i := range msgs {
		msgs[i] = Message{Role: RoleUser, Content: strings.Repeat("x", 200)}
	}

	out, actions, err := m.Prepare(context.Background(), msgs, nil, "", 500)
	if err != nil {
		t.Fatal(err)
	}
	if len(actions) == 0 {
		t.Fatal("expected context actions")
	}
	if len(out) >= len(msgs) {
		t.Errorf("context not reduced: %d -> %d", len(msgs), len(out))
	}
	for _, a := range actions {
		if a.Before == 0 {
			t.Errorf("action missing Before: %+v", a)
		}
	}
}

func TestDefaultContextManager_NoCounter(t *testing.T) {
	m := &DefaultContextManager{}
	msgs := []Message{{Role: RoleUser, Content: "hi"}}
	out, actions, err := m.Prepare(context.Background(), msgs, nil, "", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || len(actions) != 0 {
		t.Error("expected passthrough without counter")
	}
}
