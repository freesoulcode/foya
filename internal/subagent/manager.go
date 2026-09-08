package subagent

import (
	"context"
	"crypto/rand"
	"encoding/hex"

	"errors"
	"fmt"
	"io/fs"

	"sort"
	"strings"

	"time"

	conversation "github.com/freesoulcode/foya/internal/conversation"

	model "github.com/freesoulcode/foya/internal/model"

	"go.opentelemetry.io/otel/trace"
)

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
	m.emit(context.WithoutCancel(ctx), request.ParentSessionID, conversation.KindSubAgentQueued, state.snapshotCopy())

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
	m.emit(context.WithoutCancel(ctx), request.ParentSessionID, conversation.KindSubAgentRunning, state.snapshotCopy())

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
	kind := conversation.KindSubAgentCompleted
	switch status {
	case StatusFailed:
		kind = conversation.KindSubAgentFailed
	case StatusCancelled:
		kind = conversation.KindSubAgentCancelled
	case StatusInterrupted:
		kind = conversation.KindSubAgentInterrupted
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
func (m *Manager) ObserveUsage(sessionID string, usage model.Usage) {
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
	m.emit(context.Background(), budget.RootSessionID, conversation.KindAgentBudgetUpdated, budget)
	m.persist()
	if budget.Exceeded {
		m.emit(context.Background(), budget.RootSessionID, conversation.KindAgentBudgetExceeded, budget)
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
