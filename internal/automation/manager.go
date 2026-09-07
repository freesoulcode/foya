// Package automation manages durable scheduled Agent tasks.
package automation

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/freesoulcode/foya/internal/approval"
	"github.com/freesoulcode/foya/internal/event"
	"github.com/freesoulcode/foya/internal/message"
	"github.com/freesoulcode/foya/internal/question"
	"github.com/freesoulcode/foya/internal/session"
	"github.com/robfig/cron/v3"
)

type RunStatus string

const (
	RunStatusIdle      RunStatus = "idle"
	RunStatusRunning   RunStatus = "running"
	RunStatusCompleted RunStatus = "completed"
	RunStatusFailed    RunStatus = "failed"
	RunStatusCancelled RunStatus = "cancelled"
)

var (
	ErrNotFound       = errors.New("automation not found")
	ErrAlreadyRunning = errors.New("automation is already running")
)

type Task struct {
	ID            string        `json:"id"`
	Name          string        `json:"name"`
	Prompt        string        `json:"prompt"`
	Cron          string        `json:"cron"`
	Timezone      string        `json:"timezone"`
	Enabled       bool          `json:"enabled"`
	ConnectionID  string        `json:"connection_id,omitempty"`
	Model         string        `json:"model,omitempty"`
	ProjectID     string        `json:"project_id,omitempty"`
	ApprovalMode  approval.Mode `json:"approval_mode"`
	LastStatus    RunStatus     `json:"last_status"`
	LastError     string        `json:"last_error,omitempty"`
	LastSessionID string        `json:"last_session_id,omitempty"`
	LastRunAt     *time.Time    `json:"last_run_at,omitempty"`
	NextRunAt     *time.Time    `json:"next_run_at,omitempty"`
	CreatedAt     time.Time     `json:"created_at"`
	UpdatedAt     time.Time     `json:"updated_at"`
}

type Input struct {
	Name         string        `json:"name"`
	Prompt       string        `json:"prompt"`
	Cron         string        `json:"cron"`
	Timezone     string        `json:"timezone,omitempty"`
	Enabled      bool          `json:"enabled"`
	ConnectionID string        `json:"connection_id,omitempty"`
	Model        string        `json:"model,omitempty"`
	ProjectID    string        `json:"project_id,omitempty"`
	ApprovalMode approval.Mode `json:"approval_mode"`
}

type Runtime interface {
	CreateSession(session.CreateOptions) (*session.Session, error)
	RenameSession(context.Context, string, string) (*session.Session, error)
	Subscribe(context.Context, string) <-chan event.Event
	SubmitChatInput(context.Context, string, message.UserInput) error
	CancelQuestions(string, string) error
}

type Manager struct {
	mu sync.RWMutex

	root      context.Context
	runtime   Runtime
	path      string
	scheduler *cron.Cron
	tasks     map[string]Task
	order     []string
	entries   map[string]cron.EntryID
	running   map[string]context.CancelFunc
}

type persistedTasks struct {
	Version int    `json:"version"`
	Tasks   []Task `json:"tasks"`
}

func NewManager(
	ctx context.Context,
	dataDir string,
	runtime Runtime,
	autoStart bool,
) (*Manager, error) {
	if ctx == nil {
		return nil, errors.New("automation manager context is required")
	}
	if runtime == nil {
		return nil, errors.New("automation runtime is required")
	}
	manager := &Manager{
		root:      ctx,
		runtime:   runtime,
		path:      filepath.Join(dataDir, "automations.json"),
		scheduler: cron.New(),
		tasks:     make(map[string]Task),
		entries:   make(map[string]cron.EntryID),
		running:   make(map[string]context.CancelFunc),
	}
	if err := manager.load(); err != nil {
		return nil, err
	}
	for _, id := range manager.order {
		task := manager.tasks[id]
		if task.Enabled {
			if err := manager.scheduleLocked(task); err != nil {
				return nil, err
			}
		}
	}
	if autoStart {
		manager.scheduler.Start()
	}
	return manager, nil
}

