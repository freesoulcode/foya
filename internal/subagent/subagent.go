// Package subagent runs delegated work in durable child sessions.
package subagent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/freesoulcode/foya/internal/agentdef"
	"github.com/freesoulcode/foya/internal/broker"
	"github.com/freesoulcode/foya/internal/event"
	"github.com/freesoulcode/foya/internal/message"
	"github.com/freesoulcode/foya/internal/provider"
	"github.com/freesoulcode/foya/internal/session"
	"github.com/freesoulcode/foya/internal/state"
	foyatelemetry "github.com/freesoulcode/foya/internal/telemetry"
	"github.com/freesoulcode/foya/internal/tool"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
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
	History(ctx context.Context, sessionID string) ([]message.Message, error)
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
	definitions     *agentdef.Manager
	sessions        session.Manager
	runner          Runner
	history         HistoryReader
	log             state.Log
	bus             *broker.Broker[event.Event]
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
	definitions *agentdef.Manager,
	sessions session.Manager,
	runner Runner,
	history HistoryReader,
	log state.Log,
	bus *broker.Broker[event.Event],
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

type SpawnRequest struct {
	ParentSessionID  string
	ParentToolCallID string
	RootRunID        string
	Task             string
	AgentRef         string
	Context          ContextSelection
	RunID            string
	CancelWithParent bool
}

// Start schedules a child agent and returns immediately.
func (m *Manager) Start(ctx context.Context, request SpawnRequest) (Snapshot, error) {
	if _, ok := m.sessions.Get(request.ParentSessionID); !ok {
		return Snapshot{}, ErrParentSessionNotFound
	}
	rootSessionID, _ := m.rootAndDepth(request.ParentSessionID)
	rootRunID := request.RootRunID
	if rootRunID == "" {
		rootRunID = newID()
	}
	limits := m.Limits()
	m.mu.Lock()
	created := 0
	for _, existing := range m.runs {
		existing.mu.RLock()
		if existing.snapshot.RootRunID == rootRunID {
			created++
		}
		existing.mu.RUnlock()
	}
	if created >= limits.MaxChildrenPerRoot {
		m.mu.Unlock()
		return Snapshot{}, fmt.Errorf("internal safety limit reached: too many children per root run")
	}
	jobID := newID()
	runCtx, cancel := context.WithCancel(
		trace.ContextWithSpanContext(context.Background(), trace.SpanContextFromContext(ctx)),
	)
	state := &runState{
		snapshot: Snapshot{
			ID: jobID, Status: StatusQueued, RootSessionID: rootSessionID,
			RootRunID:        rootRunID,
			ParentSessionID:  request.ParentSessionID,
			ParentToolCallID: request.ParentToolCallID,
			AgentRef:         request.AgentRef, Task: strings.TrimSpace(request.Task),
			Depth: 1, CreatedAt: time.Now(),
		},
		done:   make(chan struct{}),
		cancel: cancel,
	}
	request.RunID = jobID
	m.runs[jobID] = state
	m.mu.Unlock()
	m.persist()
	m.emit(context.WithoutCancel(ctx), request.ParentSessionID, event.KindSubAgentQueued, state.snapshotCopy())

	if request.CancelWithParent {
		go func() {
			select {
			case <-ctx.Done():
				cancel()
			case <-state.done:
			}
		}()
	}
	go m.runAsync(runCtx, state, request, rootRunID)
	return state.snapshotCopy(), nil
}

func (m *Manager) runAsync(
	ctx context.Context,
	state *runState,
	request SpawnRequest,
	rootID string,
) {
	defer close(state.done)
	if err := m.acquireSlot(ctx, rootID); err != nil {
		m.finishState(state, StatusCancelled, Result{}, ctx.Err())
		return
	}
	defer m.releaseSlot(rootID)
	now := time.Now()
	state.mu.Lock()
	state.snapshot.Status = StatusRunning
	state.snapshot.StartedAt = &now
	state.mu.Unlock()
	m.persist()
	m.emit(context.WithoutCancel(ctx), request.ParentSessionID, event.KindSubAgentRunning, state.snapshotCopy())

	result, err := m.spawnNow(ctx, request)
	status := StatusCompleted
	if err != nil {
		status = StatusFailed
		if errors.Is(err, context.Canceled) {
			status = StatusCancelled
		}
	}
	m.finishState(state, status, result, err)
}

