package tool

import (
	"fmt"
	"sort"
	"sync"
	"github.com/pcc-258/tinyagent/core"
)

// ToolRegistry 持有已注册的工具，并发安全。
type ToolRegistry struct {
	mu    sync.RWMutex
	tools map[string]Tool
}

// NewToolRegistry 创建一个空注册表。
func NewToolRegistry() *ToolRegistry {
	return &ToolRegistry{tools: make(map[string]Tool)}
}

// Register 注册一个工具。
//
// 工具为 nil、名字为空或名字重复都会返回错误。
func (r *ToolRegistry) Register(t Tool) error {
	if t == nil {
		return core.NewError(core.ErrKindTool, "", "register", fmt.Errorf("tool is nil"))
	}
	info := t.Info()
	if info.Name == "" {
		return core.NewError(core.ErrKindTool, "", "register", fmt.Errorf("tool name is empty"))
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.tools[info.Name]; exists {
		return core.NewError(core.ErrKindTool, info.Name, "register", fmt.Errorf("duplicate tool name"))
	}
	r.tools[info.Name] = t
	return nil
}

// Lookup 按名字查找工具。
func (r *ToolRegistry) Lookup(name string) (Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	return t, ok
}

// List 返回所有工具，按名字排序以保证顺序稳定。
func (r *ToolRegistry) List() []Tool {
	r.mu.RLock()
	names := make([]string, 0, len(r.tools))
	for n := range r.tools {
		names = append(names, n)
	}
	r.mu.RUnlock()

	sort.Strings(names)

	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Tool, 0, len(names))
	for _, n := range names {
		if t, ok := r.tools[n]; ok {
			out = append(out, t)
		}
	}
	return out
}

// Infos 返回所有工具的定义，顺序与 List 一致。
func (r *ToolRegistry) Infos() []ToolInfo {
	tools := r.List()
	out := make([]ToolInfo, 0, len(tools))
	for _, t := range tools {
		out = append(out, t.Info())
	}
	return out
}

// Len 返回工具数量。
func (r *ToolRegistry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.tools)
}
