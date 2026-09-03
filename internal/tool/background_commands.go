package tool

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/freesoulcode/foya/internal/sandbox"
)

var ErrBackgroundCommandNotFound = errors.New("background command not found")

type BackgroundCommandSnapshot struct {
	ID             string    `json:"command_id"`
	SessionID      string    `json:"session_id"`
	Command        string    `json:"command"`
	PID            int       `json:"pid,omitempty"`
	Running        bool      `json:"running"`
	ExitCode       int       `json:"exit_code,omitempty"`
	Stdout         string    `json:"stdout,omitempty"`
	Stderr         string    `json:"stderr,omitempty"`
	StartedAt      time.Time `json:"started_at"`
	BackgroundedBy string    `json:"backgrounded_by,omitempty"`
	StoppedBy      string    `json:"stopped_by,omitempty"`
}

type BackgroundCommandManager interface {
	Start(
		sessionID, command string,
		request sandbox.ExecRequest,
		profile sandbox.Profile,
		timeout time.Duration,
	) (BackgroundCommandSnapshot, error)
	RunForeground(
		ctx context.Context,
		sessionID, toolCallID, command string,
		request sandbox.ExecRequest,
		profile sandbox.Profile,
		timeout time.Duration,
	) (sandbox.ExecResult, BackgroundCommandSnapshot, bool, error)
	Promote(sessionID, toolCallID string) (BackgroundCommandSnapshot, error)
	Reveal(sessionID, toolCallID string) (BackgroundCommandSnapshot, error)
	List(sessionID string) []BackgroundCommandSnapshot
	Get(sessionID, commandID string) (BackgroundCommandSnapshot, error)
	Stop(sessionID, commandID, stoppedBy string) (BackgroundCommandSnapshot, error)
	ClearSession(sessionID string)
}

type backgroundCommand struct {
	mu             sync.RWMutex
	id             string
	sessionID      string
	command        string
	startedAt      time.Time
	process        sandbox.Process
	cancel         context.CancelFunc
	promote        chan struct{}
	backgroundedBy string
	stoppedBy      string
	revealed       bool
}

type backgroundCommandManager struct {
	mu       sync.RWMutex
	runner   sandbox.ManagedRunner
	commands map[string]*backgroundCommand
	active   map[string]*backgroundCommand
	notify   func(BackgroundCommandSnapshot)
}

func NewBackgroundCommandManager(runner sandbox.Runner) *backgroundCommandManager {
	managed, _ := runner.(sandbox.ManagedRunner)
	return &backgroundCommandManager{
		runner:   managed,
		commands: make(map[string]*backgroundCommand),
		active:   make(map[string]*backgroundCommand),
	}
}

func (m *backgroundCommandManager) SetNotifier(notify func(BackgroundCommandSnapshot)) {
	m.mu.Lock()
	m.notify = notify
	m.mu.Unlock()
}

func (m *backgroundCommandManager) notifySnapshot(snapshot BackgroundCommandSnapshot) {
	m.mu.RLock()
	notify := m.notify
	m.mu.RUnlock()
	if notify != nil {
		notify(snapshot)
	}
}

func (m *backgroundCommandManager) watch(item *backgroundCommand) {
	go func() {
		<-item.process.Done()
		m.notifySnapshot(item.snapshot())
	}()
}

func (m *backgroundCommandManager) Start(
	sessionID, command string,
	request sandbox.ExecRequest,
	profile sandbox.Profile,
	timeout time.Duration,
) (BackgroundCommandSnapshot, error) {
	item, err := m.start(sessionID, command, request, profile, timeout, "agent")
	if err != nil {
		return BackgroundCommandSnapshot{}, err
	}
	snapshot := item.snapshot()
	m.notifySnapshot(snapshot)
	m.watch(item)
	return snapshot, nil
}

