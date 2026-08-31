// Package session 定义会话及多会话管理。
//
// 会话归内核所有,客户端无状态。一个 session 可被多个客户端订阅,
// 事件广播给所有订阅者。session 支持多设备并发连接。
package session

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/freesoulcode/foya/internal/approval"
)

var (
	// ErrNotFound 表示会话不存在。
	ErrNotFound = errors.New("session not found")
	// ErrProjectLocked 表示已绑定项目的会话不能切换或清空项目。
	ErrProjectLocked = errors.New("session project is locked")
	// ErrInvalidReasoningEffort 表示推理强度不在内核支持的统一档位中。
	ErrInvalidReasoningEffort = errors.New("invalid reasoning effort")
	// ErrInvalidApprovalMode 表示审批模式不是受支持的新模式。
	ErrInvalidApprovalMode = errors.New("invalid approval mode")
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
	SpawnedBy       *SpawnedBy      `json:"spawned_by,omitempty"`
	AgentRef        string          `json:"agent_ref,omitempty"`
	AgentName       string          `json:"agent_name,omitempty"`
	AgentDigest     string          `json:"agent_digest,omitempty"`
	Phase           Phase           `json:"phase"`
	ConnectionID    string          `json:"connection_id"`
	Model           string          `json:"model"`
	ReasoningEffort ReasoningEffort `json:"reasoning_effort,omitempty"`
	ProjectID       string          `json:"project_id,omitempty"`
	ApprovalMode    string          `json:"approval_mode,omitempty"`
	Title           string          `json:"title,omitempty"`
	TitleIsManual   bool            `json:"title_is_manual,omitempty"`
	Pinned          bool            `json:"pinned,omitempty"`
	PinnedAt        *time.Time      `json:"pinned_at,omitempty"`
	CreatedAt       time.Time       `json:"created_at"`
	UpdatedAt       time.Time       `json:"updated_at"`

	// AgentInstructions and AllowedTools are the immutable runtime snapshot
	// captured when a child session is created. They are intentionally omitted
	// from the public session representation.
	AgentInstructions string   `json:"-"`
	AllowedTools      []string `json:"-"`
	AgentMaxTurns     int      `json:"-"`
}

// SpawnedBy records durable child-session provenance.
type SpawnedBy struct {
	ParentRunID      string `json:"parent_run_id,omitempty"`
	ParentTurnID     string `json:"parent_turn_id,omitempty"`
	ParentToolCallID string `json:"parent_tool_call_id"`
}

// CreateOptions 是新建会话时可由客户端指定的参数。
// 零值字段由上层(backend/server)填充默认值。
type CreateOptions struct {
	ConnectionID      string
	Model             string
	ReasoningEffort   ReasoningEffort
	ProjectID         string
	ApprovalMode      string
	ParentID          string
	SpawnedBy         *SpawnedBy
	AgentRef          string
	AgentName         string
	AgentDigest       string
	AgentInstructions string
	AllowedTools      []string
	AgentMaxTurns     int
}

// Manager 管理多会话生命周期。
type Manager interface {
	Create(opts CreateOptions) (*Session, error)
	Get(id string) (*Session, bool)
	List() []*Session
	// Update 局部更新会话配置。项目只能从空值绑定一次，绑定后不可更换。
	// 入参为指针,nil 表示该字段不变。
	Update(id string, connectionID, model, reasoningEffort, projectID, approvalMode *string) (*Session, error)
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
	path     string
}

type persistedSession struct {
	Session
	AgentInstructions string   `json:"agent_instructions,omitempty"`
	AllowedTools      []string `json:"allowed_tools,omitempty"`
	AgentMaxTurns     int      `json:"agent_max_turns,omitempty"`
}

// NewMemManager 创建内存版会话管理器。
func NewMemManager() Manager {
	return &memManager{sessions: make(map[string]*Session)}
}

