package hook

import (
	"context"
	"fmt"
	"github.com/pcc-258/tinyagent/core"
	"github.com/pcc-258/tinyagent/model"
)

// Chain 按顺序调用一组 Hook，任一返回错误即中止。
//
// 错误被包装为 *Error{Kind: core.ErrKindHook}，Component 标明是第几个 Hook。
// 注意：Chain 不负责 panic 捕获 —— 那是 recoverer 的职责，
// 由调用方（Runner）在更外层包裹，以保证 panic 上报通道不被 Hook 绕过。
type Chain struct {
	hooks []Hook
}

// NewChain 构造调用链，自动过滤 nil，避免空槽导致 panic。
func NewChain(hooks []Hook) *Chain {
	filtered := make([]Hook, 0, len(hooks))
	for _, h := range hooks {
		if h != nil {
			filtered = append(filtered, h)
		}
	}
	return &Chain{hooks: filtered}
}

// Empty 报告链中是否没有 Hook。
func (c *Chain) Empty() bool { return len(c.hooks) == 0 }

func (c *Chain) BeforeModelCall(ctx context.Context, req *model.Request) error {
	for i, h := range c.hooks {
		if err := h.BeforeModelCall(ctx, req); err != nil {
			return c.wrap(i, "BeforeModelCall", err)
		}
	}
	return nil
}

func (c *Chain) AfterModelCall(ctx context.Context, resp *model.Response) error {
	for i, h := range c.hooks {
		if err := h.AfterModelCall(ctx, resp); err != nil {
			return c.wrap(i, "AfterModelCall", err)
		}
	}
	return nil
}

func (c *Chain) BeforeToolCall(ctx context.Context, call *core.ToolCall) error {
	for i, h := range c.hooks {
		if err := h.BeforeToolCall(ctx, call); err != nil {
			return c.wrap(i, "BeforeToolCall", err)
		}
	}
	return nil
}

func (c *Chain) AfterToolCall(ctx context.Context, call *core.ToolCall, res *core.ToolResult) error {
	for i, h := range c.hooks {
		if err := h.AfterToolCall(ctx, call, res); err != nil {
			return c.wrap(i, "AfterToolCall", err)
		}
	}
	return nil
}

func (c *Chain) wrap(index int, op string, err error) error {
	return core.NewError(core.ErrKindHook, fmt.Sprintf("hook[%d]", index), op, err)
}
