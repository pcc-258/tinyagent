package store

import (
	"context"
	"sort"
	"sync"
	"time"
	"github.com/pcc-258/tinyagent/core"
)

// MemoryStore 是 Store 的内存实现。
//
// 进程退出即丢失，适合测试与短生命周期场景。
// 生产环境应替换为持久化实现（见 VISION §6.4 M3）。
type MemoryStore struct {
	mu       sync.RWMutex
	sessions map[string]*memorySession
}

type memorySession struct {
	snapshot SessionSnapshot
	created  time.Time
	updated  time.Time
}

// NewMemoryStore 创建一个内存 Store。
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{sessions: make(map[string]*memorySession)}
}

// Load 实现 Store。会话不存在时返回 core.ErrNotFound。
func (s *MemoryStore) Load(ctx context.Context, sessionID string) (*SessionSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	rec, ok := s.sessions[sessionID]
	if !ok {
		return nil, core.ErrNotFound
	}
	return cloneSnapshot(&rec.snapshot), nil
}

// Append 实现 Store。
func (s *MemoryStore) Append(ctx context.Context, sessionID string, msgs ...core.Message) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	rec := s.ensureLocked(sessionID)
	for _, m := range msgs {
		rec.snapshot.Messages = append(rec.snapshot.Messages, core.CloneMessage(m))
	}
	rec.updated = time.Now()
	return nil
}

// Replace 实现 Store。
func (s *MemoryStore) Replace(ctx context.Context, sessionID string, msgs []core.Message) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	rec := s.ensureLocked(sessionID)
	rec.snapshot.Messages = make([]core.Message, len(msgs))
	for i, m := range msgs {
		rec.snapshot.Messages[i] = core.CloneMessage(m)
	}
	rec.updated = time.Now()
	return nil
}

// List 实现 Store，按最后更新时间倒序返回。
func (s *MemoryStore) List(ctx context.Context) ([]SessionMeta, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]SessionMeta, 0, len(s.sessions))
	for id, rec := range s.sessions {
		out = append(out, SessionMeta{
			ID:        id,
			CreatedAt: rec.created,
			UpdatedAt: rec.updated,
			Meta:      core.CloneMeta(rec.snapshot.Meta),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
	return out, nil
}

// Delete 实现 Store。会话不存在时返回 core.ErrNotFound。
func (s *MemoryStore) Delete(ctx context.Context, sessionID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.sessions[sessionID]; !ok {
		return core.ErrNotFound
	}
	delete(s.sessions, sessionID)
	return nil
}

// ensureLocked 返回会话记录，不存在则创建。调用方必须持有写锁。
func (s *MemoryStore) ensureLocked(sessionID string) *memorySession {
	rec, ok := s.sessions[sessionID]
	if !ok {
		now := time.Now()
		rec = &memorySession{
			snapshot: SessionSnapshot{ID: sessionID, Meta: map[string]any{}},
			created:  now,
			updated:  now,
		}
		s.sessions[sessionID] = rec
	}
	return rec
}

func cloneSnapshot(snap *SessionSnapshot) *SessionSnapshot {
	out := &SessionSnapshot{
		ID:       snap.ID,
		Messages: make([]core.Message, len(snap.Messages)),
		Meta:     core.CloneMeta(snap.Meta),
	}
	for i, m := range snap.Messages {
		out.Messages[i] = core.CloneMessage(m)
	}
	return out
}
