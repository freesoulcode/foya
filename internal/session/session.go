// Package session 定义会话及多会话管理。
//
// 会话归内核所有,客户端无状态。一个 session 可被多个客户端订阅,
// 事件广播给所有订阅者。session 支持多设备并发连接。
package session

import "time"

// Phase 是会话当前阶段。
type Phase string

const (
	PhaseIdle    Phase = "idle"
	PhaseTurn    Phase = "turn"
	PhaseCompact Phase = "compaction"
)

// Session 是一个长生命周期的交互会话。
type Session struct {
	ID        string
	ParentID  string // 子 agent 场景回指父会话
	Phase     Phase
	Model     string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Manager 管理多会话生命周期。
type Manager interface {
	Create(model string) (*Session, error)
	Get(id string) (*Session, bool)
	List() []*Session
	Close(id string) error
}
