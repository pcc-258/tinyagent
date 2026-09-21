package core

import "errors"

// ErrorKind 标识错误的来源模块。
type ErrorKind string

const (
	// ErrKindConfig 表示配置错误。
	ErrKindConfig ErrorKind = "config"
	// ErrKindModel 表示模型调用错误。
	ErrKindModel ErrorKind = "model"
	// ErrKindTool 表示工具相关错误。
	ErrKindTool ErrorKind = "tool"
	// ErrKindStore 表示存储相关错误。
	ErrKindStore ErrorKind = "store"
	// ErrKindContext 表示上下文处理错误。
	ErrKindContext ErrorKind = "context"
	// ErrKindHook 表示 Hook 相关错误。
	ErrKindHook ErrorKind = "hook"
	// ErrKindAudit 表示审计相关错误。
	ErrKindAudit ErrorKind = "audit"
	// ErrKindRunner 表示运行循环错误。
	ErrKindRunner ErrorKind = "runner"
	// ErrKindLimit 表示触达上限或超时。
	ErrKindLimit ErrorKind = "limit"
	// ErrKindPanic 表示捕获到了组件 panic。
	ErrKindPanic ErrorKind = "panic"
)

// Error 是库的统一错误类型。
type Error struct {
	// Kind 标识来源模块。
	Kind ErrorKind
	// Component 是具体组件名，如 "tool:get_weather"。
	Component string
	// Op 是发生错误的操作。
	Op string
	// Err 是被包装的底层错误。
	Err error
	// Stack 是 panic 发生时的堆栈（仅 Kind == ErrKindPanic 时有值）。
	Stack []byte
}

// Error 实现 error 接口。
func (e *Error) Error() string {
	s := ""
	if e.Component != "" {
		s = e.Component + ": "
	}
	if e.Op != "" {
		s += e.Op + ": "
	}
	if e.Err != nil {
		s += e.Err.Error()
	}
	if s == "" {
		return string(e.Kind)
	}
	return s
}

// Unwrap 返回被包装的底层错误。
func (e *Error) Unwrap() error { return e.Err }

// NewError 构造一个 *Error。
func NewError(kind ErrorKind, component, op string, err error) *Error {
	return &Error{Kind: kind, Component: component, Op: op, Err: err}
}

// PanicInfo 描述一次被捕获的 panic。
type PanicInfo struct {
	// Component 是发生 panic 的组件名。
	Component string
	// Value 是 recover() 拿到的值。
	Value any
	// Stack 是 panic 时的堆栈。
	Stack []byte
}

// PanicPolicy 决定组件 panic 之后如何处理。
//
// 无论采用哪种策略，panic 都会被记录并通过三通道上报（见 VISION §4.3）：
// error 返回、事件推送、结构化日志。这一上报机制不可关闭，只能重定向。
type PanicPolicy int

const (
	// PanicRecoverAsToolError 把 panic 转为业务软错误喂回模型，运行继续。
	// 这是默认策略。
	PanicRecoverAsToolError PanicPolicy = iota

	// PanicFailRun 把 panic 转为错误，终止本次运行。
	PanicFailRun

	// PanicPropagate 不捕获 panic，任其向外传播，由宿主自行处理。
	PanicPropagate
)

// 预定义错误。
var (
	// ErrSessionBusy 表示同一会话已有运行在进行。
	ErrSessionBusy = errors.New("tinyagent: session is busy")
	// ErrMaxIterations 表示触达迭代上限。
	ErrMaxIterations = errors.New("tinyagent: max iterations reached")
	// ErrHookRejected 表示调用被 Hook 否决。
	ErrHookRejected = errors.New("tinyagent: rejected by hook")
	// ErrNotFound 表示请求的对象不存在。
	ErrNotFound = errors.New("tinyagent: not found")
)
