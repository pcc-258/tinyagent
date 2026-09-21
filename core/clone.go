package core

import "encoding/json"

// CloneMessage 深拷贝一条消息。
//
// ToolCalls 与 Arguments 是切片/字节切片，必须复制，否则多个持有者
// 会共享底层数组，产生难以排查的串改。
func CloneMessage(m Message) Message {
	out := m
	if m.ToolCalls != nil {
		out.ToolCalls = make([]ToolCall, len(m.ToolCalls))
		for i, tc := range m.ToolCalls {
			out.ToolCalls[i] = tc
			if tc.Arguments != nil {
				out.ToolCalls[i].Arguments = append(json.RawMessage(nil), tc.Arguments...)
			}
		}
	}
	out.Meta = CloneMeta(m.Meta)
	return out
}

// CloneMeta 浅拷贝元信息 map。nil 输入返回 nil。
func CloneMeta(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
