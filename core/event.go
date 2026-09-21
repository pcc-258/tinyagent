package core

import "time"

// EventType 标识事件的种类。
type EventType string

const (
	// EventRunStart 表示一次运行开始。
	EventRunStart EventType = "run_start"
	// EventText 是模型输出的文本增量。
	EventText EventType = "text"
	// EventToolCall 表示模型请求调用工具。
	EventToolCall EventType = "tool_call"
	// EventToolResult 表示一次工具调用完成。
	EventToolResult EventType = "tool_result"
	// EventContextAction 表示上下文处理发生。
	EventContextAction EventType = "context_action"
	// EventRetry 表示一次重试。
	EventRetry EventType = "retry"
	// EventPanic 表示组件 panic 被捕获。
	EventPanic EventType = "panic"
	// EventRunEnd 表示运行结束。
	EventRunEnd EventType = "run_end"
)

// Event 是运行过程中产生的一条事件。
//
// 只有 Type 与 Time 必定有值，其余字段按 Type 填充：
//
//	EventText          -> Text
//	EventToolCall      -> ToolCall
//	EventToolResult    -> ToolResult
//	EventContextAction -> ContextAction
//	EventPanic         -> Panic
//	EventRunEnd        -> Usage
//	任何错误           -> Err
type Event struct {
	// Type 是事件类型。
	Type EventType

	// Text 在 EventText 时有效：模型输出的文本增量。
	Text string
	// ToolCall 在 EventToolCall 时有效。
	ToolCall *ToolCall
	// ToolResult 在 EventToolResult 时有效。
	ToolResult *ToolResult
	// ContextAction 在 EventContextAction 时有效。
	ContextAction *ContextAction
	// Panic 在 EventPanic 时有效。
	Panic *PanicInfo
	// Usage 在 EventRunEnd 时有效（若模型返回了用量）。
	Usage *Usage
	// Err 在事件代表错误时有效。
	Err error

	// Time 是事件产生时间。
	Time time.Time
}

// ContextAction 描述一次上下文处理。
type ContextAction struct {
	// Strategy 是触发的策略名，如 "truncate" / "summarize" / "offload"。
	Strategy string
	// Before 是处理前的 token 估算。
	Before int
	// After 是处理后的 token 估算。
	After int
	// Detail 是策略自定义信息。
	Detail map[string]any
}
