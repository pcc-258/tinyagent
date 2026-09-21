package tinyagent

import (
	"context"
	"fmt"
)

// hookChain 按顺序调用一组 Hook，任一返回错误即中止。
//
// 错误被包装为 *Error{Kind: ErrKindHook}，Component 标明是第几个 Hook。
// 注意：hookChain 不负责 panic 捕获 —— 那是 recoverer 的职责，
// 由调用方（Runner）在更外层包裹，以保证 panic 上报通道不被 Hook 绕过。
type hookChain struct {
	hooks []Hook
}

// newHookChain 构造调用链，自动过滤 nil，避免空槽导致 panic。
func newHookChain(hooks []Hook) *hookChain {
	filtered := make([]Hook, 0, len(hooks))
	for _, h := range hooks {
		if h != nil {
			filtered = append(filtered, h)
		}
	}
	return &hookChain{hooks: filtered}
}

// Empty 报告链中是否没有 Hook。
func (c *hookChain) Empty() bool { return len(c.hooks) == 0 }

func (c *hookChain) beforeModelCall(ctx context.Context, req *Request) error {
	for i, h := range c.hooks {
		if err := h.BeforeModelCall(ctx, req); err != nil {
			return c.wrap(i, "BeforeModelCall", err)
		}
	}
	return nil
}

func (c *hookChain) afterModelCall(ctx context.Context, resp *Response) error {
	for i, h := range c.hooks {
		if err := h.AfterModelCall(ctx, resp); err != nil {
			return c.wrap(i, "AfterModelCall", err)
		}
	}
	return nil
}

func (c *hookChain) beforeToolCall(ctx context.Context, call *ToolCall) error {
	for i, h := range c.hooks {
		if err := h.BeforeToolCall(ctx, call); err != nil {
			return c.wrap(i, "BeforeToolCall", err)
		}
	}
	return nil
}

func (c *hookChain) afterToolCall(ctx context.Context, call *ToolCall, res *ToolResult) error {
	for i, h := range c.hooks {
		if err := h.AfterToolCall(ctx, call, res); err != nil {
			return c.wrap(i, "AfterToolCall", err)
		}
	}
	return nil
}

func (c *hookChain) wrap(index int, op string, err error) error {
	return newError(ErrKindHook, fmt.Sprintf("hook[%d]", index), op, err)
}
