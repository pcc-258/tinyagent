package tinyagent

import (
	"context"
	"errors"
	"iter"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/pcc-258/tinyagent/audit"
	"github.com/pcc-258/tinyagent/core"
	"github.com/pcc-258/tinyagent/ctxmgr"
	"github.com/pcc-258/tinyagent/hook"
	"github.com/pcc-258/tinyagent/model"
	"github.com/pcc-258/tinyagent/runner"
	"github.com/pcc-258/tinyagent/store"
	"github.com/pcc-258/tinyagent/tool"
)

const (
	defaultMaxIterations = 16
	defaultContextBudget = 128000
)

// Config 配置一个 Agent。
//
// 除 Model 外，所有字段都有可用默认值 —— 零配置即可运行。
type Config struct {
	// Model 是必需的模型实现。
	Model model.Model
	// System 是系统提示。
	System string
	// Tools 是初始工具集合。
	Tools []tool.Tool
	// Store 是会话存储；为空时使用内存实现。
	Store store.Store
	// Context 是上下文管理器；为空时使用默认策略链。
	Context ctxmgr.ContextManager
	// Counter 是 token 计数器；为空时使用启发式估算。
	Counter ctxmgr.Counter
	// ContextBudget 是上下文 token 预算；<=0 时使用默认值。
	ContextBudget int
	// SequentialTools 为 true 时逐个执行工具；默认并行执行且结果保序。
	SequentialTools bool
	// ToolConcurrency bounds parallel execution; <=0 uses 8 workers.
	ToolConcurrency int
	// StopConditions are checked after each completed model/tool step.
	StopConditions []runner.StopCondition
	// OutputReserve reserves context tokens and sets the model output-token cap.
	OutputReserve int
	// Offloader backs the default context manager and its read_offloaded tool.
	Offloader ctxmgr.Offloader
	// PersistTimeout bounds writeback even after the run context is canceled.
	// <=0 uses 10 seconds. Store implementations must respect context cancellation.
	PersistTimeout time.Duration
	// MaxCachedSessions bounds loaded sessions; <=0 means unlimited. Capacity
	// exhaustion is explicit: call UnloadSession to persist and release idle sessions.
	MaxCachedSessions int
	// Hooks 是按序执行的拦截器。
	Hooks []hook.Hook
	// Audit 是审计记录器；为空时使用内存实现。
	Audit audit.Audit
	// Logger 是结构化日志器；为空时使用 slog.Default()。
	Logger *slog.Logger
	// MaxIterations 是单次运行的最大迭代轮数；<=0 时使用默认值。
	MaxIterations int
	// Timeout 是单次运行的总超时；<=0 表示不额外限制。
	Timeout time.Duration
	// PanicPolicy 决定组件 panic 之后的处理方式。
	PanicPolicy core.PanicPolicy
	// Runner 是自定义运行循环；非空时整体替换默认实现。
	Runner runner.Runner
}

// Agent 是接入方直接使用的门面，把各模块装配成一个可用对象。
type Agent struct {
	runner            runner.Runner
	store             store.Store
	logger            *slog.Logger
	guard             *core.Recoverer
	persistTimeout    time.Duration
	maxCachedSessions int
	timeout           time.Duration

	mu       sync.Mutex
	sessions map[string]*sessionEntry
}

type sessionEntry struct {
	ready   chan struct{}
	session *store.Session
	err     error
	active  bool // protected by Agent.mu; includes persistence
}

