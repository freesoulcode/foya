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
	"unicode"

	"github.com/freesoulcode/foya/internal/session"
)

type Kind string

const (
	KindPlan Kind = "plan"
	KindSpec Kind = "spec"
	KindGoal Kind = "goal"
)

type Status string

const (
	StatusActive    Status = "active"
	StatusReady     Status = "ready"
	StatusApproved  Status = "approved"
	StatusCompleted Status = "completed"
	StatusClosed    Status = "closed"
)

const (
	maxSpecDocumentRunes = 120_000
	maxSpecTasks         = 100
	maxSpecTaskRunes     = 200
)

var (
	ErrInvalidKind    = errors.New("invalid workflow kind")
	ErrInvalidGoal    = errors.New("workflow goal is required")
	ErrNotFound       = errors.New("workflow not found")
	ErrInvalidStatus  = errors.New("invalid workflow status")
	ErrNoActiveRecord = errors.New("no active workflow")
	ErrSpecIncomplete = errors.New("spec workflow did not submit its documents")
)

type SpecArtifacts struct {
	Spec      string `json:"spec"`
	Tasks     string `json:"tasks"`
	Checklist string `json:"checklist"`
}

type SpecDocuments struct {
	Title     string `json:"title"`
	Spec      string `json:"spec"`
	Tasks     string `json:"tasks"`
	Checklist string `json:"checklist"`
}

// Record is a durable workflow projection. Content is the final workflow
// artifact produced by the constrained Agent Loop.
type Record struct {
	ID           string         `json:"id"`
	SessionID    string         `json:"session_id"`
	Kind         Kind           `json:"kind"`
	Status       Status         `json:"status"`
	Goal         string         `json:"goal"`
	Title        string         `json:"title,omitempty"`
	Content      string         `json:"content,omitempty"`
	Path         string         `json:"path,omitempty"`
	ArtifactRoot string         `json:"artifact_root,omitempty"`
	Artifacts    *SpecArtifacts `json:"artifacts,omitempty"`
	Revision     int64          `json:"revision"`
	CreatedAt    time.Time      `json:"created_at"`
	UpdatedAt    time.Time      `json:"updated_at"`
}

