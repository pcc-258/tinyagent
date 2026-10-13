package runner

import (
	"context"
	"sync"
	"time"
)

type CancelMode int

const (
	CancelImmediate CancelMode = iota
	CancelAfterModel
	CancelAfterTools
)

type cancelKey struct{}
type cancelControl struct {
	mu        sync.Mutex
	cancel    context.CancelFunc
	requested bool
	mode      CancelMode
	timer     *time.Timer
	finished  bool
}

// WithCancel creates a context for one run and a thread-safe cancellation
// function. Safe-point cancellation waits for the current component/batch;
// timeout <=0 defaults to 5 seconds before escalating to immediate cancellation.
// Parent cancellation and deadlines always cancel immediately. Components must
// honor ctx; Go cannot forcibly stop a callback that ignores cancellation.
// The context is closed when the default runner finishes. Call cancel with
// CancelImmediate if the iterator will never be consumed.
func WithCancel(parent context.Context) (context.Context, func(CancelMode, time.Duration) bool) {
	ctx, stop := context.WithCancel(parent)
	c := &cancelControl{cancel: stop}
	ctx = context.WithValue(ctx, cancelKey{}, c)
	return ctx, func(mode CancelMode, timeout time.Duration) bool {
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.finished || ctx.Err() != nil {
			return false
		}
		if mode != CancelImmediate && mode != CancelAfterModel && mode != CancelAfterTools {
			return false
		}
		if c.requested && mode != CancelImmediate {
			return false
		}
		c.requested, c.mode = true, mode
		if mode == CancelImmediate {
			c.cancel()
			return true
		}
		if timeout <= 0 {
			timeout = 5 * time.Second
		}
		c.timer = time.AfterFunc(timeout, c.cancel)
		return true
	}
}

func cancelAt(ctx context.Context, point CancelMode) bool {
	c, _ := ctx.Value(cancelKey{}).(*cancelControl)
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.requested && (c.mode == point || c.mode == CancelImmediate) {
		c.cancel()
		return true
	}
	return false
}

func finishCancel(ctx context.Context) {
	c, _ := ctx.Value(cancelKey{}).(*cancelControl)
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.finished = true
	if c.timer != nil {
		c.timer.Stop()
	}
	c.cancel()
}
