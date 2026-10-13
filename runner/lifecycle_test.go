package runner

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pcc-258/tinyagent/audit"
	"github.com/pcc-258/tinyagent/core"
	"github.com/pcc-258/tinyagent/ctxmgr"
	"github.com/pcc-258/tinyagent/hook"
	"github.com/pcc-258/tinyagent/model"
	"github.com/pcc-258/tinyagent/store"
	"github.com/pcc-258/tinyagent/tool"
)

func assertPaired(t *testing.T, msgs []core.Message) {
	t.Helper()
	repaired, n := core.RepairToolCalls(msgs, "missing")
	if n != 0 {
		t.Fatalf("history has %d missing results: %+v", n, repaired)
	}
}

func TestSequentialExecutionOrder(t *testing.T) {
	var order []string
	tl := mustNewTool(t, "echo", "echo", func(_ context.Context, a delayArgs) (core.ToolResult, error) {
		order = append(order, a.Tag)
		return core.ToolResult{Content: a.Tag}, nil
	})
	calls := make([]core.ToolCall, 20)
	for i := range calls {
		calls[i] = delayCall(fmt.Sprint(i), fmt.Sprint(i), 0)
		calls[i].Name = "echo"
	}
	mdl := &scriptedModel{responses: []model.Response{{Message: core.Message{Role: core.RoleAssistant, ToolCalls: calls}}}}
	r := mustRunner(t, RunnerOptions{Model: mdl, Tools: []tool.Tool{tl}, SequentialTools: true})
	if _, err := collect(r.Run(context.Background(), store.NewSession("s"), "go")); err != nil {
		t.Fatal(err)
	}
	for i, got := range order {
		if got != fmt.Sprint(i) {
			t.Fatalf("execution order=%v", order)
		}
	}
	if len(order) != len(calls) {
		t.Fatalf("executed %d calls", len(order))
	}
}

func TestToolConcurrencyBound(t *testing.T) {
	var active, peak atomic.Int32
	entered := make(chan struct{}, 20)
	release := make(chan struct{})
	tl := mustNewTool(t, "echo", "echo", func(ctx context.Context, a delayArgs) (core.ToolResult, error) {
		n := active.Add(1)
		defer active.Add(-1)
		for p := peak.Load(); n > p; p = peak.Load() {
			if peak.CompareAndSwap(p, n) {
				break
			}
		}
		entered <- struct{}{}
		select {
		case <-release:
		case <-ctx.Done():
			return core.ToolResult{}, ctx.Err()
		}
		return core.ToolResult{Content: a.Tag}, nil
	})
	calls := make([]core.ToolCall, 12)
	for i := range calls {
		calls[i] = delayCall(fmt.Sprint(i), "ok", 0)
		calls[i].Name = "echo"
	}
	mdl := &scriptedModel{responses: []model.Response{{Message: core.Message{Role: core.RoleAssistant, ToolCalls: calls}}}}
	r := mustRunner(t, RunnerOptions{Model: mdl, Tools: []tool.Tool{tl}, ToolConcurrency: 2})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := collect(r.Run(ctx, store.NewSession("s"), "go")); done <- err }()
	for range 2 {
		select {
		case <-entered:
		case <-ctx.Done():
			close(release)
			t.Fatal(ctx.Err())
		}
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if peak.Load() != 2 {
		t.Fatalf("peak concurrency=%d, want 2", peak.Load())
	}
}

