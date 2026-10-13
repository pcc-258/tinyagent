package runner

import (
	"context"
	"errors"
	"fmt"
	"github.com/pcc-258/tinyagent/audit"
	"github.com/pcc-258/tinyagent/core"
	"github.com/pcc-258/tinyagent/ctxmgr"
	"github.com/pcc-258/tinyagent/hook"
	"github.com/pcc-258/tinyagent/model"
	"github.com/pcc-258/tinyagent/store"
	"github.com/pcc-258/tinyagent/tool"
	"iter"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var (
	runSeq          atomic.Uint64
	errYieldStopped = errors.New("tinyagent: yield stopped")
)

func newRunID() string {
	return fmt.Sprintf("run-%d-%d", time.Now().UnixNano(), runSeq.Add(1))
}

// ReActRunner 是默认的 Runner 实现。
//
// 驱动「模型调用 → 工具执行 → 结果回填」的迭代，直到模型不再请求工具、
// 触达 MaxIterations、或 context 取消。事件产出全部发生在调用 Run 的 goroutine 中。
type ReActRunner struct {
	opts    RunnerOptions
	guard   *core.Recoverer
	hooks   *hook.Chain
	tools   *tool.ToolRegistry
	logger  *slog.Logger
	maxIter int
}

// NewReActRunner 构造默认 Runner。
func NewReActRunner(opts RunnerOptions) (*ReActRunner, error) {
	if opts.Model == nil {
		return nil, core.NewError(core.ErrKindConfig, "runner", "new", errors.New("Model is required"))
	}
	if opts.OutputReserve < 0 {
		return nil, core.NewError(core.ErrKindConfig, "runner", "new", errors.New("OutputReserve must be non-negative"))
	}
	opts.StopConditions = append([]StopCondition(nil), opts.StopConditions...)

	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}

	reg := tool.NewToolRegistry()
	for _, t := range opts.Tools {
		if t == nil {
			continue
		}
		if err := reg.Register(t); err != nil {
			return nil, core.NewError(core.ErrKindConfig, "runner", "register tool", err)
		}
	}

	maxIter := opts.MaxIterations
	if maxIter <= 0 {
		maxIter = 16
	}

	return &ReActRunner{
		opts:    opts,
		guard:   core.NewRecoverer(opts.PanicPolicy, logger, nil),
		hooks:   hook.NewChain(opts.Hooks),
		tools:   reg,
		logger:  logger,
		maxIter: maxIter,
	}, nil
}

// RegisterTool 向运行器动态注册一个工具。
//
// 这是 Agent 门面动态注册工具的入口；自定义 Runner 不提供此能力。
func (r *ReActRunner) RegisterTool(t tool.Tool) error {
	return r.tools.Register(t)
}

// Run 实现 Runner。
func (r *ReActRunner) Run(ctx context.Context, s *store.Session, input string) iter.Seq2[core.Event, error] {
	return func(yield func(core.Event, error) bool) {
		defer finishCancel(ctx)
		r.run(ctx, s, input, yield)
	}
}

