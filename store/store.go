package store

import (
	"context"
	"time"
	"github.com/pcc-258/tinyagent/core"
)

// SessionMeta 是会话的元信息。
type SessionMeta struct {
	// ID 是会话标识。
	ID string
	// CreatedAt 是创建时间。
	CreatedAt time.Time
	// UpdatedAt 是最后更新时间。
	UpdatedAt time.Time
	// Meta 是自定义元信息。
	Meta map[string]any
}

// SessionSnapshot 是会话的纯数据快照。
//
// Load 返回快照而非内存对象，避免持久化格式绑死内存模型
// （见 VISION §6.4 M3）。
type SessionSnapshot struct {
	// ID 是会话标识。
	ID string
	// Messages 是消息序列。
	Messages []core.Message
	// Meta 是自定义元信息。
	Meta map[string]any
}

// Store 是会话状态的持久化契约。
//
// Append 与 Replace 都必须保证原子性：一次运行产生的多条消息
// 要么全部写入，要么全部不写，不能留下半截状态。
type Store interface {
	// Load 载入会话快照；会话不存在时返回 ErrNotFound。
	Load(ctx context.Context, sessionID string) (*SessionSnapshot, error)
	// Append 追加消息。
	Append(ctx context.Context, sessionID string, msgs ...core.Message) error
	// Replace 重写整个消息序列，供上下文处理（压缩）使用。
	Replace(ctx context.Context, sessionID string, msgs []core.Message) error
	// List 列出所有会话的元信息。
	List(ctx context.Context) ([]SessionMeta, error)
	// Delete 删除会话。
	Delete(ctx context.Context, sessionID string) error
}