func TestConsumerBreakPreservesToolHistory(t *testing.T) {
	for _, stopAt := range []core.EventType{core.EventToolCall, core.EventToolResult} {
		t.Run(string(stopAt), func(t *testing.T) {
			executed := 0
			tl := mustNewTool(t, "echo", "echo", func(context.Context, delayArgs) (core.ToolResult, error) {
				executed++
				return core.ToolResult{Content: "actual result"}, nil
			})
			c1, c2 := delayCall("c1", "a", 0), delayCall("c2", "b", 0)
			c1.Name = "echo"
			c2.Name = "echo"
			mdl := &scriptedModel{responses: []model.Response{{Message: core.Message{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{c1, c2}}}}}
			r := mustRunner(t, RunnerOptions{Model: mdl, Tools: []tool.Tool{tl}, SequentialTools: true})
			s := store.NewSession("s")
			for ev, err := range r.Run(context.Background(), s, "go") {
				if err != nil {
					t.Fatal(err)
				}
				if ev.Type == stopAt {
					break
				}
			}
			msgs := s.Messages()
			assertPaired(t, msgs)
			if len(msgs) != 4 {
				t.Fatalf("messages=%+v", msgs)
			}
			if stopAt == core.EventToolCall {
				if executed != 0 || !strings.Contains(msgs[2].Content, "not executed") {
					t.Fatalf("executed=%d history=%+v", executed, msgs)
				}
			} else {
				if executed != 2 || msgs[2].Content != "actual result" || msgs[3].Content != "actual result" {
					t.Fatalf("lost executed work: %+v", msgs)
				}
			}
		})
	}
}

func TestHookPanicPoliciesAndAudit(t *testing.T) {
	for _, stage := range []string{"before_model", "after_model", "before_tool", "after_tool"} {
		for _, policy := range []core.PanicPolicy{core.PanicRecoverAsToolError, core.PanicFailRun} {
			t.Run(fmt.Sprintf("%s/%d", stage, policy), func(t *testing.T) {
				h := hook.HookFuncs{}
				switch stage {
				case "before_model":
					h.OnBeforeModelCall = func(context.Context, *model.Request) error { panic("boom") }
				case "after_model":
					h.OnAfterModelCall = func(context.Context, *model.Response) error { panic("boom") }
				case "before_tool":
					h.OnBeforeToolCall = func(context.Context, *core.ToolCall) error { panic("boom") }
				case "after_tool":
					h.OnAfterToolCall = func(context.Context, *core.ToolCall, *core.ToolResult) error { panic("boom") }
				}
				tl := mustNewTool(t, "echo", "echo", func(context.Context, delayArgs) (core.ToolResult, error) { return core.ToolResult{Content: "ok"}, nil })
				c := delayCall("c", "a", 0)
				c.Name = "echo"
				mdl := &scriptedModel{responses: []model.Response{{Message: core.Message{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{c}}}}}
				ad := audit.NewMemoryAudit()
				s := store.NewSession("s")
				r := mustRunner(t, RunnerOptions{Model: mdl, Tools: []tool.Tool{tl}, Hooks: []hook.Hook{h}, Audit: ad, PanicPolicy: policy})
				events, err := collect(r.Run(context.Background(), s, "go"))
				if policy == core.PanicFailRun && err == nil {
					t.Fatal("expected run failure")
				}
				if policy == core.PanicRecoverAsToolError && err != nil {
					t.Fatal(err)
				}
				if countType(events, core.EventPanic) == 0 {
					t.Fatal("missing panic event")
				}
				records, qerr := ad.Query(context.Background(), audit.AuditQuery{Kind: audit.AuditPanic})
				if qerr != nil || len(records) == 0 {
					t.Fatalf("panic audit=%+v err=%v", records, qerr)
				}
				assertPaired(t, s.Messages())
			})
		}
	}
}

func TestStopConditionsCommitStep(t *testing.T) {
	for name, condition := range map[string]StopCondition{"steps": StepCountIs(1), "tokens": MaxTokensUsed(5), "tool": HasToolCall("echo")} {
		t.Run(name, func(t *testing.T) {
			c := delayCall("c", "a", 0)
			c.Name = "echo"
			mdl := &scriptedModel{responses: []model.Response{{Message: core.Message{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{c}}, Usage: core.Usage{TotalTokens: 5}}}}
			tl := mustNewTool(t, "echo", "echo", func(context.Context, delayArgs) (core.ToolResult, error) {
				return core.ToolResult{Content: "saved"}, nil
			})
			r := mustRunner(t, RunnerOptions{Model: mdl, Tools: []tool.Tool{tl}, StopConditions: []StopCondition{condition}})
			s := store.NewSession("s")
			events, err := collect(r.Run(context.Background(), s, "go"))
			if err != nil {
				t.Fatal(err)
			}
			if mdl.calls != 1 || countType(events, core.EventToolResult) != 1 {
				t.Fatalf("calls=%d events=%v", mdl.calls, events)
			}
			assertPaired(t, s.Messages())
		})
	}
}

