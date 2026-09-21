package store

import (
	"sync"
	"time"
	"github.com/pcc-258/tinyagent/core"
)

// Session 是内存态的会话。
//
// Session 内部加锁，多个 goroutine 可以安全访问。但同一个 Session
// 不允许并发运行 —— 并发 Run 会返回 ErrSessionBusy（见 VISION §8）。
type Session struct {
	mu sync.Mutex

	// ID 是会话标识。
	ID string
	// Meta 是自定义元信息。
	Meta map[string]any
	// CreatedAt 是创建时间。
	CreatedAt time.Time
	// UpdatedAt 是最后更新时间。
	UpdatedAt time.Time

	messages []core.Message
	running  bool
}

// NewSession 创建一个会话。
func NewSession(id string) *Session {
	now := time.Now()
	return &Session{
		ID:        id,
		Meta:      map[string]any{},
		CreatedAt: now,
		UpdatedAt: now,
	}
}

// Len 返回消息条数。
func (s *Session) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.messages)
}

// Messages 返回消息序列的副本。
func (s *Session) Messages() []core.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]core.Message, len(s.messages))
	for i, m := range s.messages {
		out[i] = core.CloneMessage(m)
	}
	return out
}

// Append 追加消息。
func (s *Session) Append(msgs ...core.Message) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.messages = append(s.messages, msgs...)
	s.UpdatedAt = time.Now()
}

// Replace 重写消息序列。
func (s *Session) Replace(msgs []core.Message) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.messages = make([]core.Message, len(msgs))
	copy(s.messages, msgs)
	s.UpdatedAt = time.Now()
}

// Snapshot 返回会话的纯数据快照。
func (s *Session) Snapshot() SessionSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	msgs := make([]core.Message, len(s.messages))
	copy(msgs, s.messages)
	return SessionSnapshot{ID: s.ID, Messages: msgs, Meta: core.CloneMeta(s.Meta)}
}

// LoadFrom 用快照填充会话。
func (s *Session) LoadFrom(snap SessionSnapshot) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.messages = make([]core.Message, len(snap.Messages))
	copy(s.messages, snap.Messages)
	if snap.Meta != nil {
		s.Meta = core.CloneMeta(snap.Meta)
	}
}

// TryAcquire 尝试标记会话进入运行态。
func (s *Session) TryAcquire() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running {
		return false
	}
	s.running = true
	return true
}

// Release 释放运行态。
func (s *Session) Release() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.running = false
}