func (m *Manager) finishState(state *runState, status Status, result Result, err error) {
	now := time.Now()
	state.mu.Lock()
	state.snapshot.Status = status
	state.snapshot.ChildSessionID = result.ChildSessionID
	state.snapshot.AgentRef = result.AgentRef
	state.snapshot.AgentName = result.AgentName
	state.snapshot.Output = result.Output
	if err != nil {
		state.snapshot.Error = err.Error()
	} else {
		state.snapshot.Error = result.Error
	}
	state.snapshot.CompletedAt = &now
	state.mu.Unlock()
	m.persist()
	kind := event.KindSubAgentCompleted
	switch status {
	case StatusFailed:
		kind = event.KindSubAgentFailed
	case StatusCancelled:
		kind = event.KindSubAgentCancelled
	case StatusInterrupted:
		kind = event.KindSubAgentInterrupted
	}
	snapshot := state.snapshotCopy()
	m.emit(context.Background(), snapshot.ParentSessionID, kind, snapshot)
}

func (m *Manager) Wait(ctx context.Context, ids []string, waitAll bool) ([]Snapshot, error) {
	states, err := m.resolveRuns(ids)
	if err != nil {
		return nil, err
	}
	if waitAll {
		for _, state := range states {
			select {
			case <-state.done:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
	} else {
		cases := make(chan struct{}, len(states))
		for _, state := range states {
			go func(done <-chan struct{}) {
				select {
				case <-done:
					select {
					case cases <- struct{}{}:
					default:
					}
				case <-ctx.Done():
				}
			}(state.done)
		}
		select {
		case <-cases:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	out := make([]Snapshot, 0, len(states))
	for _, state := range states {
		out = append(out, state.snapshotCopy())
	}
	return out, nil
}

func (m *Manager) Read(id string) (Snapshot, error) {
	m.mu.RLock()
	state := m.runs[id]
	m.mu.RUnlock()
	if state == nil {
		return Snapshot{}, fs.ErrNotExist
	}
	return state.snapshotCopy(), nil
}

func (m *Manager) List(parentSessionID string) []Snapshot {
	m.mu.RLock()
	states := make([]*runState, 0, len(m.runs))
	for _, state := range m.runs {
		states = append(states, state)
	}
	m.mu.RUnlock()
	out := make([]Snapshot, 0, len(states))
	for _, state := range states {
		snapshot := state.snapshotCopy()
		if parentSessionID == "" || snapshot.ParentSessionID == parentSessionID {
			out = append(out, snapshot)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out
}

func (m *Manager) Cancel(id string) error {
	m.mu.RLock()
	state := m.runs[id]
	m.mu.RUnlock()
	if state == nil {
		return fs.ErrNotExist
	}
	state.cancel()
	return nil
}

func (m *Manager) CancelTree(parentSessionID string) {
	rootID := m.rootID(parentSessionID)
	for _, snapshot := range m.List("") {
		if snapshot.RootSessionID == rootID &&
			(snapshot.Status == StatusQueued || snapshot.Status == StatusRunning) {
			_ = m.Cancel(snapshot.ID)
		}
	}
}

func (m *Manager) Budget(sessionID string) Budget {
	rootRunID := m.budgetRoot(sessionID)
	rootSessionID := m.rootID(sessionID)
	m.mu.RLock()
	used := m.treeTokens[rootRunID]
	var latest time.Time
	if rootRunID == rootSessionID {
		for _, state := range m.runs {
			state.mu.RLock()
			if state.snapshot.RootSessionID == rootSessionID &&
				state.snapshot.CreatedAt.After(latest) {
				latest = state.snapshot.CreatedAt
				rootRunID = state.snapshot.RootRunID
				used = m.treeTokens[rootRunID]
			}
			state.mu.RUnlock()
		}
	}
	m.mu.RUnlock()
	limits := m.Limits()
	return Budget{
		RootSessionID: rootSessionID,
		RootRunID:     rootRunID,
		TokensUsed:    used,
		MaxTokens:     limits.MaxTreeTokens,
		Exceeded:      limits.MaxTreeTokens > 0 && used >= limits.MaxTreeTokens,
	}
}

// ObserveUsage charges every child model request to the root task tree.
func (m *Manager) ObserveUsage(sessionID string, usage provider.Usage) {
	item, ok := m.sessions.Get(sessionID)
	if !ok || item.ParentID == "" || usage.TotalTokens <= 0 {
		return
	}
	rootID := m.rootID(sessionID)
	if item.SpawnedBy != nil && item.SpawnedBy.ParentRunID != "" {
		rootID = item.SpawnedBy.ParentRunID
	}
	m.mu.Lock()
	m.treeTokens[rootID] += usage.TotalTokens
	used := m.treeTokens[rootID]
	for _, state := range m.runs {
		state.mu.Lock()
		if state.snapshot.RootRunID == rootID {
			state.snapshot.TokensUsed = used
		}
		state.mu.Unlock()
	}
	m.mu.Unlock()
	budget := m.budgetForRun(m.rootID(sessionID), rootID)
	m.emit(context.Background(), budget.RootSessionID, event.KindAgentBudgetUpdated, budget)
	m.persist()
	if budget.Exceeded {
		m.emit(context.Background(), budget.RootSessionID, event.KindAgentBudgetExceeded, budget)
		m.cancelRunTree(rootID)
	}
}

func (m *Manager) budgetForRun(rootSessionID, rootRunID string) Budget {
	m.mu.RLock()
	used := m.treeTokens[rootRunID]
	m.mu.RUnlock()
	limits := m.Limits()
	return Budget{
		RootSessionID: rootSessionID, RootRunID: rootRunID,
		TokensUsed: used, MaxTokens: limits.MaxTreeTokens,
		Exceeded: limits.MaxTreeTokens > 0 && used >= limits.MaxTreeTokens,
	}
}

func (m *Manager) cancelRunTree(rootRunID string) {
	for _, snapshot := range m.List("") {
		if snapshot.RootRunID == rootRunID &&
			(snapshot.Status == StatusQueued || snapshot.Status == StatusRunning) {
			_ = m.Cancel(snapshot.ID)
		}
	}
}

func (m *Manager) budgetRoot(sessionID string) string {
	if item, ok := m.sessions.Get(sessionID); ok &&
		item.SpawnedBy != nil && item.SpawnedBy.ParentRunID != "" {
		return item.SpawnedBy.ParentRunID
	}
	return m.rootID(sessionID)
}

func (m *Manager) resolveRuns(ids []string) ([]*runState, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*runState, 0, len(ids))
	for _, id := range ids {
		state := m.runs[id]
		if state == nil {
			return nil, fmt.Errorf("unknown agent run %q", id)
		}
		out = append(out, state)
	}
	return out, nil
}

func (m *Manager) rootAndDepth(sessionID string) (string, int) {
	root := sessionID
	depth := 0
	for {
		item, ok := m.sessions.Get(root)
		if !ok || item.ParentID == "" {
			return root, depth
		}
		root = item.ParentID
		depth++
	}
}

func (m *Manager) rootID(sessionID string) string {
	root, _ := m.rootAndDepth(sessionID)
	return root
}

func (m *Manager) acquireSlot(ctx context.Context, rootID string) error {
	for {
		m.scheduleMu.Lock()
		if m.globalActive < m.limits.MaxGlobalConcurrency &&
			m.rootActive[rootID] < m.limits.MaxPerRoot {
			m.globalActive++
			m.rootActive[rootID]++
			m.scheduleMu.Unlock()
			return nil
		}
		changed := m.scheduleChanged
		m.scheduleMu.Unlock()
		select {
		case <-changed:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (m *Manager) releaseSlot(rootID string) {
	m.scheduleMu.Lock()
	m.globalActive--
	m.rootActive[rootID]--
	if m.rootActive[rootID] == 0 {
		delete(m.rootActive, rootID)
	}
	m.notifySchedulerLocked()
	m.scheduleMu.Unlock()
}

func (m *Manager) notifySchedulerLocked() {
	close(m.scheduleChanged)
	m.scheduleChanged = make(chan struct{})
}

func (s *runState) snapshotCopy() Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.snapshot
}

func newID() string {
	data := make([]byte, 12)
	_, _ = rand.Read(data)
	return hex.EncodeToString(data)
}

func (m *Manager) Spawn(ctx context.Context, request SpawnRequest) (Result, error) {
	rootSessionID, _ := m.rootAndDepth(request.ParentSessionID)
	rootID := request.RootRunID
	if rootID == "" {
		rootID = rootSessionID
	}
	if err := m.acquireSlot(ctx, rootID); err != nil {
		return Result{}, ctx.Err()
	}
	defer m.releaseSlot(rootID)
	return m.spawnNow(ctx, request)
}

func (m *Manager) spawnNow(ctx context.Context, request SpawnRequest) (result Result, resultErr error) {
	parent, ok := m.sessions.Get(request.ParentSessionID)
	if !ok {
		return Result{}, ErrParentSessionNotFound
	}
	definition, err := m.resolveDefinition(ctx, parent, request.AgentRef)
	if err != nil {
		return Result{}, err
	}
	tools := definition.Tools
	if len(tools) == 0 {
		tools = agentdef.WorkerDefinition().Tools
	}
	model := definition.Model
	if model == "" {
		model = parent.Model
	}
	maxTurns := definition.MaxTurns
	if maxTurns <= 0 {
		maxTurns = defaultMaxTurns
	}
	child, err := m.sessions.Create(session.CreateOptions{
		ConnectionID:    parent.ConnectionID,
		Model:           model,
		ReasoningEffort: parent.ReasoningEffort,
		ProjectID:       parent.ProjectID,
		ApprovalMode:    parent.ApprovalMode,
		ParentID:        parent.ID,
		SpawnedBy: &session.SpawnedBy{
			ParentRunID: request.RootRunID, ParentToolCallID: request.ParentToolCallID,
		},
		AgentRef:          definition.Ref,
		AgentName:         definition.Name,
		AgentDigest:       definition.Digest,
		AgentInstructions: definition.Body,
		AllowedTools:      append([]string(nil), tools...),
		AgentMaxTurns:     maxTurns,
	})
	if err != nil {
		return Result{}, err
	}
	m.attachChild(request.RunID, child.ID, definition)
	started := Started{
		ChildSessionID: child.ID,
		AgentRef:       definition.Ref,
		AgentName:      definition.Name,
		Task:           strings.TrimSpace(request.Task),
		ToolCallID:     request.ParentToolCallID,
	}
	lifecycle := Lifecycle{
		RunID:           request.RootRunID,
		ParentSessionID: parent.ID,
		ChildSessionID:  child.ID,
		AgentID:         request.RunID,
		AgentType:       definition.Ref,
		Task:            strings.TrimSpace(request.Task),
	}
	if lifecycle.AgentID == "" {
		lifecycle.AgentID = child.ID
	}
	rootSessionID, _ := m.rootAndDepth(parent.ID)
	ctx, subagentSpan := foyatelemetry.StartSpan(
		ctx,
		"foya.subagent",
		trace.SpanKindInternal,
		attribute.String("session.id", child.ID),
		attribute.String("langfuse.session.id", rootSessionID),
		attribute.String("langfuse.trace.name", "foya.turn"),
		attribute.String("langfuse.observation.type", "agent"),
		attribute.String("foya.parent.session.id", parent.ID),
		attribute.String("foya.run.id", lifecycle.RunID),
		attribute.String("foya.agent.id", lifecycle.AgentID),
		attribute.String("foya.agent.type", definition.Ref),
	)
	m.notifyLifecycleStart(context.WithoutCancel(ctx), lifecycle)
	defer func() {
		lifecycle.Status = result.Status
		lifecycle.Output = result.Output
		lifecycle.Error = result.Error
		if resultErr != nil && lifecycle.Error == "" {
			lifecycle.Error = resultErr.Error()
		}
		if lifecycle.Status == "" && resultErr != nil {
			lifecycle.Status = string(StatusFailed)
		}
		if blocked, reason := m.notifyLifecycleStop(context.WithoutCancel(ctx), lifecycle); blocked {
			lifecycle.Status = string(StatusFailed)
			lifecycle.Error = reason
			result.Status = lifecycle.Status
			result.Error = reason
			resultErr = errors.New(reason)
		}
		finalAttrs := []attribute.KeyValue{
			attribute.String("foya.subagent.status", lifecycle.Status),
		}
		if foyatelemetry.CaptureContent() {
			finalAttrs = append(finalAttrs,
				attribute.String("foya.subagent.task", lifecycle.Task),
				attribute.String("foya.subagent.output", lifecycle.Output),
			)
		}
		foyatelemetry.EndSpan(subagentSpan, lifecycle.Status, resultErr, finalAttrs...)
	}()

	if request.RunID == "" {
		m.emit(context.WithoutCancel(ctx), parent.ID, event.KindSubAgentStarted, started)
	}

	timeout := defaultTimeout
	if definition.Timeout != "" {
		timeout, _ = time.ParseDuration(definition.Timeout)
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	task, err := m.taskPackage(runCtx, parent.ID, request.Task, request.Context)
	if err != nil {
		return Result{}, err
	}
	err = m.runner.RunTurn(runCtx, child.ID, task)
	output := m.finalOutput(context.WithoutCancel(ctx), child.ID)
	result = Result{
		Status: "completed", ChildSessionID: child.ID,
		AgentRef: definition.Ref, AgentName: definition.Name, Output: output,
	}
	if err != nil || runCtx.Err() != nil {
		result.Status = "failed"
		if runCtx.Err() != nil {
			result.Error = runCtx.Err().Error()
			err = runCtx.Err()
		} else {
			result.Error = err.Error()
		}
		if request.RunID == "" {
			m.emit(context.WithoutCancel(ctx), parent.ID, event.KindSubAgentFailed, result)
		}
		return result, err
	}
	if strings.TrimSpace(output) == "" {
		result.Status = "failed"
		result.Error = "sub-agent completed without a final answer"
		if request.RunID == "" {
			m.emit(context.WithoutCancel(ctx), parent.ID, event.KindSubAgentFailed, result)
		}
		return result, errors.New(result.Error)
	}
	if request.RunID == "" {
		m.emit(context.WithoutCancel(ctx), parent.ID, event.KindSubAgentCompleted, result)
	}
	return result, nil
}

func (m *Manager) notifyLifecycleStart(ctx context.Context, lifecycle Lifecycle) {
	m.mu.RLock()
	callback := m.onStart
	m.mu.RUnlock()
	if callback != nil {
		callback(ctx, lifecycle)
	}
}

func (m *Manager) notifyLifecycleStop(
	ctx context.Context,
	lifecycle Lifecycle,
) (bool, string) {
	m.mu.RLock()
	callback := m.onStop
	m.mu.RUnlock()
	if callback != nil {
		return callback(ctx, lifecycle)
	}
	return false, ""
}

func (m *Manager) attachChild(runID, childID string, definition agentdef.Definition) {
	if runID == "" {
		return
	}
	m.mu.RLock()
	state := m.runs[runID]
	m.mu.RUnlock()
	if state == nil {
		return
	}
	state.mu.Lock()
	state.snapshot.ChildSessionID = childID
	state.snapshot.AgentRef = definition.Ref
	state.snapshot.AgentName = definition.Name
	state.mu.Unlock()
	m.persist()
	m.emit(context.Background(), state.snapshotCopy().ParentSessionID, event.KindSubAgentStarted, state.snapshotCopy())
}

func (m *Manager) taskPackage(
	ctx context.Context,
	parentSessionID, task string,
	selection ContextSelection,
) (string, error) {
	task = strings.TrimSpace(task)
	if selection.Mode == "" || selection.Mode == ContextNone {
		return task, nil
	}
	history, err := m.history.History(ctx, parentSessionID)
	if err != nil {
		return "", fmt.Errorf("read parent context: %w", err)
	}
	var selected []message.Message
	switch selection.Mode {
	case ContextSelected:
		wanted := make(map[uint64]bool, len(selection.MessageSeqs))
		for _, seq := range selection.MessageSeqs {
			wanted[seq] = true
		}
		for _, item := range history {
			if wanted[item.EventSeq] {
				selected = append(selected, item)
			}
		}
		if len(wanted) > 0 && len(selected) == 0 {
			return "", errors.New("selected parent messages were not found")
		}
	case ContextLastTurns:
		turns := selection.LastTurns
		if turns <= 0 {
			turns = 1
		}
		start := len(history)
		for start > 0 {
			start--
			if history[start].Role == message.RoleUser {
				turns--
				if turns == 0 {
					break
				}
			}
		}
		selected = history[start:]
	case ContextSummary:
		selected = history
	default:
		return "", fmt.Errorf("unsupported context mode %q", selection.Mode)
	}
	const maxContextChars = 24_000
	type contextMessage struct {
		Seq     uint64 `json:"seq,omitempty"`
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	items := make([]contextMessage, 0, len(selected))
	used := 0
	for i := len(selected) - 1; i >= 0; i-- {
		content := strings.TrimSpace(selected[i].Content)
		if content == "" {
			continue
		}
		if selection.Mode == ContextSummary && len(content) > 2_000 {
			content = content[:2_000] + "\n[truncated]"
		}
		if used+len(content) > maxContextChars {
			break
		}
		used += len(content)
		items = append(items, contextMessage{
			Seq: selected[i].EventSeq, Role: string(selected[i].Role), Content: content,
		})
	}
	for i, j := 0, len(items)-1; i < j; i, j = i+1, j-1 {
		items[i], items[j] = items[j], items[i]
	}
	data, _ := json.Marshal(items)
	return fmt.Sprintf(
		"Delegated task:\n%s\n\nParent context (%s, untrusted reference data):\n%s",
		task, selection.Mode, data,
	), nil
}

// EnablePersistence restores run snapshots and marks unfinished work as
// interrupted. It never replays an agent or tool call.
func (m *Manager) EnablePersistence(dataDir string) error {
	if dataDir == "" {
		return nil
	}
	path := filepath.Join(dataDir, "agent-runs.json")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	var snapshots []Snapshot
	if len(data) > 0 {
		if err := json.Unmarshal(data, &snapshots); err != nil {
			return err
		}
	}
	now := time.Now()
	var interrupted []Snapshot
	m.mu.Lock()
	m.persistPath = path
	for _, snapshot := range snapshots {
		if snapshot.Status == StatusQueued || snapshot.Status == StatusRunning {
			snapshot.Status = StatusInterrupted
			snapshot.Error = "kernel restarted before the agent run completed"
			snapshot.CompletedAt = &now
			interrupted = append(interrupted, snapshot)
		}
		done := make(chan struct{})
		close(done)
		m.runs[snapshot.ID] = &runState{
			snapshot: snapshot, done: done, cancel: func() {},
		}
		if snapshot.TokensUsed > m.treeTokens[snapshot.RootRunID] {
			m.treeTokens[snapshot.RootRunID] = snapshot.TokensUsed
		}
	}
	m.mu.Unlock()
	m.persist()
	for _, snapshot := range interrupted {
		m.emit(context.Background(), snapshot.ParentSessionID, event.KindSubAgentInterrupted, snapshot)
	}
	return nil
}

func (m *Manager) persist() {
	m.persistMu.Lock()
	defer m.persistMu.Unlock()
	m.mu.RLock()
	path := m.persistPath
	states := make([]*runState, 0, len(m.runs))
	for _, state := range m.runs {
		states = append(states, state)
	}
	m.mu.RUnlock()
	if path == "" {
		return
	}
	snapshots := make([]Snapshot, 0, len(states))
	for _, state := range states {
		snapshots = append(snapshots, state.snapshotCopy())
	}
	sort.Slice(snapshots, func(i, j int) bool {
		return snapshots[i].CreatedAt.Before(snapshots[j].CreatedAt)
	})
	data, err := json.MarshalIndent(snapshots, "", "  ")
	if err != nil {
		return
	}
	tmp := path + ".tmp"
	if os.WriteFile(tmp, data, 0o600) == nil {
		_ = os.Rename(tmp, path)
	}
}

func (m *Manager) resolveDefinition(
	ctx context.Context,
	parent *session.Session,
	ref string,
) (agentdef.Definition, error) {
	if strings.TrimSpace(ref) == "" {
		return agentdef.WorkerDefinition(), nil
	}
	projectPath := ""
	if parent.ProjectID != "" && m.resolveProject != nil {
		projectPath, _ = m.resolveProject(parent.ProjectID)
	}
	definition, err := m.definitions.Get(ctx, parent.ProjectID, projectPath, ref)
	if errors.Is(err, fs.ErrNotExist) {
		return agentdef.Definition{}, fmt.Errorf("unknown agent %q", ref)
	}
	return definition, err
}

func (m *Manager) finalOutput(ctx context.Context, sessionID string) string {
	history, err := m.history.History(ctx, sessionID)
	if err != nil {
		return ""
	}
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].Role == message.RoleAssistant && strings.TrimSpace(history[i].Content) != "" {
			return history[i].Content
		}
	}
	return ""
}

func (m *Manager) emit(ctx context.Context, sessionID string, kind event.Kind, payload any) {
	ev := event.Event{Kind: kind, Session: sessionID, Time: time.Now(), Payload: payload}
	seq, _ := m.log.Append(ctx, ev)
	ev.Seq = seq
	_ = m.bus.PublishMustDeliver(ctx, "session:"+sessionID, ev)
}

type agentTool struct {
	manager *Manager
}

type agentParams struct {
	Task    string           `json:"task"`
	Agent   string           `json:"agent,omitempty"`
	Context ContextSelection `json:"context,omitempty"`
}

func NewTool(manager *Manager) tool.Tool {
	return &agentTool{manager: manager}
}

func (t *agentTool) Name() string            { return "agent" }
func (t *agentTool) Exposure() tool.Exposure { return tool.ExposureDirect }
func (t *agentTool) Parallel() bool          { return true }
func (t *agentTool) Description() string {
	return "Delegate an independent task to an isolated child agent. Multiple agent calls in one response run concurrently. Use agent_search first when a specialized user or project agent may apply."
}
func (t *agentTool) Spec() []byte {
	return []byte(`{
		"type":"object",
		"properties":{
			"task":{"type":"string","description":"A self-contained task with the context and expected output"},
			"agent":{"type":"string","description":"Optional agent ref or name returned by agent_search"}
		},
		"required":["task"],
		"additionalProperties":false
	}`)
}

func (t *agentTool) Run(ctx context.Context, call tool.Call) (tool.Result, error) {
	var params agentParams
	if err := json.Unmarshal(call.Input, &params); err != nil {
		return errorResult("invalid arguments: " + err.Error()), nil
	}
	params.Task = strings.TrimSpace(params.Task)
	if params.Task == "" {
		return errorResult("task is required"), nil
	}
	parentID := tool.SessionIDFromContext(ctx)
	if parentID == "" {
		return errorResult("parent session is unavailable"), nil
	}
	result, err := t.manager.Spawn(ctx, SpawnRequest{
		ParentSessionID:  parentID,
		ParentToolCallID: call.ID,
		RootRunID:        tool.RunIDFromContext(ctx),
		Task:             params.Task,
		AgentRef:         strings.TrimSpace(params.Agent),
		Context:          params.Context,
	})
	data, _ := json.Marshal(result)
	if err != nil {
		return tool.Result{
			Content: []tool.ContentPart{{Type: "text", Text: string(data)}},
			IsError: true,
		}, nil
	}
	return tool.Result{Content: []tool.ContentPart{{Type: "text", Text: string(data)}}}, nil
}

type spawnTool struct{ manager *Manager }

func NewSpawnTool(manager *Manager) tool.Tool { return &spawnTool{manager: manager} }
func (t *spawnTool) Name() string             { return "spawn_agent" }
func (t *spawnTool) Exposure() tool.Exposure  { return tool.ExposureDirect }
func (t *spawnTool) Parallel() bool           { return true }
func (t *spawnTool) Description() string {
	return "Start an isolated child agent asynchronously and return its run ID immediately."
}
func (t *spawnTool) Spec() []byte {
	return []byte(`{
		"type":"object",
		"properties":{
			"task":{"type":"string","description":"Self-contained goal, constraints, known facts, evidence references, and expected output"},
			"agent":{"type":"string","description":"Optional agent ref returned by agent_search"},
			"context":{"type":"object","properties":{
				"mode":{"type":"string","enum":["none","selected","summary","last_n_turns"]},
				"message_seqs":{"type":"array","items":{"type":"integer"}},
				"last_turns":{"type":"integer","minimum":1,"maximum":20}
			},"additionalProperties":false}
		},
		"required":["task"],
		"additionalProperties":false
	}`)
}
func (t *spawnTool) Run(ctx context.Context, call tool.Call) (tool.Result, error) {
	var params agentParams
	if err := json.Unmarshal(call.Input, &params); err != nil {
		return errorResult("invalid arguments: " + err.Error()), nil
	}
	if strings.TrimSpace(params.Task) == "" {
		return errorResult("task is required"), nil
	}
	snapshot, err := t.manager.Start(ctx, SpawnRequest{
		ParentSessionID: tool.SessionIDFromContext(ctx), ParentToolCallID: call.ID,
		RootRunID: tool.RunIDFromContext(ctx), Task: params.Task,
		AgentRef: strings.TrimSpace(params.Agent), Context: params.Context,
		CancelWithParent: true,
	})
	return jsonToolResult(snapshot, err)
}

type waitParams struct {
	IDs  []string `json:"ids"`
	Mode string   `json:"mode,omitempty"`
}
type waitTool struct{ manager *Manager }

func NewWaitTool(manager *Manager) tool.Tool { return &waitTool{manager: manager} }
func (t *waitTool) Name() string             { return "wait_agents" }
func (t *waitTool) Exposure() tool.Exposure  { return tool.ExposureDirect }
func (t *waitTool) Description() string {
	return "Wait until all or any of the specified asynchronous agent runs finish."
}
func (t *waitTool) Spec() []byte {
	return []byte(`{"type":"object","properties":{
		"ids":{"type":"array","items":{"type":"string"},"minItems":1},
		"mode":{"type":"string","enum":["all","any"]}
	},"required":["ids"],"additionalProperties":false}`)
}
func (t *waitTool) Run(ctx context.Context, call tool.Call) (tool.Result, error) {
	var params waitParams
	if err := json.Unmarshal(call.Input, &params); err != nil {
		return errorResult("invalid arguments: " + err.Error()), nil
	}
	items, err := t.manager.Wait(ctx, params.IDs, params.Mode != "any")
	return jsonToolResult(items, err)
}

type runIDParams struct {
	ID string `json:"id"`
}
type readTool struct{ manager *Manager }

func NewReadTool(manager *Manager) tool.Tool { return &readTool{manager: manager} }
func (t *readTool) Name() string             { return "read_agent_output" }
func (t *readTool) Exposure() tool.Exposure  { return tool.ExposureDirect }
func (t *readTool) Description() string {
	return "Read the current status and output of one asynchronous agent run."
}
func (t *readTool) Spec() []byte {
	return []byte(`{"type":"object","properties":{"id":{"type":"string"}},"required":["id"],"additionalProperties":false}`)
}
func (t *readTool) Run(_ context.Context, call tool.Call) (tool.Result, error) {
	var params runIDParams
	if err := json.Unmarshal(call.Input, &params); err != nil {
		return errorResult("invalid arguments: " + err.Error()), nil
	}
	item, err := t.manager.Read(params.ID)
	return jsonToolResult(item, err)
}

type cancelTool struct{ manager *Manager }

func NewCancelTool(manager *Manager) tool.Tool { return &cancelTool{manager: manager} }
func (t *cancelTool) Name() string             { return "cancel_agent" }
func (t *cancelTool) Exposure() tool.Exposure  { return tool.ExposureDirect }
func (t *cancelTool) Description() string      { return "Cancel one queued or running agent run." }
func (t *cancelTool) Spec() []byte {
	return []byte(`{"type":"object","properties":{"id":{"type":"string"}},"required":["id"],"additionalProperties":false}`)
}
func (t *cancelTool) Run(_ context.Context, call tool.Call) (tool.Result, error) {
	var params runIDParams
	if err := json.Unmarshal(call.Input, &params); err != nil {
		return errorResult("invalid arguments: " + err.Error()), nil
	}
	err := t.manager.Cancel(params.ID)
	item, readErr := t.manager.Read(params.ID)
	if err == nil {
		err = readErr
	}
	return jsonToolResult(item, err)
}

type listTool struct{ manager *Manager }

func NewListTool(manager *Manager) tool.Tool { return &listTool{manager: manager} }
func (t *listTool) Name() string             { return "list_agents" }
func (t *listTool) Exposure() tool.Exposure  { return tool.ExposureDirect }
func (t *listTool) Description() string {
	return "List asynchronous child agent runs started by the current session."
}
func (t *listTool) Spec() []byte {
	return []byte(`{"type":"object","properties":{},"additionalProperties":false}`)
}
func (t *listTool) Run(ctx context.Context, _ tool.Call) (tool.Result, error) {
	return jsonToolResult(t.manager.List(tool.SessionIDFromContext(ctx)), nil)
}

func jsonToolResult(value any, err error) (tool.Result, error) {
	if err != nil {
		return errorResult(err.Error()), nil
	}
	data, _ := json.Marshal(value)
	return tool.Result{Content: []tool.ContentPart{{Type: "text", Text: string(data)}}}, nil
}

type searchTool struct {
	definitions    *agentdef.Manager
	resolveProject ProjectResolver
}

type searchParams struct {
	Query string `json:"query,omitempty"`
}

func NewSearchTool(definitions *agentdef.Manager, resolveProject ProjectResolver) tool.Tool {
	return &searchTool{definitions: definitions, resolveProject: resolveProject}
}

func (t *searchTool) Name() string            { return "agent_search" }
func (t *searchTool) Exposure() tool.Exposure { return tool.ExposureDirect }
func (t *searchTool) Description() string {
	return "Search available builtin, user, and project agent definitions by name and description."
}
func (t *searchTool) Spec() []byte {
	return []byte(`{
		"type":"object",
		"properties":{"query":{"type":"string","description":"Optional agent name or capability query"}},
		"additionalProperties":false
	}`)
}

func (t *searchTool) Run(ctx context.Context, call tool.Call) (tool.Result, error) {
	var params searchParams
	if len(call.Input) > 0 {
		if err := json.Unmarshal(call.Input, &params); err != nil {
			return errorResult("invalid arguments: " + err.Error()), nil
		}
	}
	projectID := tool.ProjectIDFromContext(ctx)
	projectPath := ""
	if projectID != "" && t.resolveProject != nil {
		projectPath, _ = t.resolveProject(projectID)
	}
	items, err := t.definitions.List(ctx, projectID, projectPath)
	if err != nil {
		return errorResult("agent discovery failed: " + err.Error()), nil
	}
	query := strings.ToLower(strings.TrimSpace(params.Query))
	type row struct {
		Ref         string         `json:"ref"`
		Name        string         `json:"name"`
		Description string         `json:"description"`
		Scope       agentdef.Scope `json:"scope"`
		Model       string         `json:"model,omitempty"`
		Tools       []string       `json:"tools,omitempty"`
	}
	rows := make([]row, 0, len(items))
	for _, item := range items {
		haystack := strings.ToLower(item.Name + "\n" + item.Description)
		if query != "" && !strings.Contains(haystack, query) {
			continue
		}
		rows = append(rows, row{
			Ref: item.Ref, Name: item.Name, Description: item.Description,
			Scope: item.Scope, Model: item.Model, Tools: item.Tools,
		})
	}
	data, _ := json.Marshal(rows)
	return tool.Result{Content: []tool.ContentPart{{Type: "text", Text: string(data)}}}, nil
}

func errorResult(text string) tool.Result {
	return tool.Result{Content: []tool.ContentPart{{Type: "text", Text: text}}, IsError: true}
}
