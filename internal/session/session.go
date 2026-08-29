// Package session 定义会话及多会话管理。
//
// 会话归内核所有,客户端无状态。一个 session 可被多个客户端订阅,
// 事件广播给所有订阅者。session 支持多设备并发连接。
package session

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sync"
	"time"
)

var (
	// ErrNotFound 表示会话不存在。
	ErrNotFound = errors.New("session not found")
	// ErrWorkspaceLocked 表示已绑定项目的会话不能切换或清空工作目录。
	ErrWorkspaceLocked = errors.New("session workspace is locked")
	// ErrInvalidReasoningEffort 表示推理强度不在内核支持的统一档位中。
	ErrInvalidReasoningEffort = errors.New("invalid reasoning effort")
)

// Phase 是会话当前阶段。
type Phase string

const (
	PhaseIdle    Phase = "idle"
	PhaseTurn    Phase = "turn"
	PhaseCompact Phase = "compaction"
)

// ReasoningEffort 是推理模型的会话级推理强度。
// 空值表示不覆盖模型服务的默认行为。
type ReasoningEffort string

const (
	ReasoningEffortLow    ReasoningEffort = "low"
	ReasoningEffortMedium ReasoningEffort = "medium"
	ReasoningEffortHigh   ReasoningEffort = "high"
)

// ValidReasoningEffort reports whether an effort can be sent to the current
// OpenAI-compatible provider. Empty means "follow the model default".
func ValidReasoningEffort(effort string) bool {
	switch ReasoningEffort(effort) {
	case "", ReasoningEffortLow, ReasoningEffortMedium, ReasoningEffortHigh:
		return true
	default:
		return false
	}
}

// Session 是一个长生命周期的交互会话。
type Session struct {
	ID              string          `json:"id"`
	ParentID        string          `json:"parent_id,omitempty"`
	Phase           Phase           `json:"phase"`
	ConnectionID    string          `json:"connection_id"`
	Model           string          `json:"model"`
	ReasoningEffort ReasoningEffort `json:"reasoning_effort,omitempty"`
	Workspace       string          `json:"workspace,omitempty"`
	ApprovalMode    string          `json:"approval_mode,omitempty"`
	Title           string          `json:"title,omitempty"`
	TitleIsManual   bool            `json:"title_is_manual,omitempty"`
	Pinned          bool            `json:"pinned,omitempty"`
	PinnedAt        *time.Time      `json:"pinned_at,omitempty"`
	CreatedAt       time.Time       `json:"created_at"`
	UpdatedAt       time.Time       `json:"updated_at"`
}

// CreateOptions 是新建会话时可由客户端指定的参数。
// 零值字段由上层(backend/server)填充默认值。
type CreateOptions struct {
	ConnectionID    string
	Model           string
	ReasoningEffort ReasoningEffort
	Workspace       string
	ApprovalMode    string
}

// Manager 管理多会话生命周期。
type Manager interface {
	Create(opts CreateOptions) (*Session, error)
	Get(id string) (*Session, bool)
	List() []*Session
	// Update 局部更新会话配置。工作目录只能从空值绑定一次，绑定后不可更换。
	// 入参为指针,nil 表示该字段不变。
	Update(id string, connectionID, model, reasoningEffort, workspace, approvalMode *string) (*Session, error)
	// SetPhase 更新由内核控制的执行阶段。
	SetPhase(id string, phase Phase) (*Session, error)
	// SetGeneratedTitle 设置自动生成的标题(if-absent 语义)。
	// 仅当标题为空且用户未手动改名时写入,返回是否写入成功。
	// AI 结果永不覆盖手动改名。
	SetGeneratedTitle(id, title string) (bool, error)
	// ResetGeneratedTitle clears an automatic title before regenerating it from
	// an edited first turn. Manually assigned titles are never changed.
	ResetGeneratedTitle(id string) (*Session, bool, error)
	// Rename 手动改名,置 TitleIsManual=true,此后自动标题不再覆盖。
	Rename(id, title string) error
	// SetPinned 置顶/取消置顶。置顶记录 PinnedAt 用于同组内排序。
	SetPinned(id string, pinned bool) (*Session, error)
	// Delete 永久删除会话及其元数据。
	Delete(id string) error
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

func (m *memManager) Create(opts CreateOptions) (*Session, error) {
	if !ValidReasoningEffort(string(opts.ReasoningEffort)) {
		return nil, ErrInvalidReasoningEffort
	}
	now := time.Now()
	s := &Session{
		ID:              newID(),
		Phase:           PhaseIdle,
		ConnectionID:    opts.ConnectionID,
		Model:           opts.Model,
		ReasoningEffort: opts.ReasoningEffort,
		Workspace:       opts.Workspace,
		ApprovalMode:    opts.ApprovalMode,
		CreatedAt:       now,
		UpdatedAt:       now,
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

func (m *memManager) Update(
	id string,
	connectionID, model, reasoningEffort, workspace, approvalMode *string,
) (*Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[id]
	if !ok {
		return nil, ErrNotFound
	}
	if reasoningEffort != nil && !ValidReasoningEffort(*reasoningEffort) {
		return nil, ErrInvalidReasoningEffort
	}
	if workspace != nil && s.Workspace != "" && *workspace != s.Workspace {
		return nil, ErrWorkspaceLocked
	}
	if model != nil {
		s.Model = *model
	}
	if connectionID != nil {
		s.ConnectionID = *connectionID
	}
	if reasoningEffort != nil {
		s.ReasoningEffort = ReasoningEffort(*reasoningEffort)
	}
	if workspace != nil {
		s.Workspace = *workspace
	}
	if approvalMode != nil {
		s.ApprovalMode = *approvalMode
	}
	s.UpdatedAt = time.Now()
	return s, nil
}

func (m *memManager) SetPhase(id string, phase Phase) (*Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[id]
	if !ok {
		return nil, ErrNotFound
	}
	s.Phase = phase
	s.UpdatedAt = time.Now()
	return s, nil
}

func (m *memManager) SetGeneratedTitle(id, title string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[id]
	if !ok {
		return false, ErrNotFound
	}
	// if-absent:标题已存在或用户手动改过时,不覆盖。
	if s.Title != "" || s.TitleIsManual {
		return false, nil
	}
	s.Title = title
	s.UpdatedAt = time.Now()
	return true, nil
}

func (m *memManager) ResetGeneratedTitle(id string) (*Session, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[id]
	if !ok {
		return nil, false, ErrNotFound
	}
	if s.TitleIsManual || s.Title == "" {
		return s, false, nil
	}
	s.Title = ""
	s.UpdatedAt = time.Now()
	return s, true, nil
}

func (m *memManager) Rename(id, title string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[id]
	if !ok {
		return ErrNotFound
	}
	s.Title = title
	s.TitleIsManual = true
	s.UpdatedAt = time.Now()
	return nil
}

func (m *memManager) SetPinned(id string, pinned bool) (*Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[id]
	if !ok {
		return nil, ErrNotFound
	}
	s.Pinned = pinned
	if pinned {
		now := time.Now()
		s.PinnedAt = &now
	} else {
		s.PinnedAt = nil
	}
	return s, nil
}

func (m *memManager) Delete(id string) error {
	m.mu.Lock()
	delete(m.sessions, id)
	m.mu.Unlock()
	return nil
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
