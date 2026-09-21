package tinyagent

import (
	"context"
	"sync"
	"time"
)

// MemoryAudit 是 Audit 的内存实现。
//
// 记录以追加方式写入内存，速度快，不会显著阻塞主流程。
// 进程退出即丢失；需要持久化时实现 Audit 接口替换。
type MemoryAudit struct {
	mu         sync.RWMutex
	records    []AuditRecord
	maxRecords int
}

// NewMemoryAudit 创建内存审计器。
func NewMemoryAudit() *MemoryAudit { return &MemoryAudit{} }

// WithMaxRecords 设置保留上限，超出后丢弃最旧记录；n <= 0 表示不限。
func (a *MemoryAudit) WithMaxRecords(n int) *MemoryAudit {
	a.maxRecords = n
	return a
}

// Record 实现 Audit。
func (a *MemoryAudit) Record(ctx context.Context, rec AuditRecord) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if rec.Time.IsZero() {
		rec.Time = time.Now()
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	a.records = append(a.records, rec)
	if a.maxRecords > 0 && len(a.records) > a.maxRecords {
		drop := len(a.records) - a.maxRecords
		kept := make([]AuditRecord, len(a.records)-drop)
		copy(kept, a.records[drop:])
		a.records = kept
	}
	return nil
}

// Query 实现 Audit，按写入顺序返回匹配记录。
func (a *MemoryAudit) Query(ctx context.Context, q AuditQuery) ([]AuditRecord, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	a.mu.RLock()
	defer a.mu.RUnlock()

	out := make([]AuditRecord, 0, len(a.records))
	for _, rec := range a.records {
		if q.SessionID != "" && rec.SessionID != q.SessionID {
			continue
		}
		if q.RunID != "" && rec.RunID != q.RunID {
			continue
		}
		if q.Kind != "" && rec.Kind != q.Kind {
			continue
		}
		if !q.Since.IsZero() && rec.Time.Before(q.Since) {
			continue
		}
		rec.Detail = cloneMeta(rec.Detail)
		out = append(out, rec)
		if q.Limit > 0 && len(out) >= q.Limit {
			break
		}
	}
	return out, nil
}

// Len 返回当前记录数。
func (a *MemoryAudit) Len() int {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return len(a.records)
}
