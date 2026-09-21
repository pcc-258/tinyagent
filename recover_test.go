package tinyagent

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"
)

func newTestRecoverer(policy PanicPolicy, report func(*PanicInfo)) (*recoverer, *bytes.Buffer) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	return newRecoverer(policy, logger, report), &buf
}

// TestGuard_RecoversPanic 验证 panic 被捕获，且三通道上报全部产生。
func TestGuard_RecoversPanic(t *testing.T) {
	var reported *PanicInfo
	r, buf := newTestRecoverer(PanicRecoverAsToolError, func(pi *PanicInfo) { reported = pi })

	err := r.guard("tool:boom", func() error {
		panic("kaboom")
	})

	// 通道 3：error 返回
	if err == nil {
		t.Fatal("expected error from panic, got nil")
	}
	e, ok := AsError(err)
	if !ok {
		t.Fatalf("expected *Error, got %T", err)
	}
	if e.Kind != ErrKindPanic {
		t.Errorf("Kind = %q, want %q", e.Kind, ErrKindPanic)
	}
	if e.Component != "tool:boom" {
		t.Errorf("Component = %q, want %q", e.Component, "tool:boom")
	}
	if !strings.Contains(err.Error(), "kaboom") {
		t.Errorf("error %q should mention panic value", err.Error())
	}
	if len(e.Stack) == 0 {
		t.Error("expected non-empty stack")
	}
	if !IsPanic(err) {
		t.Error("IsPanic should report true")
	}

	// 通道 2：上报回调
	if reported == nil {
		t.Fatal("report callback not invoked")
	}
	if reported.Component != "tool:boom" {
		t.Errorf("reported.Component = %q", reported.Component)
	}
	if len(reported.Stack) == 0 {
		t.Error("reported stack empty")
	}

	// 通道 1：结构化日志
	if !strings.Contains(buf.String(), "recovered panic") {
		t.Errorf("log output missing, got %q", buf.String())
	}
	if !strings.Contains(buf.String(), "tool:boom") {
		t.Errorf("log output missing component, got %q", buf.String())
	}
}

// TestGuard_NoPanic 验证正常路径不受影响。
func TestGuard_NoPanic(t *testing.T) {
	r, _ := newTestRecoverer(PanicRecoverAsToolError, nil)
	if err := r.guard("tool:ok", func() error { return nil }); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestGuard_NormalError 验证普通错误原样透传，不被当成 panic。
func TestGuard_NormalError(t *testing.T) {
	r, _ := newTestRecoverer(PanicRecoverAsToolError, nil)
	sentinel := errors.New("normal failure")
	err := r.guard("tool:ok", func() error { return sentinel })
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected sentinel error, got %v", err)
	}
	if IsPanic(err) {
		t.Error("normal error must not be reported as panic")
	}
}

// TestGuard_Propagate 验证 PanicPropagate 策略下 panic 向外传播，
// 且日志通道仍然产生输出。
func TestGuard_Propagate(t *testing.T) {
	r, buf := newTestRecoverer(PanicPropagate, nil)

	defer func() {
		got := recover()
		if got == nil {
			t.Fatal("expected panic to propagate")
		}
		if got != "propagate me" {
			t.Errorf("propagated value = %v, want %q", got, "propagate me")
		}
		if !strings.Contains(buf.String(), "recovered panic") {
			t.Error("log must be emitted even when propagating")
		}
	}()

	_ = r.guard("tool:x", func() error { panic("propagate me") })
}

// TestGuardValue 验证带返回值的 guard。
func TestGuardValue(t *testing.T) {
	r, _ := newTestRecoverer(PanicRecoverAsToolError, nil)

	v, err := guardValue(r, "tool:v", func() (int, error) { return 42, nil })
	if err != nil || v != 42 {
		t.Fatalf("got (%d, %v), want (42, nil)", v, err)
	}

	v, err = guardValue(r, "tool:v", func() (int, error) { panic("boom") })
	if err == nil {
		t.Fatal("expected error from panic")
	}
	if v != 0 {
		t.Errorf("value should be zero on panic, got %d", v)
	}
	if !IsPanic(err) {
		t.Errorf("expected panic error, got %v", err)
	}
}

// TestGuard_NonStringPanic 验证非字符串 panic 值也能被捕获并可见。
func TestGuard_NonStringPanic(t *testing.T) {
	r, _ := newTestRecoverer(PanicRecoverAsToolError, nil)
	err := r.guard("tool:x", func() error { panic(42) })
	if err == nil || !strings.Contains(err.Error(), "42") {
		t.Fatalf("expected error mentioning 42, got %v", err)
	}
}

// TestGuard_ReportNilSafe 验证 report 为 nil 时不 panic。
func TestGuard_ReportNilSafe(t *testing.T) {
	r, _ := newTestRecoverer(PanicRecoverAsToolError, nil)
	if err := r.guard("tool:x", func() error { panic("no report cb") }); err == nil {
		t.Fatal("expected error")
	}
}

// TestNewRecoverer_NilLogger 验证 logger 为 nil 时回退到默认 logger。
func TestNewRecoverer_NilLogger(t *testing.T) {
	r := newRecoverer(PanicRecoverAsToolError, nil, nil)
	if r.logger == nil {
		t.Fatal("logger should fall back to slog.Default()")
	}
}
