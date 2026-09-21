package tinyagent

// ToolCallAccumulator 把流式工具调用分片聚合成完整调用。
//
// 流式协议中，一次工具调用的 name 与 id 只在首片出现，arguments 分多片到达，
// 因此必须按 Index 聚合后拼接，才能得到可执行的 ToolCall。
type ToolCallAccumulator struct {
	byIndex map[int]*ToolCall
	order   []int
}

// NewToolCallAccumulator 创建一个空聚合器。
func NewToolCallAccumulator() *ToolCallAccumulator {
	return &ToolCallAccumulator{byIndex: make(map[int]*ToolCall)}
}

// Add 吸收一个分片。
func (a *ToolCallAccumulator) Add(d *ToolCallDelta) {
	if d == nil {
		return
	}
	cur, ok := a.byIndex[d.Index]
	if !ok {
		cur = &ToolCall{}
		a.byIndex[d.Index] = cur
		a.order = append(a.order, d.Index)
	}
	if d.ID != "" {
		cur.ID = d.ID
	}
	if d.Name != "" {
		cur.Name = d.Name
	}
	if d.ArgumentsDelta != "" {
		cur.Arguments = append(cur.Arguments, d.ArgumentsDelta...)
	}
}

// Calls 返回按出现顺序排列的完整工具调用。
//
// 参数为空的调用会补成 "{}"，避免下游解析空参数时报错。
func (a *ToolCallAccumulator) Calls() []ToolCall {
	out := make([]ToolCall, 0, len(a.order))
	for _, idx := range a.order {
		tc := a.byIndex[idx]
		if len(tc.Arguments) == 0 {
			tc.Arguments = []byte("{}")
		}
		out = append(out, *tc)
	}
	return out
}

// Empty 报告是否没有任何工具调用。
func (a *ToolCallAccumulator) Empty() bool { return len(a.order) == 0 }