func (m *backgroundCommandManager) RunForeground(
	ctx context.Context,
	sessionID, toolCallID, command string,
	request sandbox.ExecRequest,
	profile sandbox.Profile,
	timeout time.Duration,
) (sandbox.ExecResult, BackgroundCommandSnapshot, bool, error) {
	item, err := m.start(sessionID, command, request, profile, timeout, "")
	if err != nil {
		return sandbox.ExecResult{}, BackgroundCommandSnapshot{}, false, err
	}
	key := activeCommandKey(sessionID, toolCallID)
	m.mu.Lock()
	m.active[key] = item
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		delete(m.active, key)
		m.mu.Unlock()
	}()

	select {
	case <-item.process.Done():
		result, waitErr := item.process.Wait()
		item.cancel()
		snapshot := item.snapshot()
		m.notifySnapshot(snapshot)
		if snapshot.BackgroundedBy != "" {
			return result, snapshot, true, waitErr
		}
		if !m.finishForeground(key, item) {
			m.remove(item.id)
		}
		return result, snapshot, false, waitErr
	case <-ctx.Done():
		item.cancel()
		_ = item.process.Stop()
		result, _ := item.process.Wait()
		if !m.finishForeground(key, item) {
			m.remove(item.id)
		}
		return result, item.snapshot(), false, context.Cause(ctx)
	case <-item.promote:
		return sandbox.ExecResult{}, item.snapshot(), true, nil
	}
}

func (m *backgroundCommandManager) Promote(
	sessionID, toolCallID string,
) (BackgroundCommandSnapshot, error) {
	key := activeCommandKey(sessionID, toolCallID)
	m.mu.Lock()
	item := m.active[key]
	if item != nil {
		delete(m.active, key)
	}
	m.mu.Unlock()
	if item == nil || !item.process.Snapshot().Running {
		return BackgroundCommandSnapshot{}, ErrBackgroundCommandNotFound
	}
	item.mu.Lock()
	item.backgroundedBy = "user"
	item.mu.Unlock()
	m.notifySnapshot(item.snapshot())
	m.watch(item)
	item.promote <- struct{}{}
	return item.snapshot(), nil
}

func (m *backgroundCommandManager) Reveal(
	sessionID, toolCallID string,
) (BackgroundCommandSnapshot, error) {
	key := activeCommandKey(sessionID, toolCallID)
	m.mu.Lock()
	defer m.mu.Unlock()
	item := m.active[key]
	if item == nil || !item.process.Snapshot().Running {
		return BackgroundCommandSnapshot{}, ErrBackgroundCommandNotFound
	}
	item.mu.Lock()
	item.revealed = true
	item.mu.Unlock()
	return item.snapshot(), nil
}

func (m *backgroundCommandManager) List(sessionID string) []BackgroundCommandSnapshot {
	m.mu.RLock()
	items := make([]*backgroundCommand, 0)
	for _, item := range m.commands {
		if item.sessionID == sessionID {
			items = append(items, item)
		}
	}
	m.mu.RUnlock()
	out := make([]BackgroundCommandSnapshot, 0, len(items))
	for _, item := range items {
		snapshot := item.snapshot()
		if snapshot.Running && snapshot.BackgroundedBy != "" {
			out = append(out, snapshot)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].StartedAt.After(out[j].StartedAt)
	})
	return out
}

func (m *backgroundCommandManager) start(
	sessionID, command string,
	request sandbox.ExecRequest,
	profile sandbox.Profile,
	timeout time.Duration,
	backgroundedBy string,
) (*backgroundCommand, error) {
	if m.runner == nil {
		return nil, errors.New("background execution is unavailable")
	}
	runCtx := context.Background()
	cancel := func() {}
	if timeout > 0 {
		runCtx, cancel = context.WithTimeout(runCtx, timeout)
	} else {
		runCtx, cancel = context.WithCancel(runCtx)
	}
	process, err := m.runner.Start(runCtx, request, profile)
	if err != nil {
		cancel()
		return nil, err
	}
	item := &backgroundCommand{
		id:             newBackgroundCommandID(),
		sessionID:      sessionID,
		command:        command,
		startedAt:      time.Now(),
		process:        process,
		cancel:         cancel,
		promote:        make(chan struct{}, 1),
		backgroundedBy: backgroundedBy,
	}
	m.mu.Lock()
	m.commands[item.id] = item
	m.mu.Unlock()
	return item, nil
}

