package tinyagent

import "encoding/json"

// Role 标识消息的发起方。
type Role string

const (
	// RoleSystem 是系统提示。
	RoleSystem Role = "system"
	// RoleUser 是用户输入。
	RoleUser Role = "user"
	// RoleAssistant 是模型输出。
	RoleAssistant Role = "assistant"
	// RoleTool 是工具执行结果。
	RoleTool Role = "tool"
)

// Message 是会话中的一条消息。
//
// Message 的 JSON 表示是稳定契约：Model 适配器与 Store 实现都依赖它，
// 变更需谨慎。
type Message struct {
	Role       Role           `json:"role"`
	Content    string         `json:"content,omitempty"`
	ToolCalls  []ToolCall     `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
	Name       string         `json:"name,omitempty"`
	Meta       map[string]any `json:"meta,omitempty"`
}

// ToolCall 是模型发起的一次工具调用请求。
type ToolCall struct {
	// ID 是本次调用的唯一标识，用于把结果配回去。
	ID string `json:"id"`
	// Name 是工具名。
	Name string `json:"name"`
	// Arguments 是工具参数，JSON 编码。
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

// ToolResult 是一次工具调用的结果。
//
// 错误语义（见 BUILD_PLAN P3）：
//   - Error 非空表示「业务软错误」，会作为观察喂回模型，循环继续；
//   - 工具执行返回的 error 表示「框架级失败」，终止本次运行。
type ToolResult struct {
	// ToolCallID 对应发起调用的 ToolCall.ID。
	ToolCallID string `json:"tool_call_id"`
	// Content 是结果内容。
	Content string `json:"content,omitempty"`
	// Error 是业务软错误描述。
	Error string `json:"error,omitempty"`
	// Meta 是可选附加信息。
	Meta map[string]any `json:"meta,omitempty"`
}

// IsError 报告该结果是否代表业务错误。
func (r ToolResult) IsError() bool { return r.Error != "" }

// MessageFromToolResult 把工具结果转成一条 RoleTool 消息。
func MessageFromToolResult(r ToolResult) Message {
	content := r.Content
	if r.Error != "" {
		content = r.Error
	}
	return Message{
		Role:       RoleTool,
		Content:    content,
		ToolCallID: r.ToolCallID,
	}
}
