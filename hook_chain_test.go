package tinyagent

import (
	"context"
	"testing"
)

func TestHookChain_Order(t *testing.T) {
	var order []string
	h1 := HookFuncs{OnBeforeToolCall: func(context.Context, *ToolCall) error {
		order = append(order, "h1")
		return nil
	}}
	h2 := HookFuncs{OnBeforeToolCall: func(context.Context, *ToolCall) error {
		order = append(order, "h2")
		return nil
	}}

	chain := newHookChain([]Hook{h1, h2})
	if err := chain.beforeToolCall(context.Background(), &ToolCall{Name: "t"}); err != nil {
		t.Fatal(err)
	}
	if len(order) != 2 || order[0] != "h1" || order[1] != "h2" {
		t.Errorf("order = %v, want [h1 h2]", order)
	}
}

// TestHookChain_RejectStops 验证否决会立即中止链。
func TestHookChain_RejectStops(t *testing.T) {
	var reached bool
	h1 := HookFuncs{OnBeforeToolCall: func(context.Context, *ToolCall) error {
		return ErrHookRejected
	}}
	h2 := HookFuncs{OnBeforeToolCall: func(context.Context, *ToolCall) error {
		reached = true
		return nil
	}}

	chain := newHookChain([]Hook{h1, h2})
	err := chain.beforeToolCall(context.Background(), &ToolCall{})
	if err == nil {
		t.Fatal("expected rejection error")
	}
	if reached {
		t.Error("second hook must not run after rejection")
	}
	e, ok := AsError(err)
	if !ok || e.Kind != ErrKindHook {
		t.Errorf("error kind = %+v, want ErrKindHook", err)
	}
	if e.Component != "hook[0]" {
		t.Errorf("component = %q, want hook[0]", e.Component)
	}
}

// TestHookChain_Mutation 验证 Hook 可以就地修改参数。
func TestHookChain_Mutation(t *testing.T) {
	h := HookFuncs{OnBeforeToolCall: func(_ context.Context, call *ToolCall) error {
		call.Name = "rewritten"
		return nil
	}}

	chain := newHookChain([]Hook{h})
	call := &ToolCall{Name: "original"}
	if err := chain.beforeToolCall(context.Background(), call); err != nil {
		t.Fatal(err)
	}
	if call.Name != "rewritten" {
		t.Errorf("hook mutation not applied: %q", call.Name)
	}
}

func TestHookChain_NilFiltered(t *testing.T) {
	chain := newHookChain([]Hook{nil, HookFuncs{}, nil})
	if !chain.Empty() {
		t.Error("expected empty chain after filtering nil")
	}
	if err := chain.beforeModelCall(context.Background(), &Request{}); err != nil {
		t.Errorf("empty chain should be no-op: %v", err)
	}
}

func TestHookChain_AllPoints(t *testing.T) {
	var seen []string
	h := HookFuncs{
		OnBeforeModelCall: func(context.Context, *Request) error {
			seen = append(seen, "bm")
			return nil
		},
		OnAfterModelCall: func(context.Context, *Response) error {
			seen = append(seen, "am")
			return nil
		},
		OnBeforeToolCall: func(context.Context, *ToolCall) error {
			seen = append(seen, "bt")
			return nil
		},
		OnAfterToolCall: func(context.Context, *ToolCall, *ToolResult) error {
			seen = append(seen, "at")
			return nil
		},
	}
	chain := newHookChain([]Hook{h})
	ctx := context.Background()

	if err := chain.beforeModelCall(ctx, &Request{}); err != nil {
		t.Fatal(err)
	}
	if err := chain.afterModelCall(ctx, &Response{}); err != nil {
		t.Fatal(err)
	}
	if err := chain.beforeToolCall(ctx, &ToolCall{}); err != nil {
		t.Fatal(err)
	}
	if err := chain.afterToolCall(ctx, &ToolCall{}, &ToolResult{}); err != nil {
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
