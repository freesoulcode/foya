// Package subagent runs delegated work in durable child sessions.
package subagent

import (
	"context"

	"errors"

	"sync"
	"time"

	"github.com/freesoulcode/foya/internal/broker"
	conversation "github.com/freesoulcode/foya/internal/conversation"
)

const (
	defaultMaxConcurrency = 4
	defaultMaxPerRoot     = 4
	defaultMaxChildren    = 64
	defaultMaxTurns       = 20
	defaultTimeout        = 15 * time.Minute
)

var ErrParentSessionNotFound = errors.New("parent session not found")

type Runner interface {
	RunTurn(ctx context.Context, sessionID, userText string) error
}

type HistoryReader interface {
	History(ctx context.Context, sessionID string) ([]conversation.Message, error)
}

type ProjectResolver func(projectID string) (path string, ok bool)

type Started struct {
	ChildSessionID string `json:"child_session_id"`
	AgentRef       string `json:"agent_ref"`
	AgentName      string `json:"agent_name"`
	Task           string `json:"task"`
	ToolCallID     string `json:"tool_call_id"`
}

type Result struct {
	Status         string `json:"status"`
	ChildSessionID string `json:"child_session_id"`
	AgentRef       string `json:"agent_ref"`
	AgentName      string `json:"agent_name"`
	Output         string `json:"output,omitempty"`
	Error          string `json:"error,omitempty"`
}

type Lifecycle struct {
	RunID           string
	ParentSessionID string
	ChildSessionID  string
	AgentID         string
	AgentType       string
	Task            string
	Status          string
	Output          string
	Error           string
}

type Status string

const (
	StatusQueued      Status = "queued"
	StatusRunning     Status = "running"
	StatusCompleted   Status = "completed"
	StatusFailed      Status = "failed"
	StatusCancelled   Status = "cancelled"
	StatusInterrupted Status = "interrupted"
)

type ContextMode string

const (
	ContextNone      ContextMode = "none"
	ContextSelected  ContextMode = "selected"
	ContextSummary   ContextMode = "summary"
	ContextLastTurns ContextMode = "last_n_turns"
)

type ContextSelection struct {
	Mode        ContextMode `json:"mode,omitempty"`
	MessageSeqs []uint64    `json:"message_seqs,omitempty"`
	LastTurns   int         `json:"last_turns,omitempty"`
}

type Limits struct {
	MaxGlobalConcurrency int   `json:"max_global_concurrency"`
	MaxPerRoot           int   `json:"max_per_root"`
	MaxChildrenPerRoot   int   `json:"-"`
	MaxTreeTokens        int64 `json:"max_tree_tokens,omitempty"`
}

type Snapshot struct {
	ID               string     `json:"id"`
	Status           Status     `json:"status"`
	RootSessionID    string     `json:"root_session_id"`
	RootRunID        string     `json:"root_run_id"`
	ParentSessionID  string     `json:"parent_session_id"`
	ParentToolCallID string     `json:"parent_tool_call_id,omitempty"`
	ChildSessionID   string     `json:"child_session_id,omitempty"`
	AgentRef         string     `json:"agent_ref,omitempty"`
	AgentName        string     `json:"agent_name,omitempty"`
	Task             string     `json:"task"`
	Depth            int        `json:"depth"`
	TokensUsed       int64      `json:"tokens_used,omitempty"`
	Output           string     `json:"output,omitempty"`
	Error            string     `json:"error,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	StartedAt        *time.Time `json:"started_at,omitempty"`
	CompletedAt      *time.Time `json:"completed_at,omitempty"`
}

type Budget struct {
	RootSessionID string `json:"root_session_id"`
	RootRunID     string `json:"root_run_id"`
	TokensUsed    int64  `json:"tokens_used"`
	MaxTokens     int64  `json:"max_tokens,omitempty"`
	Exceeded      bool   `json:"exceeded"`
}

type runState struct {
	mu       sync.RWMutex
	snapshot Snapshot
	done     chan struct{}
	cancel   context.CancelFunc
}

// Manager coordinates child-session creation, concurrency, execution, and
// parent-session lifecycle events.
type Manager struct {
	definitions     *DefinitionManager
	sessions        conversation.Manager
	runner          Runner
	history         HistoryReader
	log             conversation.Log
	bus             *broker.Broker[conversation.Event]
	resolveProject  ProjectResolver
	mu              sync.RWMutex
	runs            map[string]*runState
	treeTokens      map[string]int64
	persistPath     string
	persistMu       sync.Mutex
	scheduleMu      sync.Mutex
	scheduleChanged chan struct{}
	limits          Limits
	globalActive    int
	rootActive      map[string]int
	onStart         func(context.Context, Lifecycle)
	onStop          func(context.Context, Lifecycle) (blocked bool, reason string)
}

func NewManager(
	definitions *DefinitionManager,
	sessions conversation.Manager,
	runner Runner,
	history HistoryReader,
	log conversation.Log,
	bus *broker.Broker[conversation.Event],
	resolveProject ProjectResolver,
	limits Limits,
) *Manager {
	if limits.MaxGlobalConcurrency <= 0 {
		limits.MaxGlobalConcurrency = defaultMaxConcurrency
	}
	if limits.MaxPerRoot <= 0 {
		limits.MaxPerRoot = defaultMaxPerRoot
	}
	if limits.MaxChildrenPerRoot <= 0 {
		limits.MaxChildrenPerRoot = defaultMaxChildren
	}
	return &Manager{
		definitions:     definitions,
		sessions:        sessions,
		runner:          runner,
		history:         history,
		log:             log,
		bus:             bus,
		resolveProject:  resolveProject,
		runs:            make(map[string]*runState),
		treeTokens:      make(map[string]int64),
		scheduleChanged: make(chan struct{}),
		limits:          limits,
		rootActive:      make(map[string]int),
	}
}

// SetLifecycleCallbacks installs optional callbacks for child-agent lifecycle
// hooks and telemetry. Callbacks are set during application construction.
func (m *Manager) SetLifecycleCallbacks(
	onStart func(context.Context, Lifecycle),
	onStop func(context.Context, Lifecycle) (blocked bool, reason string),
) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onStart = onStart
	m.onStop = onStop
}

// Limits returns the scheduler's current runtime configuration.
func (m *Manager) Limits() Limits {
	m.scheduleMu.Lock()
	defer m.scheduleMu.Unlock()
	return m.limits
}

// UpdateLimits applies new limits without interrupting active runs. When a
// limit is lowered below current usage, queued work waits for enough releases.
func (m *Manager) UpdateLimits(limits Limits) {
	if limits.MaxGlobalConcurrency <= 0 {
		limits.MaxGlobalConcurrency = defaultMaxConcurrency
	}
	if limits.MaxPerRoot <= 0 {
		limits.MaxPerRoot = defaultMaxPerRoot
	}
	m.scheduleMu.Lock()
	limits.MaxChildrenPerRoot = m.limits.MaxChildrenPerRoot
	m.limits = limits
	m.notifySchedulerLocked()
	m.scheduleMu.Unlock()
}