func TestCancelSafePoints(t *testing.T) {
	for _, mode := range []CancelMode{CancelImmediate, CancelAfterModel, CancelAfterTools} {
		t.Run(fmt.Sprint(mode), func(t *testing.T) {
			ctx, cancel := WithCancel(context.Background())
			executed := 0
			h := hook.HookFuncs{OnBeforeModelCall: func(context.Context, *model.Request) error {
				if !cancel(mode, time.Second) {
					t.Error("cancel rejected")
				}
				return nil
			}}
			tl := mustNewTool(t, "echo", "echo", func(context.Context, delayArgs) (core.ToolResult, error) {
				executed++
				return core.ToolResult{Content: "saved"}, nil
			})
			c := delayCall("c", "a", 0)
			c.Name = "echo"
			mdl := &scriptedModel{responses: []model.Response{{Message: core.Message{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{c}}}}}
			r := mustRunner(t, RunnerOptions{Model: mdl, Tools: []tool.Tool{tl}, Hooks: []hook.Hook{h}})
			s := store.NewSession("s")
			_, err := collect(r.Run(ctx, s, "go"))
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("err=%v", err)
			}
			want := 0
			if mode == CancelAfterTools {
				want = 1
			}
			if executed != want {
				t.Fatalf("executed=%d want=%d", executed, want)
			}
			assertPaired(t, s.Messages())
			if cancel(CancelImmediate, 0) {
				t.Fatal("finished control accepted cancellation")
			}
		})
	}
}

func TestSafeCancelEscalatesAndDoesNotStartQueuedTools(t *testing.T) {
	ctx, cancel := WithCancel(context.Background())
	executed := 0
	tl := mustNewTool(t, "echo", "echo", func(ctx context.Context, _ delayArgs) (core.ToolResult, error) {
		executed++
		cancel(CancelAfterTools, 20*time.Millisecond)
		<-ctx.Done()
		return core.ToolResult{}, ctx.Err()
	})
	c1, c2 := delayCall("c1", "a", 0), delayCall("c2", "b", 0)
	c1.Name = "echo"
	c2.Name = "echo"
	mdl := &scriptedModel{responses: []model.Response{{Message: core.Message{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{c1, c2}}}}}
	r := mustRunner(t, RunnerOptions{Model: mdl, Tools: []tool.Tool{tl}, SequentialTools: true})
	s := store.NewSession("s")
	_, err := collect(r.Run(ctx, s, "go"))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
	if executed != 1 {
		t.Fatalf("queued tool started after cancellation: %d", executed)
	}
	assertPaired(t, s.Messages())
}

func TestAfterModelHookEditsAreCommitted(t *testing.T) {
	h := hook.HookFuncs{OnAfterModelCall: func(_ context.Context, r *model.Response) error { r.Message.Content = "edited"; return nil }}
	r := mustRunner(t, RunnerOptions{Model: &scriptedModel{}, Hooks: []hook.Hook{h}})
	s := store.NewSession("s")
	if _, err := collect(r.Run(context.Background(), s, "go")); err != nil {
		t.Fatal(err)
	}
	if s.Messages()[1].Content != "edited" {
		t.Fatal("hook edit missing from history")
	}
}

type streamFixture struct{ seq iter.Seq2[model.Chunk, error] }

func (streamFixture) Generate(context.Context, model.Request) (model.Response, error) {
	return model.Response{}, errors.New("unused")
}
func (m streamFixture) Stream(context.Context, model.Request) iter.Seq2[model.Chunk, error] {
	return m.seq
}

