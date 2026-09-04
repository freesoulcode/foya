// Package session 定义会话及多会话管理。
//
// 会话归内核所有,客户端无状态。一个 session 可被多个客户端订阅,
// 事件广播给所有订阅者。session 支持多设备并发连接。
package session

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
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

// AgentMode controls the tool surface independently from a transient turn phase.
type AgentMode string

const (
	AgentModeExecute   AgentMode = "execute"
	AgentModePlan      AgentMode = "plan"
	AgentModePlanReady AgentMode = "plan_ready"
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

// TaskStatus 是会话内执行任务的当前状态。
type TaskStatus string

const (
	TaskStatusPending    TaskStatus = "pending"
	TaskStatusInProgress TaskStatus = "in_progress"
	TaskStatusCompleted  TaskStatus = "completed"
)

// Task 是模型维护的会话内任务列表条目。
type Task struct {
	Content string     `json:"content"`
	Status  TaskStatus `json:"status"`
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
	AgentMode       AgentMode       `json:"agent_mode,omitempty"`
	PrePlanMode     AgentMode       `json:"pre_plan_mode,omitempty"`
	ConnectionID    string          `json:"connection_id"`
	Model           string          `json:"model"`
	ReasoningEffort ReasoningEffort `json:"reasoning_effort,omitempty"`
	ProjectID       string          `json:"project_id,omitempty"`
	ApprovalMode    string          `json:"approval_mode,omitempty"`
	Title           string          `json:"title,omitempty"`
	TitleIsManual   bool            `json:"title_is_manual,omitempty"`
	Pinned          bool            `json:"pinned,omitempty"`
	PinnedAt        *time.Time      `json:"pinned_at,omitempty"`
	Tasks           []Task          `json:"tasks,omitempty"`
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
	SetAgentMode(id string, mode, prePlanMode AgentMode) (*Session, error)
	SetTasks(id string, tasks []Task) (*Session, error)
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

func newID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func validateCreateOptions(opts CreateOptions) error {
	if !ValidReasoningEffort(string(opts.ReasoningEffort)) {
		return ErrInvalidReasoningEffort
	}
	if opts.ApprovalMode != "" &&
		!approval.ValidMode(approval.Mode(opts.ApprovalMode)) {
		return fmt.Errorf("%w: %q", ErrInvalidApprovalMode, opts.ApprovalMode)
	}
	return nil
}
