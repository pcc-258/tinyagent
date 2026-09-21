package tinyagent

import "context"

// CountRequest 是 token 计数请求。
type CountRequest struct {
	// Messages 是要计数的消息。
	Messages []Message
	// Tools 是工具定义 —— 必须计入，工具 schema 往往很耗 token。
	Tools []ToolInfo
	// System 是系统提示。
	System string
}

// CountResult 是 token 计数结果。
type CountResult struct {
	// Total 是总估算值。
	Total int
	// ByPart 是分项估算，键如 "messages" / "tools" / "system"。
	ByPart map[string]int
}

// Counter 估算 token 数量。
//
// 实现必须覆盖工具 schema —— 只算消息会严重低估（见 VISION §6.4 M4）。
type Counter interface {
	Count(ctx context.Context, req CountRequest) (CountResult, error)
}

// ContextStrategy 是一种上下文处理策略。
//
// 截断、压缩、卸载、选择等都是策略的实现；每种策略独立可替换、可组合
// （见 VISION §6.4 M4）。压缩只是其中一种。
type ContextStrategy interface {
	// Name 返回策略名，用于事件与审计。
	Name() string
	// Apply 对消息序列做一次处理。
	//
	// budget 是允许的 token 上限。返回处理后的消息与本次动作描述；
	// 若未做任何处理，返回 nil 动作。
	Apply(ctx context.Context, msgs []Message, budget int) ([]Message, *ContextAction, error)
}

// ContextManager 负责全部上下文处理。
type ContextManager interface {
	// Prepare 在每次模型调用前处理上下文。
	//
	// 返回处理后的消息序列，以及本次发生的所有动作（供事件与审计使用）。
	// 实现必须保证处理结果中「工具调用 ↔ 工具结果」配对完整。
	Prepare(
		ctx context.Context,
		msgs []Message,
		tools []ToolInfo,
		system string,
		budget int,
	) ([]Message, []ContextAction, error)
}