func (r *ReActRunner) run(ctx context.Context, s *store.Session, input string, yield func(core.Event, error) bool) {
	if s == nil {
		yield(core.Event{}, core.NewError(core.ErrKindRunner, "runner", "run", errors.New("nil session")))
		return
	}
	if !s.TryAcquire() {
		yield(core.Event{}, core.ErrSessionBusy)
		return
	}
	defer s.Release()
	defer repairHistory(s)
	repairHistory(s)

	if r.opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, r.opts.Timeout)
		defer cancel()
	}

	runID := newRunID()
	startedAt := time.Now()
	r.recordAudit(ctx, s.ID, runID, audit.AuditRunStart, "runner", nil, 0)
	defer func() {
		r.recordAudit(ctx, s.ID, runID, audit.AuditRunEnd, "runner", nil, time.Since(startedAt))
	}()

	if !yield(core.Event{Type: core.EventRunStart, Time: time.Now()}, nil) {
		return
	}
	if input != "" {
		s.Append(core.Message{Role: core.RoleUser, Content: input})
	}

	var total core.Usage
	output := yield
	yield = func(ev core.Event, err error) bool {
		if ev.Type == core.EventRunEnd && ev.Usage == nil {
			usage := total
			ev.Usage = &usage
		}
		return output(ev, err)
	}
	for i := 0; i < r.maxIter; i++ {
		// Between steps both safe modes can stop without starting more work.
		cancelAt(ctx, CancelAfterModel)
		cancelAt(ctx, CancelAfterTools)
		if err := ctx.Err(); err != nil {
			yield(core.Event{Type: core.EventRunEnd, Usage: &total, Err: err, Time: time.Now()}, err)
			return
		}

		finalizing := budgetFinalizing(ctx, startedAt, i, r.maxIter)
		resp, ok := r.modelTurn(ctx, s, runID, &total, startedAt, i, finalizing, yield)
		if !ok || resp == nil {
			return
		}
		if cancelAt(ctx, CancelAfterModel) || ctx.Err() != nil {
			if len(resp.Message.ToolCalls) > 0 {
				appendMissingResults(s, resp.Message.ToolCalls, "not executed: run canceled before tool dispatch")
			}
			yield(core.Event{Type: core.EventRunEnd, Usage: &total, Err: ctx.Err(), Time: time.Now()}, ctx.Err())
			return
		}

		if len(resp.Message.ToolCalls) == 0 {
			if cancelAt(ctx, CancelAfterTools) {
				yield(core.Event{Type: core.EventRunEnd, Usage: &total, Err: ctx.Err(), Time: time.Now()}, ctx.Err())
				return
			}
			yield(core.Event{Type: core.EventRunEnd, Usage: &total, Time: time.Now()}, nil)
			return
		}
		if finalizing {
			appendMissingResults(s, resp.Message.ToolCalls, "not executed: tool calls disabled by run budget")
			yield(core.Event{Type: core.EventRunEnd, Usage: &total, Err: core.ErrMaxIterations, Time: time.Now()}, core.ErrMaxIterations)
			return
		}

		if !r.toolTurn(ctx, s, runID, resp, yield) {
			return
		}
		if cancelAt(ctx, CancelAfterTools) || ctx.Err() != nil {
			yield(core.Event{Type: core.EventRunEnd, Usage: &total, Err: ctx.Err(), Time: time.Now()}, ctx.Err())
			return
		}
		step := StepResult{Iteration: i + 1, Elapsed: time.Since(startedAt), Usage: total, Message: resp.Message}
		for _, condition := range r.opts.StopConditions {
			if condition == nil {
				continue
			}
			stop, err := core.GuardValue(r.guard, "stop condition", func() (bool, error) {
				return condition(StepResult{Iteration: step.Iteration, Elapsed: step.Elapsed, Usage: step.Usage, Message: core.CloneMessage(step.Message)}), nil
			})
			if err != nil {
				if r.reportPanic(ctx, s, runID, yield, err) {
					r.fail(yield, err)
				}
				return
			}
			if stop {
				yield(core.Event{Type: core.EventRunEnd, Usage: &total, Time: time.Now()}, nil)
				return
			}
		}
	}

	yield(core.Event{Type: core.EventRunEnd, Usage: &total, Err: core.ErrMaxIterations, Time: time.Now()}, core.ErrMaxIterations)
}

