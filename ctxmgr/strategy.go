package ctxmgr

import (
	"context"
	"fmt"
	"github.com/pcc-258/tinyagent/core"
	"sync"
	"time"
)

// TruncateStrategy 丢弃较早的历史，只保留最近的若干条消息。
//
// 属于「截断」类策略：不改变保留消息的内容，因此对近期上下文无损失。
type TruncateStrategy struct {
	// KeepRecent 是最少保留的最近消息条数，零值时取 20。
	KeepRecent int
	// Counter enables additional pair-preserving trimming to fit the message budget.
	// KeepRecent is then a preferred retention size, not a hard minimum.
	Counter Counter
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
	start := 0
	if len(msgs) > keep {
		start = safeCutPoint(msgs, len(msgs)-keep)
	}
	if s.Counter != nil {
		for start < len(msgs)-1 {
			count, err := s.Counter.Count(ctx, CountRequest{Messages: msgs[start:]})
			if err != nil {
				return nil, nil, err
			}
			if count.Total <= budget {
				break
			}
			next := start + 1
			for next < len(msgs) && msgs[next].Role == core.RoleTool {
				next++
			}
			if next >= len(msgs) {
				break
			}
			start = next
		}
	}
	if start == 0 {
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
		out[i].Content = fmt.Sprintf("[offloaded:%s] %d bytes moved out of context; use read_offloaded with this ref to retrieve ranges", ref, len(msgs[i].Content))
		out[i].Meta = core.CloneMeta(out[i].Meta)
		if out[i].Meta == nil {
			out[i].Meta = make(map[string]any)
		}
		out[i].Meta[offloadRefKey] = ref
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
	mu      sync.Mutex
	items   map[string]offloadedItem
	seq     int
	bytes   int
	options MemoryOffloaderOptions
}

type offloadedItem struct {
	content string
	expires time.Time
}

// MemoryOffloaderOptions bounds storage. A full store returns an error instead
// of silently evicting live references. TTL <=0 disables expiry. Zero limits
// default to 1024 items and 64 MiB; negative limits disable the corresponding cap.
type MemoryOffloaderOptions struct {
	MaxItems int
	MaxBytes int
	TTL      time.Duration
}

// NewMemoryOffloader 创建一个内存卸载器。
func NewMemoryOffloader() *MemoryOffloader {
	return NewMemoryOffloaderWithOptions(MemoryOffloaderOptions{})
}

func NewMemoryOffloaderWithOptions(opts MemoryOffloaderOptions) *MemoryOffloader {
	if opts.MaxItems == 0 {
		opts.MaxItems = 1024
	}
	if opts.MaxBytes == 0 {
		opts.MaxBytes = 64 * 1024 * 1024
	}
	return &MemoryOffloader{items: make(map[string]offloadedItem), options: opts}
}

// Offload 实现 Offloader。
func (o *MemoryOffloader) Offload(ctx context.Context, content string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	o.expireLocked()
	if (o.options.MaxItems > 0 && len(o.items) >= o.options.MaxItems) || (o.options.MaxBytes > 0 && len(content) > o.options.MaxBytes-o.bytes) {
		return "", fmt.Errorf("offloader capacity exceeded; delete unused references or use a persistent offloader")
	}
	o.seq++
	ref := fmt.Sprintf("mem-%d", o.seq)
	item := offloadedItem{content: content}
	if o.options.TTL > 0 {
		item.expires = time.Now().Add(o.options.TTL)
	}
	o.items[ref] = item
	o.bytes += len(content)
	return ref, nil
}

// Retrieve 实现 Offloader。
func (o *MemoryOffloader) Retrieve(ctx context.Context, ref string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	o.expireLocked()
	v, ok := o.items[ref]
	if !ok {
		return "", core.ErrNotFound
	}
	return v.content, nil
}

// Delete releases an unused reference. Callers must ensure no live history needs it.
func (o *MemoryOffloader) Delete(ctx context.Context, ref string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	v, ok := o.items[ref]
	if !ok {
		return core.ErrNotFound
	}
	delete(o.items, ref)
	o.bytes -= len(v.content)
	return nil
}

func (o *MemoryOffloader) expireLocked() {
	if o.options.TTL <= 0 {
		return
	}
	now := time.Now()
	for ref, item := range o.items {
		if !item.expires.IsZero() && !now.Before(item.expires) {
			delete(o.items, ref)
			o.bytes -= len(item.content)
		}
	}
}
