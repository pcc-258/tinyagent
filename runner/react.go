package runner

import (
	"context"
	"errors"
	"fmt"
	"github.com/pcc-258/tinyagent/audit"
	"github.com/pcc-258/tinyagent/core"
	"github.com/pcc-258/tinyagent/hook"
	"github.com/pcc-258/tinyagent/model"
	"github.com/pcc-258/tinyagent/store"
	"github.com/pcc-258/tinyagent/tool"
	"iter"
	"log/slog"
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
	for i := 0; i < r.maxIter; i++ {
		if err := ctx.Err(); err != nil {
			yield(core.Event{Type: core.EventRunEnd, Usage: &total, Err: err, Time: time.Now()}, err)
			return
		}

		resp, ok := r.modelTurn(ctx, s, runID, &total, yield)
		if !ok || resp == nil {
			return
		}

		if len(resp.Message.ToolCalls) == 0 {
			yield(core.Event{Type: core.EventRunEnd, Usage: &total, Time: time.Now()}, nil)
			return
		}

		if !r.toolTurn(ctx, s, runID, resp, yield) {
			return
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
	yield func(core.Event, error) bool,
) (*model.Response, bool) {
	msgs := s.Messages()
	tools := r.tools.Infos()

	if r.opts.Context != nil && r.opts.ContextBudget > 0 {
		processed, actions, err := r.opts.Context.Prepare(ctx, msgs, tools, r.opts.System, r.opts.ContextBudget)
		if err != nil {
			return nil, r.fail(yield, core.NewError(core.ErrKindContext, "context", "prepare", err))
		}
		for i := range actions {
			if !yield(core.Event{Type: core.EventContextAction, ContextAction: &actions[i], Time: time.Now()}, nil) {
				return nil, false
			}
			r.recordAudit(ctx, s.ID, runID, audit.AuditContextAction, "context", actions[i].Detail, 0)
		}
		if len(actions) > 0 {
			s.Replace(processed)
			msgs = processed
		}
	}

	req := &model.Request{Messages: msgs, System: r.opts.System, Tools: tools}

	if err := r.hooks.BeforeModelCall(ctx, req); err != nil {
		return nil, r.fail(yield, err)
	}

	startedAt := time.Now()
	resp, streamed, err := r.callModel(ctx, req, yield)
	if err != nil {
		return nil, r.fail(yield, err)
	}
	if resp == nil {
		return nil, false
	}

	if err := r.hooks.AfterModelCall(ctx, resp); err != nil {
		return nil, r.fail(yield, err)
	}

	if !streamed && resp.Message.Content != "" {
		if !yield(core.Event{Type: core.EventText, Text: resp.Message.Content, Time: time.Now()}, nil) {
			return nil, false
		}
	}

	total.Add(resp.Usage)
	s.Append(resp.Message)
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
		r.reportPanic(yield, err)
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

	_, err := core.GuardValue(r.guard, "model", func() (struct{}, error) {
		for chunk, cerr := range sm.Stream(ctx, *req) {
			if cerr != nil {
				return struct{}{}, cerr
			}
			if chunk.TextDelta != "" {
				sb.WriteString(chunk.TextDelta)
				if !yield(core.Event{Type: core.EventText, Text: chunk.TextDelta, Time: time.Now()}, nil) {
					return struct{}{}, errYieldStopped
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
		return struct{}{}, nil
	})
	if err != nil {
		if errors.Is(err, errYieldStopped) {
			return nil, nil
		}
		r.reportPanic(yield, err)
		return nil, core.NewError(core.ErrKindModel, "model", "Stream", err)
	}
	if finish == "length" && acc.Empty() && strings.TrimSpace(sb.String()) == "" {
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

func (r *ReActRunner) reportPanic(yield func(core.Event, error) bool, err error) {
	if pi, ok := panicInfoOf(err); ok {
		yield(core.Event{Type: core.EventPanic, Panic: pi, Time: time.Now()}, nil)
	}
}

func (r *ReActRunner) toolTurn(
	ctx context.Context,
	s *store.Session,
	runID string,
	resp *model.Response,
	yield func(core.Event, error) bool,
) bool {
	calls := resp.Message.ToolCalls
	n := len(calls)

	results := make([]core.ToolResult, n)
	panics := make([]*core.PanicInfo, n)
	runnable := make([]bool, n)
	startedAt := make([]time.Time, n)

	for i := range calls {
		if !yield(core.Event{Type: core.EventToolCall, ToolCall: &calls[i], Time: time.Now()}, nil) {
			return false
		}
		startedAt[i] = time.Now()

		if err := r.hooks.BeforeToolCall(ctx, &calls[i]); err != nil {
			results[i] = core.ToolResult{ToolCallID: calls[i].ID, Error: "rejected by hook: " + err.Error()}
			r.recordAudit(ctx, s.ID, runID, audit.AuditHookReject, "hook", map[string]any{
				"tool": calls[i].Name,
				"err":  err.Error(),
			}, time.Since(startedAt[i]))
			continue
		}
		runnable[i] = true
	}

	concurrency := n
	if r.opts.SequentialTools {
		concurrency = 1
	}
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	for i := range calls {
		if !runnable[i] {
			continue
		}
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			results[i], panics[i] = r.runTool(ctx, &calls[i])
		}(i)
	}
	wg.Wait()

	for i := range calls {
		if panics[i] != nil {
			if !yield(core.Event{Type: core.EventPanic, Panic: panics[i], Time: time.Now()}, nil) {
				return false
			}
			if r.opts.PanicPolicy == core.PanicFailRun {
				err := core.NewError(core.ErrKindPanic, panics[i].Component, "tool", fmt.Errorf("panic: %v", panics[i].Value))
				yield(core.Event{Type: core.EventRunEnd, Err: err, Time: time.Now()}, err)
				return false
			}
		}

		if runnable[i] {
			if err := r.hooks.AfterToolCall(ctx, &calls[i], &results[i]); err != nil {
				r.logger.Error("tinyagent: AfterToolCall hook failed",
					slog.String("tool", calls[i].Name), slog.Any("error", err))
			}
			r.recordAudit(ctx, s.ID, runID, audit.AuditToolCall, "tool:"+calls[i].Name, map[string]any{
				"is_error": results[i].IsError(),
				"args":     string(calls[i].Arguments),
			}, time.Since(startedAt[i]))
		}

		if !yield(core.Event{Type: core.EventToolResult, ToolResult: &results[i], Time: time.Now()}, nil) {
			return false
		}
		s.Append(core.MessageFromToolResult(results[i]))
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
	if err := r.opts.Audit.Record(ctx, rec); err != nil {
		r.logger.Error("tinyagent: audit write failed",
			slog.String("kind", string(kind)),
			slog.Any("error", err))
	}
}

func panicInfoOf(err error) (*core.PanicInfo, bool) {
	e, ok := core.AsError(err)
	if !ok || e.Kind != core.ErrKindPanic {
		return nil, false
	}
	return &core.PanicInfo{Component: e.Component, Value: e.Err, Stack: e.Stack}, true
}