// New 创建一个 Agent，未配置的模块一律使用默认实现。
func New(cfg Config) (*Agent, error) {
	if cfg.Model == nil {
		return nil, core.NewError(core.ErrKindConfig, "agent", "new", errors.New("Model is required"))
	}
	if cfg.OutputReserve < 0 {
		return nil, core.NewError(core.ErrKindConfig, "agent", "new", errors.New("OutputReserve must be non-negative"))
	}

	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	counter := cfg.Counter
	if counter == nil {
		counter = ctxmgr.NewSimpleCounter()
	}

	ctxMgr := cfg.Context
	tools := append([]tool.Tool(nil), cfg.Tools...)
	if ctxMgr == nil {
		offloader := cfg.Offloader
		if offloader == nil {
			offloader = ctxmgr.NewMemoryOffloader()
		}
		ctxMgr = ctxmgr.NewDefaultContextManagerWithOffloader(counter, offloader)
		readTool, err := ctxmgr.NewReadOffloadedTool(offloader)
		if err != nil {
			return nil, err
		}
		tools = append(tools, readTool)
	}

	st := cfg.Store
	if st == nil {
		st = store.NewMemoryStore()
	}

	ad := cfg.Audit
	if ad == nil {
		ad = audit.NewMemoryAudit().WithMaxRecords(10000)
	}

	budget := cfg.ContextBudget
	if budget <= 0 {
		budget = defaultContextBudget
	}

	maxIter := cfg.MaxIterations
	if maxIter <= 0 {
		maxIter = defaultMaxIterations
	}

	a := &Agent{
		store:             st,
		logger:            logger,
		sessions:          make(map[string]*sessionEntry),
		guard:             core.NewRecoverer(cfg.PanicPolicy, logger, nil),
		persistTimeout:    cfg.PersistTimeout,
		maxCachedSessions: cfg.MaxCachedSessions,
		timeout:           cfg.Timeout,
	}
	if a.persistTimeout <= 0 {
		a.persistTimeout = 10 * time.Second
	}

	if cfg.Runner != nil {
		a.runner = cfg.Runner
		return a, nil
	}

	rn, err := runner.NewReActRunner(runner.RunnerOptions{
		Model:           cfg.Model,
		System:          cfg.System,
		Tools:           tools,
		Store:           st,
		Context:         ctxMgr,
		Counter:         counter,
		Hooks:           cfg.Hooks,
		Audit:           ad,
		Logger:          logger,
		MaxIterations:   maxIter,
		Timeout:         cfg.Timeout,
		PanicPolicy:     cfg.PanicPolicy,
		ContextBudget:   budget,
		SequentialTools: cfg.SequentialTools,
		ToolConcurrency: cfg.ToolConcurrency,
		StopConditions:  cfg.StopConditions,
		OutputReserve:   cfg.OutputReserve,
	})
	if err != nil {
		return nil, err
	}
	a.runner = rn
	return a, nil
}

// Session 返回指定 ID 的会话；内存中没有时从 Store 载入。
func (a *Agent) Session(ctx context.Context, id string) (*store.Session, error) {
	e, err := a.session(ctx, id)
	if err != nil {
		return nil, err
	}
	return e.session, nil
}

func (a *Agent) session(ctx context.Context, id string) (*sessionEntry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	a.mu.Lock()
	if e, ok := a.sessions[id]; ok {
		a.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-e.ready:
			return e, e.err
		}
	}
	if a.maxCachedSessions > 0 && len(a.sessions) >= a.maxCachedSessions {
		a.mu.Unlock()
		return nil, core.NewError(core.ErrKindLimit, "agent", "load session", errors.New("session cache capacity reached; unload an idle session"))
	}
	e := &sessionEntry{ready: make(chan struct{})}
	a.sessions[id] = e
	a.mu.Unlock()
	completed := false
	defer func() {
		// Even PanicPropagate must not strand other callers waiting for this load.
		if !completed {
			a.mu.Lock()
			e.err = errors.New("session load aborted")
			delete(a.sessions, id)
			close(e.ready)
			a.mu.Unlock()
		}
	}()

	s := store.NewSession(id)
	snap, err := core.GuardValue(a.guard, "store", func() (*store.SessionSnapshot, error) { return a.store.Load(ctx, id) })
	switch {
	case err == nil:
		if snap == nil {
			err = errors.New("store returned nil snapshot without error")
		} else {
			s.LoadFrom(*snap)
		}
	case errors.Is(err, core.ErrNotFound):
		err = nil
	}
	a.mu.Lock()
	e.session, e.err = s, err
	if err != nil {
		delete(a.sessions, id)
	}
	close(e.ready)
	completed = true
	a.mu.Unlock()
	return e, err
}

