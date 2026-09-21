package tinyagent

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
)

// FuncTool 用 Go 函数实现 Tool。
//
// 参数 schema 由 In 的类型自动生成，接入方无需手写 JSON Schema。
type FuncTool[In any] struct {
	info ToolInfo
	fn   func(context.Context, In) (ToolResult, error)
}

// NewFuncTool 从 Go 函数创建一个工具。
//
// name 是工具名，desc 是给模型看的用途说明，fn 是执行逻辑。
//
// In 应当是结构体；其字段可用 `json` tag 指定名称、`desc` tag 指定描述。
// 带 `omitempty` 或为指针的字段视为可选，其余为必填。
//
// 注意：返回的 ToolResult 无需填写 ToolCallID，运行循环会补上。
func NewFuncTool[In any](
	name, desc string,
	fn func(context.Context, In) (ToolResult, error),
) (*FuncTool[In], error) {
	if name == "" {
		return nil, newError(ErrKindTool, "", "new", fmt.Errorf("tool name is empty"))
	}
	if fn == nil {
		return nil, newError(ErrKindTool, name, "new", fmt.Errorf("tool function is nil"))
	}

	schema, err := schemaFromType(reflect.TypeOf((*In)(nil)).Elem())
	if err != nil {
		return nil, newError(ErrKindTool, name, "schema", err)
	}

	return &FuncTool[In]{
		info: ToolInfo{Name: name, Description: desc, Parameters: schema},
		fn:   fn,
	}, nil
}

// Info 实现 Tool。
func (t *FuncTool[In]) Info() ToolInfo { return t.info }

// Run 实现 Tool。
//
// 参数解析失败时返回 ToolResult{Error: ...}（业务软错误）而非 error ——
// 这通常意味着模型给错了参数，应该把原因喂回去让它自行纠正。
func (t *FuncTool[In]) Run(ctx context.Context, args json.RawMessage) (ToolResult, error) {
	var in In
	if len(args) > 0 {
		if err := json.Unmarshal(args, &in); err != nil {
			return ToolResult{
				Error: fmt.Sprintf("invalid arguments for tool %q: %v", t.info.Name, err),
			}, nil
		}
	}
	return t.fn(ctx, in)
}
