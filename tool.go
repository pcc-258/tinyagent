package tinyagent

import (
	"context"
	"encoding/json"
)

// ToolInfo 描述一个工具，用于告诉模型有哪些工具可用。
type ToolInfo struct {
	// Name 是工具名，必须在一次运行内唯一。
	Name string
	// Description 描述工具用途，模型据此决定是否调用。
	Description string
	// Parameters 是参数的 JSON Schema。
	Parameters json.RawMessage
}

// Tool 是 agent 可以调用的工具。
type Tool interface {
	// Info 返回工具定义。
	Info() ToolInfo
	// Run 执行工具。
	//
	// 错误语义：
	//   - 返回 error 表示框架级失败，会终止本次运行；
	//   - 返回 ToolResult{Error: ...} 表示业务软错误，会喂回模型，运行继续。
	Run(ctx context.Context, args json.RawMessage) (ToolResult, error)
}