func (m *backgroundCommandManager) Get(
	sessionID, commandID string,
) (BackgroundCommandSnapshot, error) {
	item, err := m.get(sessionID, commandID)
	if err != nil {
		return BackgroundCommandSnapshot{}, err
	}
	return item.snapshot(), nil
}

func (m *backgroundCommandManager) Stop(
	sessionID, commandID, stoppedBy string,
) (BackgroundCommandSnapshot, error) {
	item, err := m.get(sessionID, commandID)
	if err != nil {
		return BackgroundCommandSnapshot{}, err
	}
	item.mu.Lock()
	item.stoppedBy = stoppedBy
	item.mu.Unlock()
	item.cancel()
	if err := item.process.Stop(); err != nil {
		return BackgroundCommandSnapshot{}, err
	}
	_, _ = item.process.Wait()
	snapshot := item.snapshot()
	m.notifySnapshot(snapshot)
	return snapshot, nil
}

func (m *backgroundCommandManager) ClearSession(sessionID string) {
	m.mu.Lock()
	var items []*backgroundCommand
	for id, item := range m.commands {
		if item.sessionID == sessionID {
			delete(m.commands, id)
			items = append(items, item)
		}
	}
	for key, item := range m.active {
		if item.sessionID == sessionID {
			delete(m.active, key)
		}
	}
	m.mu.Unlock()
	for _, item := range items {
		item.cancel()
		_ = item.process.Stop()
	}
}

func (m *backgroundCommandManager) remove(commandID string) {
	m.mu.Lock()
	delete(m.commands, commandID)
	m.mu.Unlock()
}

func (m *backgroundCommandManager) finishForeground(
	key string,
	item *backgroundCommand,
) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.active, key)
	item.mu.RLock()
	revealed := item.revealed
	item.mu.RUnlock()
	return revealed
}

func (m *backgroundCommandManager) get(
	sessionID, commandID string,
) (*backgroundCommand, error) {
	m.mu.RLock()
	item := m.commands[commandID]
	m.mu.RUnlock()
	if item == nil || item.sessionID != sessionID {
		return nil, ErrBackgroundCommandNotFound
	}
	return item, nil
}

func (c *backgroundCommand) snapshot() BackgroundCommandSnapshot {
	process := c.process.Snapshot()
	c.mu.RLock()
	backgroundedBy := c.backgroundedBy
	stoppedBy := c.stoppedBy
	c.mu.RUnlock()
	return BackgroundCommandSnapshot{
		ID:             c.id,
		SessionID:      c.sessionID,
		Command:        c.command,
		PID:            process.PID,
		Running:        process.Running,
		ExitCode:       process.ExitCode,
		Stdout:         truncateCommandOutput(string(process.Stdout)),
		Stderr:         truncateCommandOutput(string(process.Stderr)),
		StartedAt:      c.startedAt,
		BackgroundedBy: backgroundedBy,
		StoppedBy:      stoppedBy,
	}
}

func truncateCommandOutput(output string) string {
	return truncateToolOutput(output, truncationOptions{
		MaxLines:  defaultMaxOutputLines,
		MaxBytes:  maxOutputLen,
		Direction: keepOutputTail,
	}).Content
}

func newBackgroundCommandID() string {
	var data [8]byte
	_, _ = rand.Read(data[:])
	return "cmd-" + hex.EncodeToString(data[:])
}

func activeCommandKey(sessionID, toolCallID string) string {
	return sessionID + "\x00" + toolCallID
}
