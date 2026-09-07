// Package session defines chats and multi-chat lifecycle management.
//
// Chats belong to the kernel and clients remain stateless. Multiple clients
// can subscribe to one chat and receive the same event stream.
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
	// ErrNotFound indicates that a chat does not exist.
	ErrNotFound = errors.New("session not found")
	// ErrProjectLocked indicates that an attached project cannot be changed.
	ErrProjectLocked = errors.New("session project is locked")
	// ErrInvalidReasoningEffort indicates an unsupported reasoning level.
	ErrInvalidReasoningEffort = errors.New("invalid reasoning effort")
	// ErrInvalidApprovalMode indicates an unsupported approval mode.
	ErrInvalidApprovalMode = errors.New("invalid approval mode")
)

// Phase is the current chat phase.
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

// ReasoningEffort is the chat-level reasoning intensity.
// An empty value preserves the model provider default.
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

// TaskStatus is the current state of a task within a chat.
type TaskStatus string

const (
	TaskStatusPending    TaskStatus = "pending"
	TaskStatusInProgress TaskStatus = "in_progress"
	TaskStatusCompleted  TaskStatus = "completed"
)

// Task is one model-managed item in a chat task list.
type Task struct {
	Content string     `json:"content"`
	Status  TaskStatus `json:"status"`
}

// Session is a long-lived interactive chat.
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

// CreateOptions contains client-selected settings for a new chat.
// The backend or server fills defaults for zero values.
type CreateOptions struct {
	ConnectionID      string
	Model             string
	ReasoningEffort   ReasoningEffort
	ProjectID         string
	ApprovalMode      string
	Title             string
	TitleIsManual     bool
	ParentID          string
	SpawnedBy         *SpawnedBy
	AgentRef          string
	AgentName         string
	AgentDigest       string
	AgentInstructions string
	AllowedTools      []string
	AgentMaxTurns     int
}

// Manager controls the lifecycle of multiple chats.
type Manager interface {
	Create(opts CreateOptions) (*Session, error)
	Get(id string) (*Session, bool)
	List() []*Session
	// Update partially updates chat settings. A project can only be attached once.
	// A nil pointer leaves that field unchanged.
	Update(id string, connectionID, model, reasoningEffort, projectID, approvalMode *string) (*Session, error)
	// SetPhase updates the kernel-controlled execution phase.
	SetPhase(id string, phase Phase) (*Session, error)
	SetAgentMode(id string, mode, prePlanMode AgentMode) (*Session, error)
	SetTasks(id string, tasks []Task) (*Session, error)
	// SetGeneratedTitle stores a generated title only when no title exists.
	// It never overwrites a manual rename.
	SetGeneratedTitle(id, title string) (bool, error)
	// ResetGeneratedTitle clears an automatic title before regenerating it from
	// an edited first turn. Manually assigned titles are never changed.
	ResetGeneratedTitle(id string) (*Session, bool, error)
	// Rename assigns a manual title that generated titles cannot overwrite.
	Rename(id, title string) error
	// SetPinned updates pin state and records PinnedAt for ordering.
	SetPinned(id string, pinned bool) (*Session, error)
	// Delete permanently removes a chat and its metadata.
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
