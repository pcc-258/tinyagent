package tinyagent

import (
	"context"
	"time"
)

// AuditRecordKind 标识审计记录的种类。
type AuditRecordKind string

const (
	// AuditRunStart 表示一次运行开始。
	AuditRunStart AuditRecordKind = "run_start"
	// AuditRunEnd 表示一次运行结束。
	AuditRunEnd AuditRecordKind = "run_end"
	// AuditModelCall 表示一次模型调用。
	AuditModelCall AuditRecordKind = "model_call"
	// AuditToolCall 表示一次工具调用。
	AuditToolCall AuditRecordKind = "tool_call"
	// AuditContextAction 表示一次上下文处理。
	AuditContextAction AuditRecordKind = "context_action"
	// AuditHookReject 表示一次被 Hook 否决的调用。
	AuditHookReject AuditRecordKind = "hook_reject"
	// AuditPanic 表示一次被捕获的 panic。
	AuditPanic AuditRecordKind = "panic"
)

// AuditRecord 是一条审计记录。
type AuditRecord struct {
	// Time 是记录时间。
	Time time.Time
	// SessionID 是所属会话。
	SessionID string
	// RunID 是所属运行。
	RunID string
	// Kind 是记录种类。
	Kind AuditRecordKind
	// Component 是相关组件名。
	Component string
	// Detail 是明细，如工具参数与结果。
	Detail map[string]any
	// Duration 是耗时。
	Duration time.Duration
	// Err 是相关错误。
	Err error
}

// AuditQuery 是审计查询条件。零值字段表示不限制。
type AuditQuery struct {
	// SessionID 限定会话。
	SessionID string
	// RunID 限定运行。
	RunID string
	// Kind 限定种类。
	Kind AuditRecordKind
	// Since 限定起始时间。
	Since time.Time
	// Limit 限定返回条数；<=0 表示不限制。
	Limit int
}

// Audit 记录 agent 行为，供事后追溯。
//
// Audit 是旁路订阅者，**不被任何模块依赖**，因此不会阻塞主流程
// （见 VISION §6.6）。它是第一版的标准功能，不是可选项。
//
// 写入失败必须上报，不能静默丢弃。
type Audit interface {
	// Record 写入一条记录。
	Record(ctx context.Context, rec AuditRecord) error
	// Query 查询记录。
	Query(ctx context.Context, q AuditQuery) ([]AuditRecord, error)
}
