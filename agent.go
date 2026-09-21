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
	runner runner.Runner
	store  store.Store
	logger *slog.Logger

	mu       sync.Mutex
	sessions map[string]*store.Session
}

// New 创建一个 Agent，未配置的模块一律使用默认实现。
func New(cfg Config) (*Agent, error) {
	if cfg.Model == nil {
		return nil, core.NewError(core.ErrKindConfig, "agent", "new", errors.New("Model is required"))
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
	if ctxMgr == nil {
		ctxMgr = ctxmgr.NewDefaultContextManager(counter)
	}

	st := cfg.Store
	if st == nil {
		st = store.NewMemoryStore()
	}

	ad := cfg.Audit
	if ad == nil {
		ad = audit.NewMemoryAudit()
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
		store:    st,
		logger:   logger,
		sessions: make(map[string]*store.Session),
	}

	if cfg.Runner != nil {
		a.runner = cfg.Runner
		return a, nil
	}

	rn, err := runner.NewReActRunner(runner.RunnerOptions{
		Model:           cfg.Model,
		System:          cfg.System,
		Tools:           cfg.Tools,
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
	})
	if err != nil {
		return nil, err
	}
	a.runner = rn
	return a, nil
}

// Session 返回指定 ID 的会话；内存中没有时从 Store 载入。
func (a *Agent) Session(ctx context.Context, id string) (*store.Session, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if s, ok := a.sessions[id]; ok {
		return s, nil
	}

	s := store.NewSession(id)
	snap, err := a.store.Load(ctx, id)
	switch {
	case err == nil:
		s.LoadFrom(*snap)
	case errors.Is(err, core.ErrNotFound):
	default:
		return nil, err
	}
	a.sessions[id] = s
	return s, nil
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
		s, err := a.Session(ctx, sessionID)
		if err != nil {
			yield(core.Event{}, err)
			return
		}

		defer func() {
			if err := a.store.Replace(context.WithoutCancel(ctx), sessionID, s.Messages()); err != nil {
				a.logger.Error("tinyagent: persist session failed",
					slog.String("session", sessionID), slog.Any("error", err))
			}
		}()

		for ev, err := range a.runner.Run(ctx, s, input) {
			if !yield(ev, err) {
				return
			}
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