func (m *Manager) List() []Task {
	m.mu.RLock()
	defer m.mu.RUnlock()
	items := make([]Task, 0, len(m.order))
	for _, id := range m.order {
		items = append(items, cloneTask(m.tasks[id]))
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Enabled != items[j].Enabled {
			return items[i].Enabled
		}
		if items[i].NextRunAt != nil && items[j].NextRunAt != nil &&
			!items[i].NextRunAt.Equal(*items[j].NextRunAt) {
			return items[i].NextRunAt.Before(*items[j].NextRunAt)
		}
		return items[i].UpdatedAt.After(items[j].UpdatedAt)
	})
	return items
}

func (m *Manager) Get(id string) (Task, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	task, ok := m.tasks[id]
	return cloneTask(task), ok
}

func (m *Manager) Create(input Input) (Task, error) {
	task, schedule, err := taskFromInput("", input)
	if err != nil {
		return Task{}, err
	}
	task.ID, err = newID()
	if err != nil {
		return Task{}, err
	}
	now := time.Now()
	task.CreatedAt = now
	task.UpdatedAt = now
	if task.Enabled {
		next := schedule.Next(now)
		task.NextRunAt = &next
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.tasks[task.ID] = task
	m.order = append(m.order, task.ID)
	if task.Enabled {
		if err := m.scheduleLocked(task); err != nil {
			delete(m.tasks, task.ID)
			m.order = m.order[:len(m.order)-1]
			return Task{}, err
		}
	}
	if err := m.persistLocked(); err != nil {
		m.unscheduleLocked(task.ID)
		delete(m.tasks, task.ID)
		m.order = m.order[:len(m.order)-1]
		return Task{}, err
	}
	return cloneTask(task), nil
}

func (m *Manager) Update(id string, input Input) (Task, error) {
	next, schedule, err := taskFromInput(id, input)
	if err != nil {
		return Task{}, err
	}
	m.mu.Lock()
	current, ok := m.tasks[id]
	if !ok {
		m.mu.Unlock()
		return Task{}, ErrNotFound
	}
	next.CreatedAt = current.CreatedAt
	next.UpdatedAt = time.Now()
	next.LastStatus = current.LastStatus
	next.LastError = current.LastError
	next.LastSessionID = current.LastSessionID
	next.LastRunAt = current.LastRunAt
	if next.Enabled {
		nextRun := schedule.Next(time.Now())
		next.NextRunAt = &nextRun
	}
	m.unscheduleLocked(id)
	m.tasks[id] = next
	if next.Enabled {
		if err := m.scheduleLocked(next); err != nil {
			m.tasks[id] = current
			_ = m.scheduleLocked(current)
			m.mu.Unlock()
			return Task{}, err
		}
	}
	if err := m.persistLocked(); err != nil {
		m.unscheduleLocked(id)
		m.tasks[id] = current
		_ = m.scheduleLocked(current)
		m.mu.Unlock()
		return Task{}, err
	}
	cancel := m.running[id]
	m.mu.Unlock()
	if !next.Enabled && cancel != nil {
		cancel()
	}
	return cloneTask(next), nil
}

func (m *Manager) Delete(id string) error {
	m.mu.Lock()
	task, ok := m.tasks[id]
	if !ok {
		m.mu.Unlock()
		return ErrNotFound
	}
	m.unscheduleLocked(id)
	delete(m.tasks, id)
	for index, taskID := range m.order {
		if taskID == id {
			m.order = append(m.order[:index], m.order[index+1:]...)
			break
		}
	}
	if err := m.persistLocked(); err != nil {
		m.tasks[id] = task
		m.order = append(m.order, id)
		_ = m.scheduleLocked(task)
		m.mu.Unlock()
		return err
	}
	cancel := m.running[id]
	delete(m.running, id)
	m.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return nil
}

func (m *Manager) RunNow(id string) (Task, error) {
	return m.trigger(id)
}

func (m *Manager) Close() {
	stopCtx := m.scheduler.Stop()
	<-stopCtx.Done()
	m.mu.Lock()
	cancels := make([]context.CancelFunc, 0, len(m.running))
	for _, cancel := range m.running {
		cancels = append(cancels, cancel)
	}
	m.running = make(map[string]context.CancelFunc)
	m.mu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
}

func (m *Manager) trigger(id string) (Task, error) {
	m.mu.Lock()
	task, ok := m.tasks[id]
	if !ok {
		m.mu.Unlock()
		return Task{}, ErrNotFound
	}
	if _, running := m.running[id]; running {
		m.mu.Unlock()
		return cloneTask(task), ErrAlreadyRunning
	}
	runCtx, cancel := context.WithCancel(m.root)
	m.running[id] = cancel
	now := time.Now()
	task.LastRunAt = &now
	task.LastStatus = RunStatusRunning
	task.LastError = ""
	task.NextRunAt = m.nextRun(task, now)
	task.UpdatedAt = now
	m.tasks[id] = task
	_ = m.persistLocked()
	m.mu.Unlock()

	go m.execute(runCtx, task)
	return cloneTask(task), nil
}

func (m *Manager) execute(ctx context.Context, task Task) {
	sessionItem, err := m.runtime.CreateSession(session.CreateOptions{
		ConnectionID: task.ConnectionID,
		Model:        task.Model,
		ProjectID:    task.ProjectID,
		ApprovalMode: string(task.ApprovalMode),
	})
	if err != nil {
		m.finish(task.ID, "", RunStatusFailed, err)
		return
	}
	_, _ = m.runtime.RenameSession(context.WithoutCancel(ctx), sessionItem.ID, "Automation: "+task.Name)
	events := m.runtime.Subscribe(ctx, sessionItem.ID)
	if err := m.runtime.SubmitChatInput(ctx, sessionItem.ID, message.UserInput{Text: task.Prompt}); err != nil {
		m.finish(task.ID, sessionItem.ID, RunStatusFailed, err)
		return
	}

	var runErr error
	for {
		select {
		case <-ctx.Done():
			m.finish(task.ID, sessionItem.ID, RunStatusCancelled, ctx.Err())
			return
		case item, ok := <-events:
			if !ok {
				m.finish(task.ID, sessionItem.ID, RunStatusFailed, errors.New("automation event stream closed"))
				return
			}
			switch item.Kind {
			case event.KindQuestionRequested:
				if batch, ok := item.Payload.(question.Batch); ok {
					_ = m.runtime.CancelQuestions(sessionItem.ID, batch.ID)
				}
			case event.KindError:
				runErr = fmt.Errorf("%v", item.Payload)
			case event.KindTurnComplete:
				if runErr != nil {
					m.finish(task.ID, sessionItem.ID, RunStatusFailed, runErr)
				} else {
					m.finish(task.ID, sessionItem.ID, RunStatusCompleted, nil)
				}
				return
			}
		}
	}
}

func (m *Manager) finish(id, sessionID string, status RunStatus, runErr error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	task, ok := m.tasks[id]
	if !ok {
		return
	}
	delete(m.running, id)
	task.LastSessionID = sessionID
	task.LastStatus = status
	task.LastError = ""
	if runErr != nil && !errors.Is(runErr, context.Canceled) {
		task.LastError = runErr.Error()
	}
	task.NextRunAt = m.nextRun(task, time.Now())
	task.UpdatedAt = time.Now()
	m.tasks[id] = task
	_ = m.persistLocked()
}

func (m *Manager) scheduleLocked(task Task) error {
	if !task.Enabled {
		return nil
	}
	entryID, err := m.scheduler.AddFunc(taskSpec(task), func() {
		_, _ = m.trigger(task.ID)
	})
	if err != nil {
		return err
	}
	m.entries[task.ID] = entryID
	return nil
}

func (m *Manager) unscheduleLocked(id string) {
	if entryID, ok := m.entries[id]; ok {
		m.scheduler.Remove(entryID)
		delete(m.entries, id)
	}
}

func (m *Manager) nextRun(task Task, after time.Time) *time.Time {
	if !task.Enabled {
		return nil
	}
	schedule, err := cron.ParseStandard(taskSpec(task))
	if err != nil {
		return nil
	}
	next := schedule.Next(after)
	return &next
}

func (m *Manager) load() error {
	data, err := os.ReadFile(m.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var persisted persistedTasks
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&persisted); err != nil {
		return err
	}
	if persisted.Version != 1 {
		return errors.New("unsupported automation store version")
	}
	for _, task := range persisted.Tasks {
		normalized, schedule, err := taskFromInput(task.ID, Input{
			Name:         task.Name,
			Prompt:       task.Prompt,
			Cron:         task.Cron,
			Timezone:     task.Timezone,
			Enabled:      task.Enabled,
			ConnectionID: task.ConnectionID,
			Model:        task.Model,
			ProjectID:    task.ProjectID,
			ApprovalMode: task.ApprovalMode,
		})
		if err != nil || normalized.ID == "" {
			return fmt.Errorf("invalid automation %q: %w", task.ID, err)
		}
		normalized.LastStatus = task.LastStatus
		normalized.LastError = task.LastError
		normalized.LastSessionID = task.LastSessionID
		normalized.LastRunAt = task.LastRunAt
		normalized.CreatedAt = task.CreatedAt
		normalized.UpdatedAt = task.UpdatedAt
		if normalized.Enabled {
			next := schedule.Next(time.Now())
			normalized.NextRunAt = &next
		}
		m.tasks[normalized.ID] = normalized
		m.order = append(m.order, normalized.ID)
	}
	return nil
}

func (m *Manager) persistLocked() error {
	items := make([]Task, 0, len(m.order))
	for _, id := range m.order {
		items = append(items, m.tasks[id])
	}
	data, err := json.MarshalIndent(persistedTasks{Version: 1, Tasks: items}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(m.path), 0o700); err != nil {
		return err
	}
	temp := m.path + ".tmp"
	if err := os.WriteFile(temp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(temp, m.path)
}

func taskFromInput(id string, input Input) (Task, cron.Schedule, error) {
	input.Name = strings.TrimSpace(input.Name)
	input.Prompt = strings.TrimSpace(input.Prompt)
	input.Cron = strings.TrimSpace(input.Cron)
	input.Timezone = strings.TrimSpace(input.Timezone)
	input.ConnectionID = strings.TrimSpace(input.ConnectionID)
	input.Model = strings.TrimSpace(input.Model)
	input.ProjectID = strings.TrimSpace(input.ProjectID)
	if input.Name == "" {
		return Task{}, nil, errors.New("automation name is required")
	}
	if input.Prompt == "" {
		return Task{}, nil, errors.New("automation prompt is required")
	}
	if input.Cron == "" {
		return Task{}, nil, errors.New("automation cron expression is required")
	}
	if input.Timezone == "" {
		input.Timezone = time.Local.String()
	}
	if _, err := time.LoadLocation(input.Timezone); err != nil {
		return Task{}, nil, fmt.Errorf("invalid automation timezone: %w", err)
	}
	if input.ApprovalMode == "" {
		input.ApprovalMode = approval.ModeAuto
	}
	if input.ApprovalMode != approval.ModeAuto && input.ApprovalMode != approval.ModeFullAccess {
		return Task{}, nil, errors.New("automation approval mode must be auto or full_access")
	}
	spec := "CRON_TZ=" + input.Timezone + " " + input.Cron
	schedule, err := cron.ParseStandard(spec)
	if err != nil {
		return Task{}, nil, fmt.Errorf("invalid automation schedule: %w", err)
	}
	return Task{
		ID:           id,
		Name:         input.Name,
		Prompt:       input.Prompt,
		Cron:         input.Cron,
		Timezone:     input.Timezone,
		Enabled:      input.Enabled,
		ConnectionID: input.ConnectionID,
		Model:        input.Model,
		ProjectID:    input.ProjectID,
		ApprovalMode: input.ApprovalMode,
		LastStatus:   RunStatusIdle,
	}, schedule, nil
}

func taskSpec(task Task) string {
	return "CRON_TZ=" + task.Timezone + " " + task.Cron
}

func cloneTask(task Task) Task {
	if task.LastRunAt != nil {
		value := *task.LastRunAt
		task.LastRunAt = &value
	}
	if task.NextRunAt != nil {
		value := *task.NextRunAt
		task.NextRunAt = &value
	}
	return task
}

func newID() (string, error) {
	raw := make([]byte, 8)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return "automation-" + hex.EncodeToString(raw), nil
}