// modelTurn 执行一轮模型调用：上下文处理 → Hook → 调用模型 → 回填。
//
// 返回 ok=false 表示事件消费方已中断或发生错误，调用方应立即返回。
func (r *ReActRunner) modelTurn(
	ctx context.Context,
	s *store.Session,
	runID string,
	total *core.Usage,
	runStartedAt time.Time,
	iteration int,
	finalizing bool,
	yield func(core.Event, error) bool,
) (*model.Response, bool) {
	msgs := s.Messages()
	hooks := r.guardedHooks(ctx, s, runID, yield)
	tools, infoErr := core.GuardValue(r.guard, "tool definitions", func() ([]tool.ToolInfo, error) { return r.tools.Infos(), nil })
	if infoErr != nil {
		if r.reportPanic(ctx, s, runID, yield, infoErr) {
			r.fail(yield, infoErr)
		}
		return nil, false
	}
	if finalizing {
		tools = nil
	}

	if r.opts.Context != nil && r.opts.ContextBudget > 0 {
		inputBudget := r.opts.ContextBudget - r.opts.OutputReserve
		if inputBudget <= 0 {
			return nil, r.fail(yield, core.ErrContextBudget)
		}
		system := buildSystemPrompt(r.opts.System, ctx, runStartedAt, iteration, r.maxIter, total, finalizing)
		type preparedContext struct {
			messages []core.Message
			actions  []core.ContextAction
		}
		prepared, err := core.GuardValue(r.guard, "context", func() (preparedContext, error) {
			processed, actions, err := r.opts.Context.Prepare(ctx, msgs, tools, system, inputBudget)
			return preparedContext{processed, actions}, err
		})
		processed, actions := prepared.messages, prepared.actions
		if err != nil {
			if !r.reportPanic(ctx, s, runID, yield, err) {
				return nil, false
			}
			return nil, r.fail(yield, core.NewError(core.ErrKindContext, "context", "prepare", err))
		}
		s.Replace(processed)
		msgs = processed
		for i := range actions {
			if !yield(core.Event{Type: core.EventContextAction, ContextAction: &actions[i], Time: time.Now()}, nil) {
				return nil, false
			}
			r.recordAudit(ctx, s.ID, runID, audit.AuditContextAction, "context", actions[i].Detail, 0)
		}
	}

	req := &model.Request{
		Messages: msgs,
		System:   buildSystemPrompt(r.opts.System, ctx, runStartedAt, iteration, r.maxIter, total, finalizing),
		Tools:    tools,
	}
	if r.opts.OutputReserve > 0 {
		reserve := r.opts.OutputReserve
		req.MaxTokens = &reserve
	}

	if err := hooks.BeforeModelCall(ctx, req); err != nil {
		if errors.Is(err, errYieldStopped) {
			return nil, false
		}
		return nil, r.fail(yield, err)
	}
	if r.opts.Counter != nil && r.opts.ContextBudget > 0 {
		reserve := r.opts.OutputReserve
		if req.MaxTokens != nil {
			reserve = max(reserve, *req.MaxTokens)
		}
		count, err := core.GuardValue(r.guard, "counter", func() (ctxmgr.CountResult, error) {
			return r.opts.Counter.Count(ctx, ctxmgr.CountRequest{Messages: req.Messages, Tools: req.Tools, System: req.System})
		})
		if err != nil {
			if r.reportPanic(ctx, s, runID, yield, err) {
				r.fail(yield, err)
			}
			return nil, false
		}
		if count.Total > r.opts.ContextBudget-reserve {
			return nil, r.fail(yield, fmt.Errorf("%w: final request uses %d tokens", core.ErrContextBudget, count.Total))
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, r.fail(yield, err)
	}

	startedAt := time.Now()
	resp, streamed, err := r.callModel(ctx, req, yield)
	if err != nil {
		if !r.reportPanic(ctx, s, runID, yield, err) {
			return nil, false
		}
		return nil, r.fail(yield, err)
	}
	if resp == nil {
		return nil, false
	}
	if resp.FinishReason == "length" {
		total.Add(resp.Usage)
		s.Append(resp.Message)
		appendMissingResults(s, resp.Message.ToolCalls, "not executed: model output was truncated")
		return nil, r.fail(yield, core.NewError(core.ErrKindModel, "model", "Generate", errors.New("model output truncated with finish_reason=length")))
	}

	total.Add(resp.Usage)
	originalMessage := core.CloneMessage(resp.Message)
	if err := hooks.AfterModelCall(ctx, resp); err != nil {
		s.Append(originalMessage)
		appendMissingResults(s, originalMessage.ToolCalls, "not executed: AfterModelCall interrupted before tool dispatch")
		if errors.Is(err, errYieldStopped) {
			return nil, false
		}
		return nil, r.fail(yield, err)
	}
	s.Append(resp.Message)

	if !streamed && resp.Message.Content != "" {
		if !yield(core.Event{Type: core.EventText, Text: resp.Message.Content, Time: time.Now()}, nil) {
			return nil, false
		}
	}

	r.recordAudit(ctx, s.ID, runID, audit.AuditModelCall, "model", map[string]any{
		"finish_reason":     resp.FinishReason,
		"prompt_tokens":     resp.Usage.PromptTokens,
		"completion_tokens": resp.Usage.CompletionTokens,
	}, time.Since(startedAt))

	return resp, true
}

// callModel 优先使用流式；模型实现未提供流式时退回非流式。
//
// 第二个返回值报告文本是否已由流式路径实时产出，
// 避免非流式补发文本时与流式增量重复。
func (r *ReActRunner) callModel(
	ctx context.Context,
	req *model.Request,
	yield func(core.Event, error) bool,
) (*model.Response, bool, error) {
	if sm, ok := r.opts.Model.(model.StreamingModel); ok {
		resp, err := r.streamModel(ctx, sm, req, yield)
		return resp, true, err
	}

	resp, err := core.GuardValue(r.guard, "model", func() (model.Response, error) {
		return r.opts.Model.Generate(ctx, *req)
	})
	if err != nil {
		return nil, false, core.NewError(core.ErrKindModel, "model", "Generate", err)
	}
	return &resp, false, nil
}

// streamModel 消费流式响应：文本增量实时产出，工具调用分片按 Index 聚合。
func (r *ReActRunner) streamModel(
	ctx context.Context,
	sm model.StreamingModel,
	req *model.Request,
	yield func(core.Event, error) bool,
) (*model.Response, error) {
	acc := model.NewToolCallAccumulator()
	var sb strings.Builder
	var usage core.Usage
	var finish string

	type pullStream struct {
		next func() (model.Chunk, error, bool)
		stop func()
	}
	pull, err := core.GuardValue(r.guard, "model", func() (pullStream, error) {
		next, stop := iter.Pull2(sm.Stream(ctx, *req))
		return pullStream{next, stop}, nil
	})
	if err != nil {
		return nil, core.NewError(core.ErrKindModel, "model", "Stream", err)
	}
	defer func() {
		// Consumer callbacks are outside recovery. Only provider iteration and
		// teardown are component callbacks covered by the model panic policy.
		_, _ = core.GuardValue(r.guard, "model", func() (struct{}, error) { pull.stop(); return struct{}{}, nil })
	}()
	type streamItem struct {
		chunk model.Chunk
		err   error
		ok    bool
	}
	for {
		item, err := core.GuardValue(r.guard, "model", func() (streamItem, error) {
			chunk, err, ok := pull.next()
			return streamItem{chunk, err, ok}, nil
		})
		if err != nil {
			return nil, core.NewError(core.ErrKindModel, "model", "Stream", err)
		}
		if !item.ok {
			break
		}
		if item.err != nil {
			return nil, core.NewError(core.ErrKindModel, "model", "Stream", item.err)
		}
		chunk := item.chunk
		if chunk.TextDelta != "" {
			sb.WriteString(chunk.TextDelta)
			if !yield(core.Event{Type: core.EventText, Text: chunk.TextDelta, Time: time.Now()}, nil) {
				return nil, nil
			}
		}
		if chunk.ToolCall != nil {
			acc.Add(chunk.ToolCall)
		}
		if chunk.Usage != nil {
			usage = *chunk.Usage
		}
		if chunk.FinishReason != "" {
			finish = chunk.FinishReason
		}
	}

	if finish == "length" {
		return nil, core.NewError(core.ErrKindModel, "model", "Stream",
			errors.New("model output truncated with finish_reason=length"))
	}

	return &model.Response{
		Message: core.Message{
			Role:      core.RoleAssistant,
			Content:   sb.String(),
			ToolCalls: acc.Calls(),
		},
		Usage:        usage,
		FinishReason: finish,
	}, nil
}

func (r *ReActRunner) fail(yield func(core.Event, error) bool, err error) bool {
	yield(core.Event{Type: core.EventRunEnd, Err: err, Time: time.Now()}, err)
	return false
}

func (r *ReActRunner) reportPanic(ctx context.Context, s *store.Session, runID string, yield func(core.Event, error) bool, err error) bool {
	if pi, ok := panicInfoOf(err); ok {
		r.recordAudit(ctx, s.ID, runID, audit.AuditPanic, pi.Component, map[string]any{"error": err.Error()}, 0)
		return yield(core.Event{Type: core.EventPanic, Panic: pi, Time: time.Now()}, nil)
	}
	return true
}

func (r *ReActRunner) guardedHooks(ctx context.Context, s *store.Session, runID string, yield func(core.Event, error) bool) *hook.Chain {
	return r.hooks.Guarded(r.guard, func(err error) error {
		if !r.reportPanic(ctx, s, runID, yield, err) {
			return errYieldStopped
		}
		return nil
	}, r.opts.PanicPolicy == core.PanicFailRun)
}

func repairHistory(s *store.Session) {
	msgs, n := core.RepairToolCalls(s.Messages(), "execution outcome unavailable after interruption; do not assume success or cancellation")
	if n > 0 {
		s.Replace(msgs)
	}
}

func appendMissingResults(s *store.Session, calls []core.ToolCall, reason string) {
	for _, c := range calls {
		s.Append(core.MessageFromToolResult(core.ToolResult{ToolCallID: c.ID, Error: reason}))
	}
}

func (r *ReActRunner) toolTurn(
	ctx context.Context,
	s *store.Session,
	runID string,
	resp *model.Response,
	yield func(core.Event, error) bool,
) bool {
	calls := core.CloneMessage(resp.Message).ToolCalls
	ctx = ctxmgr.WithOffloadReferences(ctx, s.Messages())
	ctx, cancelTools := context.WithCancel(ctx)
	defer cancelTools()
	n := len(calls)
	hooks := r.guardedHooks(ctx, s, runID, yield)
	results := make([]core.ToolResult, n)
	panics := make([]*core.PanicInfo, n)
	runnable := make([]bool, n)
	executed := make([]bool, n)
	durations := make([]time.Duration, n)
	committed := false
	commit := func() {
		if committed {
			return
		}
		committed = true
		msgs := make([]core.Message, n)
		for i := range calls {
			results[i].ToolCallID = calls[i].ID
			msgs[i] = core.MessageFromToolResult(results[i])
		}
		s.Append(msgs...)
	}
	for i := range calls {
		results[i] = core.ToolResult{ToolCallID: calls[i].ID, Error: "not executed: run stopped before tool dispatch"}
	}
	// Even a consumer break at a ToolCall event must leave paired history.
	defer commit()
	for i := range calls {
		eventCall := core.CloneMessage(core.Message{ToolCalls: []core.ToolCall{calls[i]}}).ToolCalls[0]
		if !yield(core.Event{Type: core.EventToolCall, ToolCall: &eventCall, Time: time.Now()}, nil) {
			return false
		}
		id := calls[i].ID
		err := hooks.BeforeToolCall(ctx, &calls[i])
		// IDs are correlation keys, not a hook rewrite surface.
		calls[i].ID = id
		if err != nil {
			if errors.Is(err, errYieldStopped) {
				return false
			}
			if core.IsPanic(err) && r.opts.PanicPolicy == core.PanicFailRun {
				return r.fail(yield, err)
			}
			results[i].Error = "rejected by hook: " + err.Error()
			r.recordAudit(ctx, s.ID, runID, audit.AuditHookReject, "hook", map[string]any{"tool": calls[i].Name, "err": err.Error()}, 0)
			continue
		}
		runnable[i] = true
	}
	run := func(i int) {
		if err := ctx.Err(); err != nil {
			results[i].Error = "not executed: " + err.Error()
			return
		}
		start := time.Now()
		executed[i] = true
		results[i], panics[i] = r.runTool(ctx, &calls[i])
		if panics[i] != nil && r.opts.PanicPolicy == core.PanicFailRun {
			cancelTools()
		}
		durations[i] = time.Since(start)
	}
	if r.opts.SequentialTools {
		for i := range calls {
			if runnable[i] {
				run(i)
			}
		}
	} else {
		limit := r.opts.ToolConcurrency
		if limit <= 0 {
			limit = 8
		}
		jobs := make(chan int, n)
		for i := range calls {
			if runnable[i] {
				jobs <- i
			}
		}
		close(jobs)
		var wg sync.WaitGroup
		for range min(limit, len(jobs)) {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := range jobs {
					run(i)
				}
			}()
		}
		wg.Wait()
	}
	var fatal error
	for i := range calls {
		if executed[i] {
			id := calls[i].ID
			err := hooks.AfterToolCall(ctx, &calls[i], &results[i])
			calls[i].ID = id
			if err != nil {
				if errors.Is(err, errYieldStopped) {
					return false
				}
				if core.IsPanic(err) && r.opts.PanicPolicy == core.PanicFailRun {
					fatal = err
				}
				r.logger.Error("tinyagent: AfterToolCall hook failed", slog.String("tool", calls[i].Name), slog.Any("error", err))
			}
		}
		if runnable[i] {
			r.recordAudit(ctx, s.ID, runID, audit.AuditToolCall, "tool:"+calls[i].Name, map[string]any{"is_error": results[i].IsError(), "args": string(calls[i].Arguments)}, durations[i])
		}
	}
	// Commit the whole batch before notifying the consumer, including panic and
	// error results, so interrupting any notification cannot erase executed work.
	commit()
	for i := range calls {
		if panics[i] != nil {
			err := core.NewError(core.ErrKindPanic, panics[i].Component, "tool", fmt.Errorf("panic: %v", panics[i].Value))
			r.recordAudit(ctx, s.ID, runID, audit.AuditPanic, panics[i].Component, map[string]any{"error": err.Error()}, 0)
			if !yield(core.Event{Type: core.EventPanic, Panic: panics[i], Time: time.Now()}, nil) {
				return false
			}
			if r.opts.PanicPolicy == core.PanicFailRun {
				fatal = err
			}
		}
		result := results[i]
		if !yield(core.Event{Type: core.EventToolResult, ToolResult: &result, Time: time.Now()}, nil) {
			return false
		}
	}
	if fatal != nil {
		return r.fail(yield, fatal)
	}
	return true
}

