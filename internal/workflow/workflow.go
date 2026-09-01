// Package workflow owns the durable state and execution policy for explicit
// user-directed planning workflows.
package workflow

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Kind string

const (
	KindPlan Kind = "plan"
	KindSpec Kind = "spec"
	KindGoal Kind = "goal"
)

type Status string

const (
	StatusActive   Status = "active"
	StatusReady    Status = "ready"
	StatusApproved Status = "approved"
	StatusClosed   Status = "closed"
)

var (
	ErrInvalidKind    = errors.New("invalid workflow kind")
	ErrInvalidGoal    = errors.New("workflow goal is required")
	ErrNotFound       = errors.New("workflow not found")
	ErrInvalidStatus  = errors.New("invalid workflow status")
	ErrNoActiveRecord = errors.New("no active workflow")
)

// Record is a durable workflow projection. Content is the final workflow
// artifact produced by the constrained Agent Loop.
type Record struct {
	ID        string    `json:"id"`
	SessionID string    `json:"session_id"`
	Kind      Kind      `json:"kind"`
	Status    Status    `json:"status"`
	Goal      string    `json:"goal"`
	Content   string    `json:"content,omitempty"`
	Path      string    `json:"path,omitempty"`
	Revision  int64     `json:"revision"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Policy is resolved at every agent step so a workflow can restrict tools
// without changing session metadata or restarting the kernel.
type Policy struct {
	Instructions string
	AllowedTools []string
}

type Manager struct {
	mu      sync.RWMutex
	path    string
	records map[string]Record
}

func NewManager(dataDir string) (*Manager, error) {
	m := &Manager{
		path:    filepath.Join(dataDir, "workflows.json"),
		records: make(map[string]Record),
	}
	data, err := os.ReadFile(m.path)
	if errors.Is(err, os.ErrNotExist) {
		return m, nil
	}
	if err != nil {
		return nil, err
	}
	var records []Record
	if err := json.Unmarshal(data, &records); err != nil {
		return nil, err
	}
	for _, record := range records {
		if validKind(record.Kind) && validStatus(record.Status) && record.ID != "" && record.SessionID != "" {
			m.records[record.ID] = record
		}
	}
	return m, nil
}

func (m *Manager) Start(sessionID string, kind Kind, goal, projectPath string) (Record, error) {
	if !validKind(kind) {
		return Record{}, ErrInvalidKind
	}
	goal = strings.TrimSpace(goal)
	if goal == "" {
		return Record{}, ErrInvalidGoal
	}
	now := time.Now()
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, record := range m.records {
		if record.SessionID == sessionID && (record.Status == StatusActive || record.Status == StatusReady) {
			record.Status = StatusClosed
			record.UpdatedAt = now
			record.Revision++
			m.records[id] = record
		}
	}
	record := Record{
		ID:        newID(),
		SessionID: sessionID,
		Kind:      kind,
		Status:    StatusActive,
		Goal:      goal,
		Revision:  1,
		CreatedAt: now,
		UpdatedAt: now,
	}
	root := filepath.Join(m.path, "..", string(kind)+"s")
	if projectPath != "" {
		root = filepath.Join(projectPath, ".foya", string(kind)+"s")
	}
	record.Path = filepath.Join(root, record.ID+".md")
	if err := writeArtifact(record); err != nil {
		return Record{}, err
	}
	m.records[record.ID] = record
	if err := m.persistLocked(); err != nil {
		delete(m.records, record.ID)
		return Record{}, err
	}
	return record, nil
}

func (m *Manager) Get(id string) (Record, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	record, ok := m.records[id]
	return record, ok
}

func (m *Manager) Active(sessionID string) (Record, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var latest Record
	for _, record := range m.records {
		if record.SessionID != sessionID || (record.Status != StatusActive && record.Status != StatusReady) {
			continue
		}
		if latest.ID == "" || record.UpdatedAt.After(latest.UpdatedAt) {
			latest = record
		}
	}
	return latest, latest.ID != ""
}

func (m *Manager) Update(id string, status Status, content string) (Record, error) {
	if !validStatus(status) || status == StatusClosed {
		return Record{}, ErrInvalidStatus
	}
	content = strings.TrimSpace(content)
	if content == "" {
		return Record{}, fmt.Errorf("workflow content is required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	record, ok := m.records[id]
	if !ok {
		return Record{}, ErrNotFound
	}
	record.Status = status
	record.Content = content
	record.Revision++
	record.UpdatedAt = time.Now()
	m.records[id] = record
	if err := writeArtifact(record); err != nil {
		return Record{}, err
	}
	if err := m.persistLocked(); err != nil {
		return Record{}, err
	}
	return record, nil
}

func (m *Manager) Approve(id string) (Record, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	record, ok := m.records[id]
	if !ok {
		return Record{}, ErrNotFound
	}
	if record.Status != StatusReady {
		return Record{}, ErrInvalidStatus
	}
	record.Status = StatusApproved
	record.Revision++
	record.UpdatedAt = time.Now()
	m.records[id] = record
	if err := writeArtifact(record); err != nil {
		return Record{}, err
	}
	if err := m.persistLocked(); err != nil {
		return Record{}, err
	}
	return record, nil
}

// CompleteActive stores the final assistant response as the workflow artifact.
// It is called by the Agent Loop after a constrained workflow turn completes;
// no user-visible model tool is involved.
func (m *Manager) CompleteActive(sessionID, content string) (Record, bool, error) {
	record, ok := m.Active(sessionID)
	if !ok || record.Status != StatusActive || record.Kind != KindPlan {
		return Record{}, false, nil
	}
	updated, err := m.Update(record.ID, StatusReady, content)
	return updated, true, err
}

func (m *Manager) Policy(sessionID string) (Policy, bool) {
	record, ok := m.Active(sessionID)
	if !ok || record.Kind != KindPlan ||
		(record.Status != StatusActive && record.Status != StatusReady) {
		return Policy{}, false
	}
	return Policy{
		AllowedTools: []string{
			"read", "web_search", "web_fetch", "skill_search", "skill_load", "rule_load", "ask_user",
		},
		Instructions: fmt.Sprintf(`You are in %s workflow mode.
Goal: %s

Explore and reason using only the available read-only tools. Do not edit files, execute shell commands, create subagents, or perform other side effects. Return the complete %s artifact as your final response.`, record.Kind, record.Goal, record.Kind),
	}, true
}

func (m *Manager) persistLocked() error {
	records := make([]Record, 0, len(m.records))
	for _, record := range m.records {
		records = append(records, record)
	}
	data, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(m.path), 0o700); err != nil {
		return err
	}
	tmp := m.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, m.path)
}

func validKind(kind Kind) bool {
	return kind == KindPlan || kind == KindSpec || kind == KindGoal
}

func validStatus(status Status) bool {
	return status == StatusActive || status == StatusReady || status == StatusApproved || status == StatusClosed
}

func newID() string {
	return fmt.Sprintf("wf-%d", time.Now().UnixNano())
}

func writeArtifact(record Record) error {
	if err := os.MkdirAll(filepath.Dir(record.Path), 0o700); err != nil {
		return err
	}
	content := record.Content
	if content == "" {
		content = "正在规划中。"
	}
	data := fmt.Sprintf("---\nkind: %s\nstatus: %s\ngoal: %q\n---\n\n%s\n",
		record.Kind, record.Status, record.Goal, content)
	tmp := record.Path + ".tmp"
	if err := os.WriteFile(tmp, []byte(data), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, record.Path)
}