// Policy is resolved at every agent step so a workflow can restrict tools
// without changing session metadata or restarting the kernel.
type Policy struct {
	Instructions string
	AllowedTools []string
	ExtraTools   []string
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
		if record.SessionID == sessionID &&
			(record.Status == StatusActive ||
				record.Status == StatusReady ||
				record.Status == StatusApproved) {
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
	root := filepath.Join(filepath.Dir(m.path), string(kind)+"s")
	if projectPath != "" {
		root = filepath.Join(projectPath, ".foya", string(kind)+"s")
	}
	if kind == KindSpec {
		record.ArtifactRoot = root
	} else {
		record.Path = filepath.Join(root, record.ID+".md")
	}
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
	if !ok {
		return Record{}, false, nil
	}
	if record.Kind == KindSpec {
		if record.Status != StatusReady {
			return Record{}, false, ErrSpecIncomplete
		}
		return record, true, nil
	}
	if record.Status != StatusActive {
		return Record{}, false, nil
	}
	updated, err := m.Update(record.ID, StatusReady, content)
	return updated, true, err
}

func (m *Manager) Policy(sessionID string) (Policy, bool) {
	record, ok := m.Active(sessionID)
	if !ok || (record.Status != StatusActive && record.Status != StatusReady) {
		return Policy{}, false
	}
	allowedTools := []string{
		"read", "web_search", "web_fetch", "skill_search", "skill_load", "skill_read_resource", "rule_load", "ask_user",
	}
	if record.Kind == KindSpec {
		location := "Create a concise title for the work and pass it as the title field when submitting the documents. " +
			"The title is used to create a readable folder under " + record.ArtifactRoot + "."
		if record.Artifacts != nil {
			location = fmt.Sprintf(`Update the existing documents at:
- %s
- %s
- %s`, record.Artifacts.Spec, record.Artifacts.Tasks, record.Artifacts.Checklist)
		}
		return Policy{
			AllowedTools: append(allowedTools, SubmitSpecToolName),
			ExtraTools:   []string{SubmitSpecToolName},
			Instructions: fmt.Sprintf(`You are creating or revising a specification for this request:
%s

Work in read-only mode. Inspect relevant project context and ask the user only for decisions that materially affect scope or acceptance criteria. Do not modify source files or run commands.

Produce all three reviewable Markdown documents:
- spec.md: context, goals, non-goals, user scenarios, functional and non-functional requirements, edge cases, assumptions, and measurable acceptance criteria.
- tasks.md: an ordered implementation checklist using "- [ ]" items. Each task must be concrete, independently verifiable, and include relevant file paths when known.
- checklist.md: an acceptance checklist using "- [ ]" items that verifies the specification and completed implementation.

Call %s with a concise title and the complete contents of all three documents. This is the only tool allowed to write the Spec artifacts. If revising an existing Spec, read the current files first and submit the complete updated versions.

%s

After the tool succeeds, briefly summarize the documents and stop. Tell the user they may provide more changes or confirm execution. Do not implement the specification until the user explicitly approves it.`,
				record.Goal,
				SubmitSpecToolName,
				location,
			),
		}, true
	}
	if record.Kind != KindPlan {
		return Policy{}, false
	}
	return Policy{
		AllowedTools: allowedTools,
		Instructions: fmt.Sprintf(`You are in %s workflow mode.
Goal: %s

Explore and reason using only the available read-only tools. Do not edit files, execute shell commands, create subagents, or perform other side effects. Return the complete %s artifact as your final response.`, record.Kind, record.Goal, record.Kind),
	}, true
}

func (m *Manager) SubmitSpec(sessionID string, documents SpecDocuments) (Record, error) {
	documents.Title = strings.TrimSpace(documents.Title)
	documents.Spec = strings.TrimSpace(documents.Spec)
	documents.Tasks = strings.TrimSpace(documents.Tasks)
	documents.Checklist = strings.TrimSpace(documents.Checklist)
	if _, err := validateSpecDocuments(documents); err != nil {
		return Record{}, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	record, ok := m.activeLocked(sessionID)
	if !ok || record.Kind != KindSpec {
		return Record{}, ErrNoActiveRecord
	}
	if record.Artifacts == nil {
		dir, err := uniqueDirectory(record.ArtifactRoot, slugify(documents.Title))
		if err != nil {
			return Record{}, err
		}
		record.Title = documents.Title
		record.Artifacts = &SpecArtifacts{
			Spec:      filepath.Join(dir, "spec.md"),
			Tasks:     filepath.Join(dir, "tasks.md"),
			Checklist: filepath.Join(dir, "checklist.md"),
		}
	}
	record.Path = record.Artifacts.Spec
	if err := writeSpecDocuments(*record.Artifacts, documents); err != nil {
		return Record{}, err
	}
	record.Status = StatusReady
	record.Content = documents.Spec
	record.Revision++
	record.UpdatedAt = time.Now()
	previous := m.records[record.ID]
	m.records[record.ID] = record
	if err := m.persistLocked(); err != nil {
		m.records[record.ID] = previous
		return Record{}, err
	}
	return record, nil
}

func validateSpecDocuments(documents SpecDocuments) ([]session.Task, error) {
	if documents.Title == "" || documents.Spec == "" ||
		documents.Tasks == "" || documents.Checklist == "" {
		return nil, errors.New("title, spec, tasks, and checklist documents are required")
	}
	if len([]rune(documents.Title)) > 80 {
		return nil, errors.New("spec title must be 80 characters or fewer")
	}
	for name, content := range map[string]string{
		"spec": documents.Spec, "tasks": documents.Tasks, "checklist": documents.Checklist,
	} {
		if len([]rune(content)) > maxSpecDocumentRunes {
			return nil, fmt.Errorf("%s document exceeds %d characters", name, maxSpecDocumentRunes)
		}
	}
	tasks, err := parseSpecTaskItems(documents.Tasks)
	if err != nil {
		return nil, err
	}
	if !hasChecklistItems(documents.Checklist) {
		return nil, errors.New("checklist document must contain at least one Markdown checklist item")
	}
	return tasks, nil
}

func (m *Manager) SpecTaskItems(id string) ([]session.Task, error) {
	record, ok := m.Get(id)
	if !ok {
		return nil, ErrNotFound
	}
	if record.Kind != KindSpec {
		return nil, ErrInvalidKind
	}
	artifacts := ensureSpecArtifacts(record)
	specData, err := os.ReadFile(artifacts.Spec)
	if err != nil {
		return nil, err
	}
	tasksData, err := os.ReadFile(artifacts.Tasks)
	if err != nil {
		return nil, err
	}
	checklistData, err := os.ReadFile(artifacts.Checklist)
	if err != nil {
		return nil, err
	}
	return validateSpecDocuments(SpecDocuments{
		Title:     record.Title,
		Spec:      strings.TrimSpace(string(specData)),
		Tasks:     strings.TrimSpace(string(tasksData)),
		Checklist: strings.TrimSpace(string(checklistData)),
	})
}

func parseSpecTaskItems(content string) ([]session.Task, error) {
	items := make([]session.Task, 0)
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		status := session.TaskStatusPending
		switch {
		case strings.HasPrefix(line, "- [ ] "):
			line = strings.TrimSpace(strings.TrimPrefix(line, "- [ ] "))
		case strings.HasPrefix(line, "- [x] "):
			line = strings.TrimSpace(strings.TrimPrefix(line, "- [x] "))
			status = session.TaskStatusCompleted
		case strings.HasPrefix(line, "- [X] "):
			line = strings.TrimSpace(strings.TrimPrefix(line, "- [X] "))
			status = session.TaskStatusCompleted
		default:
			continue
		}
		if line != "" {
			if len([]rune(line)) > maxSpecTaskRunes {
				return nil, fmt.Errorf("spec task must be %d characters or fewer", maxSpecTaskRunes)
			}
			items = append(items, session.Task{Content: line, Status: status})
		}
	}
	if len(items) == 0 {
		return nil, errors.New("spec tasks document contains no checklist items")
	}
	if len(items) > maxSpecTasks {
		return nil, fmt.Errorf("spec tasks document contains more than %d tasks", maxSpecTasks)
	}
	return items, nil
}

func (m *Manager) SyncSpecTaskProgress(
	sessionID string,
	tasks []session.Task,
) (Record, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var current Record
	for _, record := range m.records {
		if record.SessionID != sessionID ||
			record.Kind != KindSpec ||
			record.Status != StatusApproved {
			continue
		}
		if current.ID == "" || record.UpdatedAt.After(current.UpdatedAt) {
			current = record
		}
	}
	if current.ID == "" {
		return Record{}, false, nil
	}
	artifacts := ensureSpecArtifacts(current)
	data, err := os.ReadFile(artifacts.Tasks)
	if err != nil {
		return Record{}, false, err
	}
	next, changed := syncTaskCheckboxes(string(data), tasks)
	allCompleted := len(tasks) > 0
	for _, task := range tasks {
		if task.Status != session.TaskStatusCompleted {
			allCompleted = false
			break
		}
	}
	statusChanged := allCompleted && current.Status != StatusCompleted
	if !changed && !statusChanged {
		return current, false, nil
	}
	if changed {
		if err := atomicWrite(artifacts.Tasks, next); err != nil {
			return Record{}, false, err
		}
	}
	if statusChanged {
		current.Status = StatusCompleted
	}
	current.Revision++
	current.UpdatedAt = time.Now()
	previous := m.records[current.ID]
	m.records[current.ID] = current
	if err := m.persistLocked(); err != nil {
		m.records[current.ID] = previous
		return Record{}, false, err
	}
	return current, true, nil
}

func (m *Manager) activeLocked(sessionID string) (Record, bool) {
	var latest Record
	for _, record := range m.records {
		if record.SessionID != sessionID ||
			(record.Status != StatusActive && record.Status != StatusReady) {
			continue
		}
		if latest.ID == "" || record.UpdatedAt.After(latest.UpdatedAt) {
			latest = record
		}
	}
	return latest, latest.ID != ""
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
	return status == StatusActive || status == StatusReady || status == StatusApproved ||
		status == StatusCompleted || status == StatusClosed
}

func newID() string {
	return fmt.Sprintf("wf-%d", time.Now().UnixNano())
}

func writeArtifact(record Record) error {
	if record.Kind == KindSpec {
		root := record.ArtifactRoot
		if record.Artifacts != nil {
			root = filepath.Dir(record.Artifacts.Spec)
		}
		if root == "" {
			return errors.New("spec artifact root is required")
		}
		return os.MkdirAll(root, 0o700)
	}
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

func ensureSpecArtifacts(record Record) *SpecArtifacts {
	if record.Artifacts != nil {
		copy := *record.Artifacts
		return &copy
	}
	root := record.ArtifactRoot
	if root == "" {
		root = strings.TrimSuffix(record.Path, filepath.Ext(record.Path))
		if filepath.Base(root) == record.ID {
			root = filepath.Dir(record.Path)
		}
	}
	name := record.Title
	if name == "" {
		name = record.Goal
	}
	root = filepath.Join(root, slugify(name))
	return &SpecArtifacts{
		Spec:      filepath.Join(root, "spec.md"),
		Tasks:     filepath.Join(root, "tasks.md"),
		Checklist: filepath.Join(root, "checklist.md"),
	}
}

func writeSpecDocuments(paths SpecArtifacts, documents SpecDocuments) error {
	if err := os.MkdirAll(filepath.Dir(paths.Spec), 0o700); err != nil {
		return err
	}
	for path, content := range map[string]string{
		paths.Spec:      documents.Spec,
		paths.Tasks:     documents.Tasks,
		paths.Checklist: documents.Checklist,
	} {
		if err := atomicWrite(path, content+"\n"); err != nil {
			return err
		}
	}
	return nil
}

func atomicWrite(path, content string) error {
	file, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmp := file.Name()
	defer os.Remove(tmp)
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return err
	}
	if _, err := file.WriteString(content); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func hasChecklistItems(content string) bool {
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "- [ ] ") ||
			strings.HasPrefix(line, "- [x] ") ||
			strings.HasPrefix(line, "- [X] ") {
			return true
		}
	}
	return false
}