// runTool 查找并执行工具，捕获其中的 panic。可安全并发调用。
func (r *ReActRunner) runTool(ctx context.Context, call *core.ToolCall) (core.ToolResult, *core.PanicInfo) {
	tool, ok := r.tools.Lookup(call.Name)
	if !ok {
		return core.ToolResult{ToolCallID: call.ID, Error: fmt.Sprintf("unknown tool %q", call.Name)}, nil
	}

	res, err := core.GuardValue(r.guard, "tool:"+call.Name, func() (core.ToolResult, error) {
		return tool.Run(ctx, call.Arguments)
	})

	var panicInfo *core.PanicInfo
	if err != nil {
		if pi, isPanic := panicInfoOf(err); isPanic {
			panicInfo = pi
		}
		res = core.ToolResult{ToolCallID: call.ID, Error: err.Error()}
	}
	res.ToolCallID = call.ID
	return res, panicInfo
}

func (r *ReActRunner) recordAudit(
	ctx context.Context,
	sessionID, runID string,
	kind audit.AuditRecordKind,
	component string,
	detail map[string]any,
	dur time.Duration,
) {
	if r.opts.Audit == nil {
		return
	}
	rec := audit.AuditRecord{
		SessionID: sessionID,
		RunID:     runID,
		Kind:      kind,
		Component: component,
		Detail:    detail,
		Duration:  dur,
	}
	auditCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	_, err := core.GuardValue(r.guard, "audit", func() (struct{}, error) { return struct{}{}, r.opts.Audit.Record(auditCtx, rec) })
	if err != nil {
		r.logger.Error("tinyagent: audit write failed",
			slog.String("kind", string(kind)),
			slog.Any("error", err))
	}
}

