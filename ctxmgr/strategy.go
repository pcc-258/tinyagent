package ctxmgr

import (
	"context"
	"fmt"
	"sync"
	"github.com/pcc-258/tinyagent/core"
)

// TruncateStrategy 丢弃较早的历史，只保留最近的若干条消息。
//
// 属于「截断」类策略：不改变保留消息的内容，因此对近期上下文无损失。
type TruncateStrategy struct {
	// KeepRecent 是最少保留的最近消息条数，零值时取 20。
	KeepRecent int
}

// Name 实现 ContextStrategy。
func (s *TruncateStrategy) Name() string { return "truncate" }

// Apply 实现 ContextStrategy。
func (s *TruncateStrategy) Apply(ctx context.Context, msgs []core.Message, budget int) ([]core.Message, *core.ContextAction, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	keep := s.KeepRecent
	if keep <= 0 {
		keep = 20
	}
	if len(msgs) <= keep {
		return msgs, nil, nil
	}

	start := safeCutPoint(msgs, len(msgs)-keep)
	if start <= 0 {
		return msgs, nil, nil
	}

	out := make([]core.Message, len(msgs)-start)
	copy(out, msgs[start:])
	return out, &core.ContextAction{
		Strategy: s.Name(),
		Detail:   map[string]any{"dropped_messages": start},
	}, nil
}

// safeCutPoint 把裁剪点前移，直到不会切断 tool_call ↔ tool_result 配对。
//
// 工具结果必须与其对应的 assistant 工具调用一起保留，
// 否则 OpenAI 兼容端点会直接返回 400。这是最容易踩的坑。
func safeCutPoint(msgs []core.Message, start int) int {
	for start > 0 && msgs[start].Role == core.RoleTool {
		start--
	}
	return start
}

// Offloader 把大块内容移出上下文，返回一个可引用的句柄。
type Offloader interface {
	// Offload 保存内容并返回引用标识。
	Offload(ctx context.Context, content string) (ref string, err error)
	// Retrieve 按引用取回内容。
	Retrieve(ctx context.Context, ref string) (string, error)
}

// OffloadStrategy 把过大的工具结果移出上下文，原地留下引用标记。
//
// 属于「卸载」类策略：内容仍在，只是不再占用上下文预算。
type OffloadStrategy struct {
	// Threshold 是触发卸载的字符数阈值，零值时取 4096。
	Threshold int
	// Offloader 是落地位置，为空时本策略不生效。
	Offloader Offloader
}

// Name 实现 ContextStrategy。
func (s *OffloadStrategy) Name() string { return "offload" }

// Apply 实现 ContextStrategy。
func (s *OffloadStrategy) Apply(ctx context.Context, msgs []core.Message, budget int) ([]core.Message, *core.ContextAction, error) {
	if s.Offloader == nil {
		return msgs, nil, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}

	threshold := s.Threshold
	if threshold <= 0 {
		threshold = 4096
	}

	out := make([]core.Message, len(msgs))
	copy(out, msgs)

	offloaded := 0
	for i := range out {
		if out[i].Role != core.RoleTool || len(out[i].Content) <= threshold {
			continue
		}
		ref, err := s.Offloader.Offload(ctx, out[i].Content)
		if err != nil {
			return nil, nil, fmt.Errorf("offload tool result: %w", err)
		}
		out[i].Content = fmt.Sprintf("[offloaded:%s] %d bytes moved out of context", ref, len(msgs[i].Content))
		offloaded++
	}

	if offloaded == 0 {
		return msgs, nil, nil
	}
	return out, &core.ContextAction{
		Strategy: s.Name(),
		Detail:   map[string]any{"offloaded_results": offloaded},
	}, nil
}

// MemoryOffloader 是 Offloader 的内存实现，进程退出即丢失。
type MemoryOffloader struct {
	mu    sync.Mutex
	items map[string]string
	seq   int
}

// NewMemoryOffloader 创建一个内存卸载器。
func NewMemoryOffloader() *MemoryOffloader {
	return &MemoryOffloader{items: make(map[string]string)}
}

// Offload 实现 Offloader。
func (o *MemoryOffloader) Offload(ctx context.Context, content string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	o.seq++
	ref := fmt.Sprintf("mem-%d", o.seq)
	o.items[ref] = content
	return ref, nil
}

// Retrieve 实现 Offloader。
func (o *MemoryOffloader) Retrieve(ctx context.Context, ref string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	v, ok := o.items[ref]
	if !ok {
		return "", core.ErrNotFound
	}
	return v, nil
}
