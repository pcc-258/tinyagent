package hook

import (
	"context"
	"testing"
	"github.com/pcc-258/tinyagent/core"
	"github.com/pcc-258/tinyagent/model"
)

func TestHookChain_Order(t *testing.T) {
	var order []string
	h1 := HookFuncs{OnBeforeToolCall: func(context.Context, *core.ToolCall) error {
		order = append(order, "h1")
		return nil
	}}
	h2 := HookFuncs{OnBeforeToolCall: func(context.Context, *core.ToolCall) error {
		order = append(order, "h2")
		return nil
	}}

	chain := NewChain([]Hook{h1, h2})
	if err := chain.BeforeToolCall(context.Background(), &core.ToolCall{Name: "t"}); err != nil {
		t.Fatal(err)
	}
	if len(order) != 2 || order[0] != "h1" || order[1] != "h2" {
		t.Errorf("order = %v, want [h1 h2]", order)
	}
}

// TestHookChain_RejectStops 验证否决会立即中止链。
func TestHookChain_RejectStops(t *testing.T) {
	var reached bool
	h1 := HookFuncs{OnBeforeToolCall: func(context.Context, *core.ToolCall) error {
		return core.ErrHookRejected
	}}
	h2 := HookFuncs{OnBeforeToolCall: func(context.Context, *core.ToolCall) error {
		reached = true
		return nil
	}}

	chain := NewChain([]Hook{h1, h2})
	err := chain.BeforeToolCall(context.Background(), &core.ToolCall{})
	if err == nil {
		t.Fatal("expected rejection error")
	}
	if reached {
		t.Error("second hook must not run after rejection")
	}
	e, ok := core.AsError(err)
	if !ok || e.Kind != core.ErrKindHook {
		t.Errorf("error kind = %+v, want core.ErrKindHook", err)
	}
	if e.Component != "hook[0]" {
		t.Errorf("component = %q, want hook[0]", e.Component)
	}
}

// TestHookChain_Mutation 验证 Hook 可以就地修改参数。
func TestHookChain_Mutation(t *testing.T) {
	h := HookFuncs{OnBeforeToolCall: func(_ context.Context, call *core.ToolCall) error {
		call.Name = "rewritten"
		return nil
	}}

	chain := NewChain([]Hook{h})
	call := &core.ToolCall{Name: "original"}
	if err := chain.BeforeToolCall(context.Background(), call); err != nil {
		t.Fatal(err)
	}
	if call.Name != "rewritten" {
		t.Errorf("hook mutation not applied: %q", call.Name)
	}
}

func TestHookChain_NilFiltered(t *testing.T) {
	chain := NewChain([]Hook{nil, nil})
	if !chain.Empty() {
		t.Error("expected empty chain after filtering nil slots")
	}
	if err := chain.BeforeModelCall(context.Background(), &model.Request{}); err != nil {
		t.Errorf("empty chain should be no-op: %v", err)
	}

	chain2 := NewChain([]Hook{HookFuncs{}})
	if chain2.Empty() {
		t.Error("HookFuncs{} is a valid no-op hook and must not be filtered")
	}
}

func TestHookChain_AllPoints(t *testing.T) {
	var seen []string
	h := HookFuncs{
		OnBeforeModelCall: func(context.Context, *model.Request) error {
			seen = append(seen, "bm")
			return nil
		},
		OnAfterModelCall: func(context.Context, *model.Response) error {
			seen = append(seen, "am")
			return nil
		},
		OnBeforeToolCall: func(context.Context, *core.ToolCall) error {
			seen = append(seen, "bt")
			return nil
		},
		OnAfterToolCall: func(context.Context, *core.ToolCall, *core.ToolResult) error {
			seen = append(seen, "at")
			return nil
		},
	}
	chain := NewChain([]Hook{h})
	ctx := context.Background()

	if err := chain.BeforeModelCall(ctx, &model.Request{}); err != nil {
		t.Fatal(err)
	}
	if err := chain.AfterModelCall(ctx, &model.Response{}); err != nil {
		t.Fatal(err)
	}
	if err := chain.BeforeToolCall(ctx, &core.ToolCall{}); err != nil {
		t.Fatal(err)
	}
	if err := chain.AfterToolCall(ctx, &core.ToolCall{}, &core.ToolResult{}); err != nil {
		t.Fatal(err)
	}

	want := []string{"bm", "am", "bt", "at"}
	if len(seen) != len(want) {
		t.Fatalf("seen = %v, want %v", seen, want)
	}
	for i := range want {
		if seen[i] != want[i] {
			t.Errorf("seen[%d] = %q, want %q", i, seen[i], want[i])
		}
	}
}