func panicInfoOf(err error) (*core.PanicInfo, bool) {
	e, ok := core.AsPanic(err)
	if !ok {
		return nil, false
	}
	return &core.PanicInfo{Component: e.Component, Value: e.Err, Stack: e.Stack}, true
}

const budgetGuidance = "Prefer delivering a candidate that satisfies the stated requirements over further refinement; stop as soon as the requirements are met."

func buildSystemPrompt(
	base string,
	ctx context.Context,
	startedAt time.Time,
	iteration, maxIter int,
	total *core.Usage,
	finalizing bool,
) string {
	var b strings.Builder
	b.WriteString(base)
	if b.Len() == 0 {
		b.WriteString("You are a helpful agent.")
	}
	if !strings.HasSuffix(b.String(), "\n") {
		b.WriteString("\n\n")
	}
	b.WriteString("[run budget] iteration=")
	b.WriteString(strconv.Itoa(iteration + 1))
	b.WriteString("/")
	b.WriteString(strconv.Itoa(maxIter))
	b.WriteString(" elapsed_seconds=")
	b.WriteString(strconv.FormatFloat(time.Since(startedAt).Seconds(), 'f', 0, 64))
	if deadline, ok := ctx.Deadline(); ok {
		b.WriteString(" remaining_seconds=")
		b.WriteString(strconv.FormatFloat(time.Until(deadline).Seconds(), 'f', 0, 64))
	}
	if total != nil && total.TotalTokens > 0 {
		b.WriteString(" tokens_used=")
		b.WriteString(strconv.Itoa(total.TotalTokens))
	}
	b.WriteString("\n")
	b.WriteString(budgetGuidance)
	if finalizing {
		b.WriteString("\nTool calls are disabled because the run budget is nearly exhausted. Provide your final answer now without calling tools.")
	}
	return b.String()
}

func budgetFinalizing(ctx context.Context, startedAt time.Time, iteration, maxIter int) bool {
	if iteration >= maxIter-1 {
		return true
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		return false
	}
	elapsed := time.Since(startedAt)
	remaining := time.Until(deadline)
	total := elapsed + remaining
	if total <= 0 {
		return false
	}
	return remaining <= total/10
}
