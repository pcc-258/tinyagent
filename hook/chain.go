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
// 未配置 Guarded 的 Chain 不捕获 panic。Runner 使用按运行配置的
// Guarded 视图，逐个隔离 Hook 并通过运行事件与审计上报。
type Chain struct {
	hooks       []Hook
	guard       *core.Recoverer
	onPanic     func(error) error
	failOnPanic bool
}

// Guarded returns a per-run view. Panic reporting is supplied by the runner;
// a recovered hook is skipped unless the configured policy fails the run.
func (c *Chain) Guarded(guard *core.Recoverer, onPanic func(error) error, failOnPanic bool) *Chain {
	return &Chain{hooks: c.hooks, guard: guard, onPanic: onPanic, failOnPanic: failOnPanic}
}

func (c *Chain) invoke(i int, op string, fn func() error) error {
	if c.guard == nil {
		return fn()
	}
	_, err := core.GuardValue(c.guard, fmt.Sprintf("hook[%d].%s", i, op), func() (struct{}, error) { return struct{}{}, fn() })
	if core.IsPanic(err) {
		if c.onPanic != nil {
			if reportErr := c.onPanic(err); reportErr != nil {
				return reportErr
			}
		}
		if !c.failOnPanic {
			return nil
		}
	}
	return err
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
		if err := c.invoke(i, "BeforeModelCall", func() error { return h.BeforeModelCall(ctx, req) }); err != nil {
			return c.wrap(i, "BeforeModelCall", err)
		}
	}
	return nil
}

func (c *Chain) AfterModelCall(ctx context.Context, resp *model.Response) error {
	for i, h := range c.hooks {
		if err := c.invoke(i, "AfterModelCall", func() error { return h.AfterModelCall(ctx, resp) }); err != nil {
			return c.wrap(i, "AfterModelCall", err)
		}
	}
	return nil
}

func (c *Chain) BeforeToolCall(ctx context.Context, call *core.ToolCall) error {
	for i, h := range c.hooks {
		if err := c.invoke(i, "BeforeToolCall", func() error { return h.BeforeToolCall(ctx, call) }); err != nil {
			return c.wrap(i, "BeforeToolCall", err)
		}
	}
	return nil
}

func (c *Chain) AfterToolCall(ctx context.Context, call *core.ToolCall, res *core.ToolResult) error {
	for i, h := range c.hooks {
		if err := c.invoke(i, "AfterToolCall", func() error { return h.AfterToolCall(ctx, call, res) }); err != nil {
			return c.wrap(i, "AfterToolCall", err)
		}
	}
	return nil
}

func (c *Chain) wrap(index int, op string, err error) error {
	if core.IsPanic(err) {
		return err
	}
	return core.NewError(core.ErrKindHook, fmt.Sprintf("hook[%d]", index), op, err)
}
