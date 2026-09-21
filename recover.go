package tinyagent

import (
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
)

// recoverer 统一处理组件 panic。
//
// 设计要点（见 VISION §4.3）：
//   - panic 报告是**内建强制机制**，不可插拔；
//   - 三通道上报：结构化日志 + 上报回调 + error 返回；
//   - 无论采用哪种 PanicPolicy，上报都必然发生。
type recoverer struct {
	policy PanicPolicy
	logger *slog.Logger
	// report 是额外的上报通道（事件推送、审计写入）。可以为 nil，
	// 但日志通道始终存在。
	report func(*PanicInfo)
}

// newRecoverer 构造一个 recoverer。
func newRecoverer(policy PanicPolicy, logger *slog.Logger, report func(*PanicInfo)) *recoverer {
	if logger == nil {
		logger = slog.Default()
	}
	return &recoverer{policy: policy, logger: logger, report: report}
}

// guard 执行 fn 并捕获其中的 panic。
//
// 返回值语义：
//   - fn 返回 nil          → nil
//   - fn 返回错误          → 原样返回
//   - 捕获 panic 且策略非 Propagate → *Error{Kind: ErrKindPanic}
//   - 策略为 PanicPropagate → 重新 panic
func (r *recoverer) guard(component string, fn func() error) (err error) {
	defer func() {
		if v := recover(); v != nil {
			err = r.handle(component, v)
		}
	}()
	return fn()
}

// guardValue 是 guard 的带返回值版本。
func guardValue[T any](r *recoverer, component string, fn func() (T, error)) (v T, err error) {
	defer func() {
		if p := recover(); p != nil {
			var zero T
			v = zero
			err = r.handle(component, p)
		}
	}()
	return fn()
}

// handle 处理一个被捕获的 panic，并完成三通道上报。
func (r *recoverer) handle(component string, v any) error {
	stack := debug.Stack()
	info := &PanicInfo{
		Component: component,
		Value:     v,
		Stack:     stack,
	}

	// 通道 1：结构化日志 —— 始终存在，不可关闭，只能通过重定向 logger 改变去向。
	r.logger.Error("tinyagent: recovered panic from component",
		slog.String("component", component),
		slog.Any("value", v),
		slog.String("stack", string(stack)),
	)

	// 通道 2：上报回调 —— 用于事件推送与审计写入。
	if r.report != nil {
		r.report(info)
	}

	// 通道 3：error 返回。
	if r.policy == PanicPropagate {
		panic(v)
	}

	return &Error{
		Kind:      ErrKindPanic,
		Component: component,
		Op:        "recover",
		Err:       fmt.Errorf("panic: %v", v),
		Stack:     stack,
	}
}

// AsError 把 err 断言为库的 *Error。
func AsError(err error) (*Error, bool) {
	var e *Error
	if errors.As(err, &e) {
		return e, true
	}
	return nil, false
}

// IsPanic 报告 err 是否源于一次被捕获的 panic。
func IsPanic(err error) bool {
	e, ok := AsError(err)
	return ok && e.Kind == ErrKindPanic
}