// NewPersistentManager restores session metadata from disk and persists every
// mutation atomically. Runtime phases are reset to idle because in-flight work
// is recovered separately as interrupted runs.
func NewPersistentManager(dataDir string) (Manager, error) {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, err
	}
	manager := &memManager{
		sessions: make(map[string]*Session),
		path:     filepath.Join(dataDir, "sessions.json"),
	}
	data, err := os.ReadFile(manager.path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	var items []persistedSession
	if len(data) > 0 {
		if err := json.Unmarshal(data, &items); err != nil {
			return nil, err
		}
	}
	droppedInvalid := false
	for _, stored := range items {
		if stored.ID == "" {
			continue
		}
		if stored.ApprovalMode != "" && !approval.ValidMode(approval.Mode(stored.ApprovalMode)) {
			droppedInvalid = true
			continue
		}
		item := stored.Session
		item.Phase = PhaseIdle
		item.AgentInstructions = stored.AgentInstructions
		item.AllowedTools = append([]string(nil), stored.AllowedTools...)
		item.AgentMaxTurns = stored.AgentMaxTurns
		manager.sessions[item.ID] = &item
	}
	if droppedInvalid {
		if err := manager.persistLocked(); err != nil {
			return nil, err
		}
	}
	return manager, nil
}

func (m *memManager) Create(opts CreateOptions) (*Session, error) {
	if !ValidReasoningEffort(string(opts.ReasoningEffort)) {
		return nil, ErrInvalidReasoningEffort
	}
	if opts.ApprovalMode != "" && !approval.ValidMode(approval.Mode(opts.ApprovalMode)) {
		return nil, fmt.Errorf("%w: %q", ErrInvalidApprovalMode, opts.ApprovalMode)
	}
	now := time.Now()
	var spawnedBy *SpawnedBy
	if opts.SpawnedBy != nil {
		snapshot := *opts.SpawnedBy
		spawnedBy = &snapshot
	}
	s := &Session{
		ID:                newID(),
		ParentID:          opts.ParentID,
		SpawnedBy:         spawnedBy,
		AgentRef:          opts.AgentRef,
		AgentName:         opts.AgentName,
		AgentDigest:       opts.AgentDigest,
		Phase:             PhaseIdle,
		ConnectionID:      opts.ConnectionID,
		Model:             opts.Model,
		ReasoningEffort:   opts.ReasoningEffort,
		ProjectID:         opts.ProjectID,
		ApprovalMode:      opts.ApprovalMode,
		CreatedAt:         now,
		UpdatedAt:         now,
		AgentInstructions: opts.AgentInstructions,
		AllowedTools:      append([]string(nil), opts.AllowedTools...),
		AgentMaxTurns:     opts.AgentMaxTurns,
	}
	m.mu.Lock()
	m.sessions[s.ID] = s
	if err := m.persistLocked(); err != nil {
		delete(m.sessions, s.ID)
		m.mu.Unlock()
		return nil, err
	}
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
	connectionID, model, reasoningEffort, projectID, approvalMode *string,
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
	if approvalMode != nil && !approval.ValidMode(approval.Mode(*approvalMode)) {
		return nil, fmt.Errorf("%w: %q", ErrInvalidApprovalMode, *approvalMode)
	}
	if projectID != nil && s.ProjectID != "" && *projectID != s.ProjectID {
		return nil, ErrProjectLocked
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
	if projectID != nil {
		s.ProjectID = *projectID
	}
	if approvalMode != nil {
		s.ApprovalMode = *approvalMode
	}
	s.UpdatedAt = time.Now()
	if err := m.persistLocked(); err != nil {
		return nil, err
	}
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
	if err := m.persistLocked(); err != nil {
		return nil, err
	}
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
	if err := m.persistLocked(); err != nil {
		return false, err
	}
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
	if err := m.persistLocked(); err != nil {
		return nil, false, err
	}
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
	return m.persistLocked()
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
	s.UpdatedAt = time.Now()
	if err := m.persistLocked(); err != nil {
		return nil, err
	}
	return s, nil
}

func (m *memManager) Delete(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.sessions, id)
	return m.persistLocked()
}

func (m *memManager) Close(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.sessions, id)
	return m.persistLocked()
}

func (m *memManager) persistLocked() error {
	if m.path == "" {
		return nil
	}
	items := make([]persistedSession, 0, len(m.sessions))
	for _, item := range m.sessions {
		items = append(items, persistedSession{
			Session: *item, AgentInstructions: item.AgentInstructions,
			AllowedTools:  append([]string(nil), item.AllowedTools...),
			AgentMaxTurns: item.AgentMaxTurns,
		})
	}
	data, err := json.MarshalIndent(items, "", "  ")
	if err != nil {
		return err
	}
	tmp := m.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, m.path)
}

func newID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
