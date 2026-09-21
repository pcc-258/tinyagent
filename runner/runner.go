package runner

import (
	"context"
	"iter"
	"log/slog"
	"time"
	"github.com/pcc-258/tinyagent/audit"
	"github.com/pcc-258/tinyagent/core"
	"github.com/pcc-258/tinyagent/ctxmgr"
	"github.com/pcc-258/tinyagent/hook"
	"github.com/pcc-258/tinyagent/model"
	"github.com/pcc-258/tinyagent/store"
	"github.com/pcc-258/tinyagent/tool"
)

// RunnerOptions 是 Runner 的注入面。
//
// 替换 Runner 实现时，RunnerOptions 才是真正的契约：自定义 Runner
// 必须处理其中的所有依赖，不能漏发事件、漏调 Hook、漏写 Audit。
type RunnerOptions struct {
	// Model 是必需的模型实现。
	Model model.Model
	// System 是系统提示，随每次模型调用发送。
	System string
	// ContextBudget 是上下文 token 预算；<=0 表示不做上下文处理。
	ContextBudget int
	// SequentialTools 为 true 时逐个执行工具；默认并行执行且结果保序。
	SequentialTools bool
	// Tools 是可调用的工具集合。
	Tools []tool.Tool
	// Store 是会话持久化实现。
	Store store.Store
	// Context 是上下文管理器。
	Context ctxmgr.ContextManager
	// Counter 是 token 计数器。
	Counter ctxmgr.Counter
	// Hooks 是按注册顺序执行的拦截器。
	Hooks []hook.Hook
	// Audit 是审计记录器。
	Audit audit.Audit
	// Logger 是结构化日志器；nil 时使用 slog.Default()。
	Logger *slog.Logger
	// MaxIterations 是单次运行的最大迭代轮数；<=0 时使用默认值。
	MaxIterations int
	// Timeout 是单次运行的总超时；<=0 表示不额外限制。
	Timeout time.Duration
	// PanicPolicy 决定组件 panic 之后的处理方式。
	PanicPolicy core.PanicPolicy
}

// Runner 驱动 agent 循环，可整体替换。
//
// 契约：
//   - 事件产出只在调用 Run 的那个 goroutine 中发生；
//   - 返回的迭代器被提前中断时，必须清理内部 goroutine，不得泄漏；
//   - 必须尊重 context 取消；
//   - 必须调用 RunnerOptions 中的 Hook 与 Audit，并产出对应事件。
type Runner interface {
	Run(ctx context.Context, s *store.Session, input string) iter.Seq2[core.Event, error]
}
