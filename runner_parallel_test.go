package tinyagent

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

type delayArgs struct {
	Tag string `json:"tag"`
	MS  int    `json:"ms"`
}

func delayTool(t *testing.T) Tool {
	t.Helper()
	tool, err := NewFuncTool("delay", "延迟返回给定标签",
		func(ctx context.Context, a delayArgs) (ToolResult, error) {
			time.Sleep(time.Duration(a.MS) * time.Millisecond)
			return ToolResult{Content: a.Tag}, nil
		})
	if err != nil {
		t.Fatal(err)
	}
	return tool
}

func delayCall(id, tag string, ms int) ToolCall {
	args, _ := json.Marshal(delayArgs{Tag: tag, MS: ms})
	return ToolCall{ID: id, Name: "delay", Arguments: args}
}

// TestReActRunner_ParallelToolsPreserveOrder 用「最慢的排最前」来区分
// 保序与完成序：若按完成先后返回，结果会颠倒。
func TestReActRunner_ParallelToolsPreserveOrder(t *testing.T) {
	model := &scriptedModel{responses: []Response{
		{Message: Message{Role: RoleAssistant, ToolCalls: []ToolCall{
			delayCall("c1", "first", 60),
			delayCall("c2", "second", 30),
			delayCall("c3", "third", 0),
		}}},
		{Message: Message{Role: RoleAssistant, Content: "done"}},
	}}
	runner := mustRunner(t, RunnerOptions{Model: model, Tools: []Tool{delayTool(t)}})

	start := time.Now()
	events, err := collect(runner.Run(context.Background(), NewSession("s"), "go"))
	elapsed := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}

	var order []string
	for _, ev := range events {
		if ev.Type == EventToolResult {
			order = append(order, ev.ToolResult.Content)
		}
	}
	want := []string{"first", "second", "third"}
	if len(order) != len(want) {
		t.Fatalf("got %d results, want %d: %v", len(order), len(want), order)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("order = %v, want %v (slowest first must still be yielded first)", order, want)
		}
	}

	if elapsed > 80*time.Millisecond {
		t.Errorf("elapsed = %v; tools did not overlap (sequential would be ~90ms)", elapsed)
	}
}

func TestReActRunner_SequentialTools(t *testing.T) {
	model := &scriptedModel{responses: []Response{
		{Message: Message{Role: RoleAssistant, ToolCalls: []ToolCall{
			delayCall("c1", "a", 40),
			delayCall("c2", "b", 40),
		}}},
		{Message: Message{Role: RoleAssistant, Content: "done"}},
	}}
	runner := mustRunner(t, RunnerOptions{
		Model:           model,
		Tools:           []Tool{delayTool(t)},
		SequentialTools: true,
	})

	start := time.Now()
	if _, err := collect(runner.Run(context.Background(), NewSession("s"), "go")); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed < 70*time.Millisecond {
		t.Errorf("elapsed = %v; SequentialTools must not overlap", elapsed)
	}
}

// TestReActRunner_ParallelToolPanicIsolated 验证一个工具 panic 不影响同批其他工具。
func TestReActRunner_ParallelToolPanicIsolated(t *testing.T) {
	boom, err := NewFuncTool("boom", "总是 panic",
		func(ctx context.Context, a delayArgs) (ToolResult, error) {
			panic("tool exploded")
		})
	if err != nil {
		t.Fatal(err)
	}

	model := &scriptedModel{responses: []Response{
		{Message: Message{Role: RoleAssistant, ToolCalls: []ToolCall{
			delayCall("c1", "ok", 10),
			{ID: "c2", Name: "boom", Arguments: json.RawMessage(`{}`)},
			delayCall("c3", "ok2", 10),
		}}},
		{Message: Message{Role: RoleAssistant, Content: "done"}},
	}}
	runner := mustRunner(t, RunnerOptions{Model: model, Tools: []Tool{delayTool(t), boom}})

	events, err := collect(runner.Run(context.Background(), NewSession("s"), "go"))
	if err != nil {
		t.Fatalf("panicking tool must not fail the run: %v", err)
	}
	if got := countType(events, EventPanic); got != 1 {
		t.Errorf("panic events = %d, want 1", got)
	}
	if got := countType(events, EventToolResult); got != 3 {
		t.Errorf("tool results = %d, want 3 (panic converted to soft error)", got)
	}
}
