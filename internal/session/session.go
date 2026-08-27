// Package session 定义会话及多会话管理。
//
// 会话归内核所有,客户端无状态。一个 session 可被多个客户端订阅,
// 事件广播给所有订阅者。session 支持多设备并发连接。
package session

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

// Phase 是会话当前阶段。
type Phase string

const (
	PhaseIdle    Phase = "idle"
	PhaseTurn    Phase = "turn"
	PhaseCompact Phase = "compaction"
)

// Session 是一个长生命周期的交互会话。
type Session struct {
	ID        string    `json:"id"`
	ParentID  string    `json:"parent_id,omitempty"`
	Phase     Phase     `json:"phase"`
	Model     string    `json:"model"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Manager 管理多会话生命周期。
type Manager interface {
	Create(model string) (*Session, error)
	Get(id string) (*Session, bool)
	List() []*Session
	Close(id string) error
}

// memManager 是内存版会话管理器(脚手架阶段;后续由 SQLite 索引替换)。
type memManager struct {
	mu       sync.RWMutex
	sessions map[string]*Session
}

// NewMemManager 创建内存版会话管理器。
func NewMemManager() Manager {
	return &memManager{sessions: make(map[string]*Session)}
}

func (m *memManager) Create(model string) (*Session, error) {
	now := time.Now()
	s := &Session{
		ID:        newID(),
		Phase:     PhaseIdle,
		Model:     model,
		CreatedAt: now,
		UpdatedAt: now,
	}
	m.mu.Lock()
	m.sessions[s.ID] = s
	m.mu.Unlock()
	return s, nil
}

func (m *memManager) Get(id string) (*Session, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s, ok := m.sessions[id]
	return s, ok
}

func (m *memManager) List() []*Session {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*Session, 0, len(m.sessions))
	for _, s := range m.sessions {
		out = append(out, s)
	}
	return out
}

func (m *memManager) Close(id string) error {
	m.mu.Lock()
	delete(m.sessions, id)
	m.mu.Unlock()
	return nil
}

func newID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