func syncTaskCheckboxes(content string, tasks []session.Task) (string, bool) {
	statuses := make(map[string]session.TaskStatus, len(tasks))
	for _, task := range tasks {
		statuses[taskKey(task.Content)] = task.Status
	}
	lines := strings.Split(content, "\n")
	changed := false
	for index, line := range lines {
		leading := len(line) - len(strings.TrimLeft(line, " \t"))
		trimmed := line[leading:]
		if len(trimmed) < 7 ||
			(!strings.HasPrefix(trimmed, "- [ ] ") &&
				!strings.HasPrefix(trimmed, "- [x] ") &&
				!strings.HasPrefix(trimmed, "- [X] ")) {
			continue
		}
		status, ok := statuses[taskKey(trimmed[6:])]
		if !ok {
			continue
		}
		marker := byte(' ')
		if status == session.TaskStatusCompleted {
			marker = 'x'
		}
		markerIndex := leading + 3
		if lines[index][markerIndex] == marker {
			continue
		}
		updated := []byte(lines[index])
		updated[markerIndex] = marker
		lines[index] = string(updated)
		changed = true
	}
	return strings.Join(lines, "\n"), changed
}

func taskKey(content string) string {
	normalized := strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(content)), " "))
	fields := strings.Fields(normalized)
	if len(fields) == 0 {
		return ""
	}
	id := strings.Trim(fields[0], "*_`[]():.-")
	if len(id) > 1 && id[0] == 't' {
		valid := true
		for _, r := range id[1:] {
			if r < '0' || r > '9' {
				valid = false
				break
			}
		}
		if valid {
			return id
		}
	}
	return normalized
}

func uniqueDirectory(root, slug string) (string, error) {
	candidate := filepath.Join(root, slug)
	for suffix := 2; ; suffix++ {
		if _, err := os.Stat(candidate); errors.Is(err, os.ErrNotExist) {
			return candidate, nil
		} else if err != nil {
			return "", err
		}
		candidate = filepath.Join(root, fmt.Sprintf("%s-%d", slug, suffix))
	}
}

func slugify(value string) string {
	var out strings.Builder
	lastSeparator := false
	for _, r := range strings.ToLower(strings.TrimSpace(value)) {
		switch {
		case unicode.IsLetter(r) || unicode.IsNumber(r):
			out.WriteRune(r)
			lastSeparator = false
		case !lastSeparator && out.Len() > 0:
			out.WriteByte('-')
			lastSeparator = true
		}
		if out.Len() >= 48 {
			break
		}
	}
	slug := strings.Trim(out.String(), "-")
	if slug == "" {
		return "spec-" + time.Now().Format("20060102-150405")
	}
	return slug
}