func TestStreamingConsumerPanicPropagatesAndCleansUp(t *testing.T) {
	cleaned := make(chan struct{})
	mdl := streamFixture{seq: func(yield func(model.Chunk, error) bool) {
		defer close(cleaned)
		yield(model.Chunk{TextDelta: "text"}, nil)
	}}
	r := mustRunner(t, RunnerOptions{Model: mdl})
	s := store.NewSession("s")
	func() {
		defer func() {
			if p := recover(); p != "consumer panic" {
				t.Errorf("panic=%v", p)
			}
		}()
		for ev, err := range r.Run(context.Background(), s, "go") {
			if err != nil {
				t.Fatal(err)
			}
			if ev.Type == core.EventText {
				panic("consumer panic")
			}
		}
	}()
	select {
	case <-cleaned:
	default:
		t.Fatal("provider iterator not cleaned up")
	}
	if !s.TryAcquire() {
		t.Fatal("session lease stranded after consumer panic")
	}
	s.Release()
}

func TestStreamingProviderPanicIsReported(t *testing.T) {
	mdl := streamFixture{seq: func(yield func(model.Chunk, error) bool) {
		yield(model.Chunk{TextDelta: "text"}, nil)
		panic("provider panic")
	}}
	ad := audit.NewMemoryAudit()
	r := mustRunner(t, RunnerOptions{Model: mdl, Audit: ad})
	events, err := collect(r.Run(context.Background(), store.NewSession("s"), "go"))
	if !core.IsPanic(err) || countType(events, core.EventPanic) != 1 {
		t.Fatalf("panic lost: %v %+v", err, events)
	}
	records, _ := ad.Query(context.Background(), audit.AuditQuery{Kind: audit.AuditPanic})
	if len(records) != 1 {
		t.Fatalf("audit=%+v", records)
	}
}

func TestTruncatedStreamDoesNotDispatchTools(t *testing.T) {
	executed := false
	tl := mustNewTool(t, "echo", "echo", func(context.Context, delayArgs) (core.ToolResult, error) {
		executed = true
		return core.ToolResult{}, nil
	})
	mdl := streamFixture{seq: func(yield func(model.Chunk, error) bool) {
		yield(model.Chunk{ToolCall: &model.ToolCallDelta{Index: 0, ID: "c", Name: "echo", ArgumentsDelta: `{"tag":"partial`}}, nil)
		yield(model.Chunk{FinishReason: "length"}, nil)
	}}
	r := mustRunner(t, RunnerOptions{Model: mdl, Tools: []tool.Tool{tl}})
	_, err := collect(r.Run(context.Background(), store.NewSession("s"), "go"))
	if err == nil || executed {
		t.Fatalf("err=%v executed=%v", err, executed)
	}
}

func TestHookCannotExpandPastReservedContextBudget(t *testing.T) {
	mdl := &capturingRequestModel{}
	h := hook.HookFuncs{OnBeforeModelCall: func(_ context.Context, req *model.Request) error {
		req.Messages = append(req.Messages, core.Message{Role: core.RoleUser, Content: strings.Repeat("x", 4000)})
		return nil
	}}
	counter := ctxmgr.NewSimpleCounter()
	r := mustRunner(t, RunnerOptions{Model: mdl, Counter: counter, Context: ctxmgr.NewDefaultContextManager(counter), ContextBudget: 500, OutputReserve: 100, Hooks: []hook.Hook{h}})
	_, err := collect(r.Run(context.Background(), store.NewSession("s"), "go"))
	if !errors.Is(err, core.ErrContextBudget) || mdl.req != nil {
		t.Fatalf("oversized request reached model: %v", err)
	}
}

func TestSafeCancelBeforeRunDoesNotStartModel(t *testing.T) {
	ctx, cancel := WithCancel(context.Background())
	cancel(CancelAfterTools, time.Second)
	mdl := &capturingRequestModel{}
	r := mustRunner(t, RunnerOptions{Model: mdl})
	_, err := collect(r.Run(ctx, store.NewSession("s"), "go"))
	if !errors.Is(err, context.Canceled) || mdl.req != nil {
		t.Fatalf("err=%v request=%+v", err, mdl.req)
	}
}
