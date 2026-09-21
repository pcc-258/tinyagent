package ctxmgr

import (
	"context"
	"github.com/pcc-258/tinyagent/core"
	"github.com/pcc-258/tinyagent/tool"
)

// DefaultContextManager 是 ContextManager 的默认实现。
//
// 它按顺序应用策略链，每次应用后重新估算，直到落入预算。
// 默认链为：卸载大结果 → 截断旧历史。
type DefaultContextManager struct {
	// Counter 是 token 估算器，为空时不做任何处理。
	Counter Counter
	// Strategies 是策略链，按顺序尝试。
	Strategies []ContextStrategy
}

// NewDefaultContextManager 用推荐默认策略构造上下文管理器。
func NewDefaultContextManager(counter Counter) *DefaultContextManager {
	return &DefaultContextManager{
		Counter: counter,
		Strategies: []ContextStrategy{
			&OffloadStrategy{Offloader: NewMemoryOffloader()},
			&TruncateStrategy{},
		},
	}
}

// Prepare 实现 ContextManager。
//
// 返回处理后的消息序列，以及本次发生的所有上下文动作（供事件与审计使用）。
func (m *DefaultContextManager) Prepare(
	ctx context.Context,
	msgs []core.Message,
	tools []tool.ToolInfo,
	system string,
	budget int,
) ([]core.Message, []core.ContextAction, error) {
	if m.Counter == nil || budget <= 0 {
		return msgs, nil, nil
	}

	cur := msgs
	var actions []core.ContextAction

	for _, st := range m.Strategies {
		count, err := m.Counter.Count(ctx, CountRequest{Messages: cur, Tools: tools, System: system})
		if err != nil {
			return nil, actions, err
		}
		if count.Total <= budget {
			break
		}

		next, action, err := st.Apply(ctx, cur, budget)
		if err != nil {
			return nil, actions, err
		}
		if action != nil {
			action.Before = count.Total
			if after, err := m.Counter.Count(ctx, CountRequest{Messages: next, Tools: tools, System: system}); err == nil {
				action.After = after.Total
			}
			actions = append(actions, *action)
		}
		cur = next
	}

	return cur, actions, nil
}
