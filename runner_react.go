package tinyagent

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"log/slog"
	"strings"
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
	guard   *recoverer
	hooks   *hookChain
	tools   *ToolRegistry
	logger  *slog.Logger
	maxIter int
}

// NewReActRunner 构造默认 Runner。
func NewReActRunner(opts RunnerOptions) (*ReActRunner, error) {
	if opts.Model == nil {
		return nil, newError(ErrKindConfig, "runner", "new", errors.New("Model is required"))
	}

	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}

	reg := NewToolRegistry()
	for _, t := range opts.Tools {
		if t == nil {
			continue
		}
		if err := reg.Register(t); err != nil {
			return nil, newError(ErrKindConfig, "runner", "register tool", err)
		}
	}

	maxIter := opts.MaxIterations
	if maxIter <= 0 {
		maxIter = 16
	}

	return &ReActRunner{
		opts:    opts,
		guard:   newRecoverer(opts.PanicPolicy, logger, nil),
		hooks:   newHookChain(opts.Hooks),
		tools:   reg,
		logger:  logger,
		maxIter: maxIter,
	}, nil
}

// Run 实现 Runner。
func (r *ReActRunner) Run(ctx context.Context, s *Session, input string) iter.Seq2[Event, error] {
	return func(yield func(Event, error) bool) {
		r.run(ctx, s, input, yield)
	}
}

func (r *ReActRunner) run(ctx context.Context, s *Session, input string, yield func(Event, error) bool) {
	if s == nil {
		yield(Event{}, newError(ErrKindRunner, "runner", "run", errors.New("nil session")))
		return
	}
	if !s.tryAcquire() {
		yield(Event{}, ErrSessionBusy)
		return
	}
	defer s.release()

	if r.opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, r.opts.Timeout)
		defer cancel()
	}

	runID := newRunID()
	startedAt := time.Now()
	r.recordAudit(ctx, s.ID, runID, AuditRunStart, "runner", nil, 0)
	defer func() {
		r.recordAudit(ctx, s.ID, runID, AuditRunEnd, "runner", nil, time.Since(startedAt))
	}()

	if !yield(Event{Type: EventRunStart, Time: time.Now()}, nil) {
		return
	}
	if input != "" {
		s.Append(Message{Role: RoleUser, Content: input})
	}

	var total Usage
	for i := 0; i < r.maxIter; i++ {
		if err := ctx.Err(); err != nil {
			yield(Event{Type: EventRunEnd, Usage: &total, Err: err, Time: time.Now()}, err)
			return
		}

		resp, ok := r.modelTurn(ctx, s, runID, &total, yield)
		if !ok || resp == nil {
			return
		}

		if len(resp.Message.ToolCalls) == 0 {
			yield(Event{Type: EventRunEnd, Usage: &total, Time: time.Now()}, nil)
			return
		}

		if !r.toolTurn(ctx, s, runID, resp, yield) {
			return
		}
	}

	yield(Event{Type: EventRunEnd, Usage: &total, Err: ErrMaxIterations, Time: time.Now()}, ErrMaxIterations)
}

