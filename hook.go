package tinyagent

import "context"

// Hook 在 agent 运行的关键节点被同步调用。
//
// 实现方可以：
//   - 观察：读取指针指向的值
//   - 修改：写入指针指向的值
//   - 否决：返回错误
//
// 注意：接口中**不存在 panic 钩子** —— panic 上报是内建强制机制，
// 不可插拔（见 VISION §4.3）。
//
// Hook 在热路径上同步执行，实现应保持轻量。
type Hook interface {
	// BeforeModelCall 在调用模型前触发。返回错误将终止本次运行。
	BeforeModelCall(ctx context.Context, req *Request) error
	// AfterModelCall 在模型返回后触发。返回错误将终止本次运行。
	AfterModelCall(ctx context.Context, resp *Response) error
	// BeforeToolCall 在工具执行前触发。返回错误将否决本次调用。
	BeforeToolCall(ctx context.Context, call *ToolCall) error
	// AfterToolCall 在工具执行后触发。
	AfterToolCall(ctx context.Context, call *ToolCall, res *ToolResult) error
}

// HookFuncs 是 Hook 的函数式实现。
//
// nil 字段表示在该节点不介入，便于只关心少数节点时使用。
type HookFuncs struct {
	OnBeforeModelCall func(ctx context.Context, req *Request) error
	OnAfterModelCall  func(ctx context.Context, resp *Response) error
	OnBeforeToolCall  func(ctx context.Context, call *ToolCall) error
	OnAfterToolCall   func(ctx context.Context, call *ToolCall, res *ToolResult) error
}

// 编译期断言：HookFuncs 实现 Hook。
var _ Hook = HookFuncs{}

// BeforeModelCall 实现 Hook。
func (h HookFuncs) BeforeModelCall(ctx context.Context, req *Request) error {
	if h.OnBeforeModelCall == nil {
		return nil
	}
	return h.OnBeforeModelCall(ctx, req)
}

// AfterModelCall 实现 Hook。
func (h HookFuncs) AfterModelCall(ctx context.Context, resp *Response) error {
	if h.OnAfterModelCall == nil {
		return nil
	}
	return h.OnAfterModelCall(ctx, resp)
}

// BeforeToolCall 实现 Hook。
func (h HookFuncs) BeforeToolCall(ctx context.Context, call *ToolCall) error {
	if h.OnBeforeToolCall == nil {
		return nil
	}
	return h.OnBeforeToolCall(ctx, call)
}

// AfterToolCall 实现 Hook。
func (h HookFuncs) AfterToolCall(ctx context.Context, call *ToolCall, res *ToolResult) error {
	if h.OnAfterToolCall == nil {
		return nil
	}
	return h.OnAfterToolCall(ctx, call, res)
}