// UnloadSession persists and releases an idle cached session without deleting
// its Store record. Previously returned Session handles must not be used after
// a successful unload. Active runs/loads are rejected, never evicted implicitly.
func (a *Agent) UnloadSession(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	a.mu.Lock()
	e, ok := a.sessions[id]
	if !ok {
		a.mu.Unlock()
		return core.ErrNotFound
	}
	select {
	case <-e.ready:
	default:
		a.mu.Unlock()
		return core.ErrSessionBusy
	}
	if e.active || !e.session.TryAcquire() {
		a.mu.Unlock()
		return core.ErrSessionBusy
	}
	e.active = true
	a.mu.Unlock()
	defer func() { e.session.Release(); a.mu.Lock(); e.active = false; a.mu.Unlock() }()
	writeCtx, cancel := context.WithTimeout(ctx, a.persistTimeout)
	defer cancel()
	_, err := core.GuardValue(a.guard, "store", func() (struct{}, error) { return struct{}{}, a.store.Replace(writeCtx, id, e.session.Messages()) })
	if err != nil {
		return core.NewError(core.ErrKindStore, "store", "unload", err)
	}
	a.mu.Lock()
	delete(a.sessions, id)
	a.mu.Unlock()
	return nil
}

// RegisterTool 向默认 Runner 动态注册一个工具。
//
// 若 Config.Runner 被替换，本方法不生效，工具应通过 runner.RunnerOptions 提供。
func (a *Agent) RegisterTool(t tool.Tool) error {
	r, ok := a.runner.(*runner.ReActRunner)
	if !ok {
		return core.NewError(core.ErrKindConfig, "agent", "register tool",
			errors.New("custom Runner does not support dynamic tool registration"))
	}
	return r.RegisterTool(t)
}

// Run 执行一次运行并产出事件流，运行结束后把会话状态写回 Store。
func (a *Agent) Run(ctx context.Context, sessionID, input string) iter.Seq2[core.Event, error] {
	return func(yield func(core.Event, error) bool) {
		if a.timeout > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, a.timeout)
			defer cancel()
		}
		e, err := a.session(ctx, sessionID)
		if err != nil {
			yield(core.Event{}, err)
			return
		}

		a.mu.Lock()
		if e.active || a.sessions[sessionID] != e {
			a.mu.Unlock()
			yield(core.Event{}, core.ErrSessionBusy)
			return
		}
		e.active = true
		a.mu.Unlock()
		defer func() { a.mu.Lock(); e.active = false; a.mu.Unlock() }()
		s := e.session
		started, persisted := false, false
		persist := func() error {
			if !started || persisted {
				return nil
			}
			persisted = true
			writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), a.persistTimeout)
			defer cancel()
			_, err := core.GuardValue(a.guard, "store", func() (struct{}, error) {
				return struct{}{}, a.store.Replace(writeCtx, sessionID, s.Messages())
			})
			if err != nil {
				a.logger.Error("tinyagent: persist session failed", slog.String("session", sessionID), slog.Any("error", err))
				return core.NewError(core.ErrKindStore, "store", "persist", err)
			}
			return nil
		}
		defer func() { _ = persist() }()
		var terminal *core.Event
		var runErr error

		for ev, err := range a.runner.Run(ctx, s, input) {
			if ev.Type == core.EventRunStart {
				started = true
			}
			if ev.Type == core.EventRunEnd {
				terminal, runErr = &ev, err
				continue
			}
			if !yield(ev, err) {
				return
			}
		}
		persistErr := persist()
		if terminal != nil {
			runErr = errors.Join(runErr, persistErr)
			terminal.Err = runErr
			yield(*terminal, runErr)
		} else if persistErr != nil {
			yield(core.Event{Type: core.EventRunEnd, Err: persistErr, Time: time.Now()}, persistErr)
		}
	}
}

// Chat 是 Run 的同步便捷版本，收集全部文本增量后返回。
func (a *Agent) Chat(ctx context.Context, sessionID, input string) (string, error) {
	var sb strings.Builder
	var runErr error
	for ev, err := range a.Run(ctx, sessionID, input) {
		if err != nil {
			runErr = err
			continue
		}
		if ev.Type == core.EventText {
			sb.WriteString(ev.Text)
		}
	}
	return sb.String(), runErr
}