// modelTurn 执行一轮模型调用：上下文处理 → Hook → 调用模型 → 回填。
//
// 返回 ok=false 表示事件消费方已中断或发生错误，调用方应立即返回。
func (r *ReActRunner) modelTurn(
	ctx context.Context,
	s *Session,
	runID string,
	total *Usage,
	yield func(Event, error) bool,
) (*Response, bool) {
	msgs := s.Messages()
	tools := r.tools.Infos()

	if r.opts.Context != nil && r.opts.ContextBudget > 0 {
		processed, actions, err := r.opts.Context.Prepare(ctx, msgs, tools, r.opts.System, r.opts.ContextBudget)
		if err != nil {
			return nil, r.fail(yield, newError(ErrKindContext, "context", "prepare", err))
		}
		for i := range actions {
			if !yield(Event{Type: EventContextAction, ContextAction: &actions[i], Time: time.Now()}, nil) {
				return nil, false
			}
			r.recordAudit(ctx, s.ID, runID, AuditContextAction, "context", actions[i].Detail, 0)
		}
		if len(actions) > 0 {
			s.Replace(processed)
			msgs = processed
		}
	}

	req := &Request{Messages: msgs, System: r.opts.System, Tools: tools}

	if err := r.hooks.beforeModelCall(ctx, req); err != nil {
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

	if err := r.hooks.afterModelCall(ctx, resp); err != nil {
		return nil, r.fail(yield, err)
	}

	if !streamed && resp.Message.Content != "" {
		if !yield(Event{Type: EventText, Text: resp.Message.Content, Time: time.Now()}, nil) {
			return nil, false
		}
	}

	total.Add(resp.Usage)
	s.Append(resp.Message)
	r.recordAudit(ctx, s.ID, runID, AuditModelCall, "model", map[string]any{
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
	req *Request,
	yield func(Event, error) bool,
) (*Response, bool, error) {
	if sm, ok := r.opts.Model.(StreamingModel); ok {
		resp, err := r.streamModel(ctx, sm, req, yield)
		return resp, true, err
	}

	resp, err := guardValue(r.guard, "model", func() (Response, error) {
		return r.opts.Model.Generate(ctx, *req)
	})
	if err != nil {
		r.reportPanic(yield, err)
		return nil, false, newError(ErrKindModel, "model", "Generate", err)
	}
	return &resp, false, nil
}

// streamModel 消费流式响应：文本增量实时产出，工具调用分片按 Index 聚合。
func (r *ReActRunner) streamModel(
	ctx context.Context,
	sm StreamingModel,
	req *Request,
	yield func(Event, error) bool,
) (*Response, error) {
	acc := NewToolCallAccumulator()
	var sb strings.Builder
	var usage Usage
	var finish string

	_, err := guardValue(r.guard, "model", func() (struct{}, error) {
		for chunk, cerr := range sm.Stream(ctx, *req) {
			if cerr != nil {
				return struct{}{}, cerr
			}
			if chunk.TextDelta != "" {
				sb.WriteString(chunk.TextDelta)
				if !yield(Event{Type: EventText, Text: chunk.TextDelta, Time: time.Now()}, nil) {
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
		return nil, newError(ErrKindModel, "model", "Stream", err)
	}

	return &Response{
		Message: Message{
			Role:      RoleAssistant,
			Content:   sb.String(),
			ToolCalls: acc.Calls(),
		},
		Usage:        usage,
		FinishReason: finish,
	}, nil
}

func (r *ReActRunner) fail(yield func(Event, error) bool, err error) bool {
	yield(Event{Type: EventRunEnd, Err: err, Time: time.Now()}, err)
	return false
}

func (r *ReActRunner) reportPanic(yield func(Event, error) bool, err error) {
	if pi, ok := panicInfoOf(err); ok {
		yield(Event{Type: EventPanic, Panic: pi, Time: time.Now()}, nil)
	}
}

func (r *ReActRunner) toolTurn(
	ctx context.Context,
	s *Session,
	runID string,
	resp *Response,
	yield func(Event, error) bool,
) bool {
	for i := range resp.Message.ToolCalls {
		call := resp.Message.ToolCalls[i]

		if !yield(Event{Type: EventToolCall, ToolCall: &call, Time: time.Now()}, nil) {
			return false
		}

		res, panicInfo := r.execTool(ctx, s.ID, runID, &call)

		if panicInfo != nil {
			if !yield(Event{Type: EventPanic, Panic: panicInfo, Time: time.Now()}, nil) {
				return false
			}
			if r.opts.PanicPolicy == PanicFailRun {
				err := newError(ErrKindPanic, panicInfo.Component, "tool", fmt.Errorf("panic: %v", panicInfo.Value))
				yield(Event{Type: EventRunEnd, Err: err, Time: time.Now()}, err)
				return false
			}
		}

		if !yield(Event{Type: EventToolResult, ToolResult: &res, Time: time.Now()}, nil) {
			return false
		}

		s.Append(MessageFromToolResult(res))
	}
	return true
}

// execTool 执行单个工具：Hook → 查找 → 执行（含 panic 捕获）→ Hook → 审计。
//
// 工具返回的 error 是框架级失败，会被转成软错误喂回模型；
// ToolResult.Error 则是工具主动报告的业务错误。两者都不中断循环。
func (r *ReActRunner) execTool(
	ctx context.Context,
	sessionID, runID string,
	call *ToolCall,
) (ToolResult, *PanicInfo) {
	startedAt := time.Now()

	if err := r.hooks.beforeToolCall(ctx, call); err != nil {
		res := ToolResult{ToolCallID: call.ID, Error: "rejected by hook: " + err.Error()}
		r.recordAudit(ctx, sessionID, runID, AuditHookReject, "hook", map[string]any{
			"tool": call.Name,
			"err":  err.Error(),
		}, time.Since(startedAt))
		return res, nil
	}

	tool, ok := r.tools.Lookup(call.Name)
	if !ok {
		res := ToolResult{ToolCallID: call.ID, Error: fmt.Sprintf("unknown tool %q", call.Name)}
		r.recordAudit(ctx, sessionID, runID, AuditToolCall, "tool:"+call.Name, map[string]any{
			"error": "unknown tool",
		}, time.Since(startedAt))
		return res, nil
	}

	res, err := guardValue(r.guard, "tool:"+call.Name, func() (ToolResult, error) {
		return tool.Run(ctx, call.Arguments)
	})

	var panicInfo *PanicInfo
	if err != nil {
		if pi, isPanic := panicInfoOf(err); isPanic {
			panicInfo = pi
		}
		res = ToolResult{ToolCallID: call.ID, Error: err.Error()}
	}
	res.ToolCallID = call.ID

	if hookErr := r.hooks.afterToolCall(ctx, call, &res); hookErr != nil {
		r.logger.Error("tinyagent: AfterToolCall hook failed",
			slog.String("tool", call.Name), slog.Any("error", hookErr))
	}

	r.recordAudit(ctx, sessionID, runID, AuditToolCall, "tool:"+call.Name, map[string]any{
		"is_error": res.IsError(),
		"args":     string(call.Arguments),
	}, time.Since(startedAt))

	return res, panicInfo
}

func (r *ReActRunner) recordAudit(
	ctx context.Context,
	sessionID, runID string,
	kind AuditRecordKind,
	component string,
	detail map[string]any,
	dur time.Duration,
) {
	if r.opts.Audit == nil {
		return
	}
	rec := AuditRecord{
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

func panicInfoOf(err error) (*PanicInfo, bool) {
	e, ok := AsError(err)
	if !ok || e.Kind != ErrKindPanic {
		return nil, false
	}
	return &PanicInfo{Component: e.Component, Value: e.Err, Stack: e.Stack}, true
}
