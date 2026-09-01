// Package contextdata stores user-managed rules and durable memories.
//
// Rules and memories use human-readable Markdown files. The kernel event
// journal contains only change notifications for client synchronization.
package contextdata

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

type Scope string

const (
	ScopeGlobal  Scope = "global"
	ScopeProject Scope = "project"
)

type RuleTrigger string

const (
	RuleAlways        RuleTrigger = "always"
	RuleGlob          RuleTrigger = "glob"
	RuleModelDecision RuleTrigger = "model_decision"
	RuleManual        RuleTrigger = "manual"
)

var (
	ErrNotFound       = errors.New("context item not found")
	ErrInvalidScope   = errors.New("invalid context scope")
	ErrEmptyContent   = errors.New("context content is required")
	ErrTooLarge       = errors.New("context content is too large")
	ErrInvalidRule    = errors.New("invalid rule configuration")
	ErrMemoryDisabled = errors.New("memory is disabled")
)

const (
	maxRuleRunes   = 6000
	maxMemoryRunes = 2000
)

type Rule struct {
	ID          string      `json:"id"`
	Name        string      `json:"name"`
	Description string      `json:"description,omitempty"`
	Trigger     RuleTrigger `json:"trigger"`
	Globs       []string    `json:"globs,omitempty"`
	Path        string      `json:"path,omitempty"`
	Scope       Scope       `json:"scope"`
	ProjectID   string      `json:"project_id,omitempty"`
	Content     string      `json:"content"`
	CreatedAt   time.Time   `json:"created_at"`
	UpdatedAt   time.Time   `json:"updated_at"`
}

type Memory struct {
	ID        string    `json:"id"`
	Scope     Scope     `json:"scope"`
	ProjectID string    `json:"project_id,omitempty"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type MemorySettings struct {
	Enabled bool `json:"enabled"`
}

type Event struct {
	Seq            uint64          `json:"seq"`
	Kind           string          `json:"kind"`
	Rule           *Rule           `json:"rule,omitempty"`
	Memory         *Memory         `json:"memory,omitempty"`
	MemorySettings *MemorySettings `json:"memory_settings,omitempty"`
	DeletedID      string          `json:"deleted_id,omitempty"`
	Time           time.Time       `json:"time"`
}

type memoryMetadata struct {
	ID        string    `json:"id"`
	Scope     Scope     `json:"scope"`
	ProjectID string    `json:"project_id,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type ruleMetadata struct {
	ID          string      `json:"id"`
	Name        string      `json:"name,omitempty"`
	Description string      `json:"description,omitempty"`
	Trigger     RuleTrigger `json:"trigger,omitempty"`
	Globs       []string    `json:"globs,omitempty"`
	Path        string      `json:"path,omitempty"`
	Scope       Scope       `json:"scope"`
	ProjectID   string      `json:"project_id,omitempty"`
	CreatedAt   time.Time   `json:"created_at"`
	UpdatedAt   time.Time   `json:"updated_at"`
}

type RuleOptions struct {
	Name        string
	Description string
	Trigger     RuleTrigger
	Globs       []string
	Path        string
}

type ruleFrontmatter struct {
	ID          string      `yaml:"id,omitempty"`
	Name        string      `yaml:"name,omitempty"`
	Description string      `yaml:"description,omitempty"`
	Trigger     RuleTrigger `yaml:"trigger,omitempty"`
	Globs       []string    `yaml:"globs,omitempty"`
}

type Store struct {
	mu                 sync.RWMutex
	eventPath          string
	memorySettingsPath string
	memoryRoot         string
	ruleRoot           string
	fallbackProjectDir string
	resolveProjectPath func(string) (string, bool)
	listProjectPaths   func() map[string]string
	seq                uint64
	rules              map[string]Rule
	ruleFiles          map[string]string
	memories           map[string]Memory
	memoryEnabled      bool
	loadedRuleProjects map[string]bool
	loadedProjects     map[string]bool
	events             []Event
	nextSubID          uint64
	subscribers        map[uint64]chan Event
}

func NewStore(dataDir string) (*Store, error) {
	contextDir := filepath.Join(dataDir, "context")
	if err := os.MkdirAll(contextDir, 0o700); err != nil {
		return nil, err
	}
	store := &Store{
		eventPath:          filepath.Join(contextDir, "events.jsonl"),
		memorySettingsPath: filepath.Join(contextDir, "memory-settings.json"),
		memoryRoot:         filepath.Join(dataDir, "memory"),
		ruleRoot:           filepath.Join(dataDir, "rules"),
		fallbackProjectDir: filepath.Join(dataDir, "projects"),
		rules:              make(map[string]Rule),
		ruleFiles:          make(map[string]string),
		memories:           make(map[string]Memory),
		memoryEnabled:      true,
		loadedRuleProjects: make(map[string]bool),
		loadedProjects:     make(map[string]bool),
		subscribers:        make(map[uint64]chan Event),
	}
	if err := store.loadEvents(); err != nil {
		return nil, err
	}
	if err := store.loadMemorySettings(); err != nil {
		return nil, err
	}
	if err := store.loadGlobalRulesLocked(); err != nil {
		return nil, err
	}
	if err := store.loadMemoryFileLocked(ScopeGlobal, ""); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *Store) MemorySettings() MemorySettings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return MemorySettings{Enabled: s.memoryEnabled}
}

func (s *Store) MemoryEnabled() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.memoryEnabled
}

func (s *Store) UpdateMemorySettings(settings MemorySettings) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	previous := s.memoryEnabled
	if previous == settings.Enabled {
		return nil
	}
	s.memoryEnabled = settings.Enabled
	if err := s.writeMemorySettingsLocked(); err != nil {
		s.memoryEnabled = previous
		return err
	}
	now := time.Now()
	ev := Event{
		Seq: s.seq + 1, Kind: "memory_settings_updated",
		MemorySettings: &settings, Time: now,
	}
	if err := s.appendEventLocked(ev, ev); err != nil {
		s.memoryEnabled = previous
		_ = s.writeMemorySettingsLocked()
		return err
	}
	s.publishLocked(ev)
	return nil
}

func (s *Store) loadMemorySettings() error {
	data, err := os.ReadFile(s.memorySettingsPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var settings MemorySettings
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&settings); err != nil {
		return fmt.Errorf("load memory settings: %w", err)
	}
	s.memoryEnabled = settings.Enabled
	return nil
}

func (s *Store) writeMemorySettingsLocked() error {
	data, err := json.MarshalIndent(MemorySettings{Enabled: s.memoryEnabled}, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return writeAtomicFile(s.memorySettingsPath, ".memory-settings-*.tmp", data)
}

// ConfigureFiles installs the user-visible rule and memory roots plus
// project-path resolvers. It is called once before the store is exposed.
func (s *Store) ConfigureFiles(
	memoryRoot, ruleRoot string,
	resolveProjectPath func(string) (string, bool),
	listProjectPaths func() map[string]string,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.memoryRoot = memoryRoot
	s.ruleRoot = ruleRoot
	s.resolveProjectPath = resolveProjectPath
	s.listProjectPaths = listProjectPaths
	s.rules = make(map[string]Rule)
	s.ruleFiles = make(map[string]string)
	s.memories = make(map[string]Memory)
	s.loadedRuleProjects = make(map[string]bool)
	s.loadedProjects = make(map[string]bool)
	if err := s.loadGlobalRulesLocked(); err != nil {
		return err
	}
	if err := s.loadMemoryFileLocked(ScopeGlobal, ""); err != nil {
		return err
	}
	if listProjectPaths != nil {
		for projectID := range listProjectPaths() {
			if err := s.loadProjectRulesLocked(projectID); err != nil {
				return err
			}
			if err := s.loadMemoryFileLocked(ScopeProject, projectID); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Store) ListRules(scope Scope, projectID string) []Rule {
	s.mu.Lock()
	defer s.mu.Unlock()
	if scope == ScopeProject || (scope == "" && projectID != "") {
		_ = s.ensureProjectRulesLoadedLocked(projectID)
	}
	items := make([]Rule, 0, len(s.rules))
	for _, item := range s.rules {
		if matches(item.Scope, item.ProjectID, scope, projectID) {
			items = append(items, item)
		}
	}
	sortRules(items)
	return items
}

func (s *Store) ListMemories(scope Scope, projectID string) []Memory {
	s.mu.Lock()
	defer s.mu.Unlock()
	if scope == ScopeProject || (scope == "" && projectID != "") {
		_ = s.ensureProjectLoadedLocked(projectID)
	}
	items := make([]Memory, 0, 2)
	if scope == "" || scope == ScopeGlobal {
		if item, ok := s.memoryForScopeLocked(ScopeGlobal, ""); ok {
			items = append(items, item)
		}
	}
	if (scope == ScopeProject || scope == "") && projectID != "" {
		if item, ok := s.memoryForScopeLocked(ScopeProject, projectID); ok {
			items = append(items, item)
		}
	}
	sortMemories(items)
	return items
}

func (s *Store) EffectiveRules(projectID string) []Rule {
	return s.ListRules("", projectID)
}

func (s *Store) ActiveRules(projectID, activity string) []Rule {
	items := s.EffectiveRules(projectID)
	active := make([]Rule, 0, len(items))
	for _, item := range items {
		switch item.Trigger {
		case RuleAlways:
			active = append(active, item)
		case RuleGlob:
			if ruleMatchesActivity(item, activity) {
				active = append(active, item)
			}
		case RuleManual:
			if strings.Contains(activity, "@"+item.Name) ||
				strings.Contains(activity, "@"+item.ID) {
				active = append(active, item)
			}
		}
	}
	return active
}

func (s *Store) AvailableRules(projectID string) []Rule {
	items := s.EffectiveRules(projectID)
	available := make([]Rule, 0, len(items))
	for _, item := range items {
		if item.Trigger == RuleModelDecision {
			available = append(available, item)
		}
	}
	return available
}

func (s *Store) GetRule(projectID, ref string) (Rule, error) {
	for _, item := range s.EffectiveRules(projectID) {
		if item.Trigger == RuleModelDecision && (item.ID == ref || item.Name == ref) {
			return item, nil
		}
	}
	return Rule{}, ErrNotFound
}

func (s *Store) EffectiveMemories(projectID string) []Memory {
	if !s.MemoryEnabled() {
		return nil
	}
	return s.ListMemories("", projectID)
}

func ruleMatchesActivity(rule Rule, activity string) bool {
	activity = filepath.ToSlash(activity)
	for _, pattern := range rule.Globs {
		expression := globExpression(filepath.ToSlash(pattern))
		if matched, _ := regexp.MatchString(expression, activity); matched {
			return true
		}
	}
	return false
}

func globExpression(pattern string) string {
	var out strings.Builder
	out.WriteString(`(^|[\s"'=:,(])`)
	for i := 0; i < len(pattern); {
		switch {
		case i+1 < len(pattern) && pattern[i:i+2] == "**":
			out.WriteString(`.*`)
			i += 2
		case pattern[i] == '*':
			out.WriteString(`[^/\s"'=:,)]*`)
			i++
		case pattern[i] == '?':
			out.WriteString(`[^/\s"'=:,)]`)
			i++
		default:
			out.WriteString(regexp.QuoteMeta(pattern[i : i+1]))
			i++
		}
	}
	out.WriteString(`($|[\s"'=:,)])`)
	return out.String()
}

func (s *Store) CreateRule(
	scope Scope,
	projectID, content string,
	options ...RuleOptions,
) (Rule, error) {
	content, err := validate(scope, projectID, content, maxRuleRunes)
	if err != nil {
		return Rule{}, err
	}
	now := time.Now()
	item := Rule{
		ID: newID(), Scope: scope, ProjectID: normalizedProjectID(scope, projectID),
		Content: content, CreatedAt: now, UpdatedAt: now,
	}
	if err := applyRuleOptions(&item, options); err != nil {
		return Rule{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if scope == ScopeProject {
		if err := s.ensureProjectRulesLoadedLocked(projectID); err != nil {
			return Rule{}, err
		}
	}
	s.rules[item.ID] = item
	if err := s.writeRuleLocked(item); err != nil {
		delete(s.rules, item.ID)
		return Rule{}, err
	}
	ev := Event{Seq: s.seq + 1, Kind: "rule_created", Rule: &item, Time: now}
	if err := s.appendRuleEventLocked(ev); err != nil {
		delete(s.rules, item.ID)
		_ = s.deleteRuleFileLocked(item)
		return Rule{}, err
	}
	s.publishLocked(ev)
	return item, nil
}

func (s *Store) UpdateRule(id, content string, options ...RuleOptions) (Rule, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.rules[id]
	if !ok {
		if err := s.loadAllProjectRulesLocked(); err != nil {
			return Rule{}, err
		}
		item, ok = s.rules[id]
	}
	if !ok {
		return Rule{}, ErrNotFound
	}
	cleaned, err := validate(item.Scope, item.ProjectID, content, maxRuleRunes)
	if err != nil {
		return Rule{}, err
	}
	previous := item
	item.Content = cleaned
	if err := applyRuleOptions(&item, options); err != nil {
		return Rule{}, err
	}
	item.UpdatedAt = time.Now()
	s.rules[id] = item
	previousFile := s.ruleFiles[id]
	if err := s.writeRuleLocked(item); err != nil {
		s.rules[id] = previous
		return Rule{}, err
	}
	currentFile := s.ruleFiles[id]
	if item.Scope == ScopeProject && previousFile != "" && previousFile != currentFile {
		if err := os.Remove(previousFile); err != nil && !errors.Is(err, os.ErrNotExist) {
			s.rules[id] = previous
			s.ruleFiles[id] = previousFile
			_ = os.Remove(currentFile)
			return Rule{}, err
		}
	}
	ev := Event{Seq: s.seq + 1, Kind: "rule_updated", Rule: &item, Time: item.UpdatedAt}
	if err := s.appendRuleEventLocked(ev); err != nil {
		s.rules[id] = previous
		if item.Scope == ScopeProject && previousFile != "" && previousFile != currentFile {
			_ = os.Remove(currentFile)
			s.ruleFiles[id] = previousFile
		}
		_ = s.writeRuleLocked(previous)
		return Rule{}, err
	}
	s.publishLocked(ev)
	return item, nil
}

func (s *Store) DeleteRule(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.rules[id]
	if !ok {
		if err := s.loadAllProjectRulesLocked(); err != nil {
			return err
		}
		item, ok = s.rules[id]
	}
	if !ok {
		return ErrNotFound
	}
	delete(s.rules, id)
	if err := s.deleteRuleFileLocked(item); err != nil {
		s.rules[id] = item
		return err
	}
	ev := Event{Seq: s.seq + 1, Kind: "rule_deleted", DeletedID: id, Time: time.Now()}
	if err := s.appendRuleEventLocked(ev); err != nil {
		s.rules[id] = item
		_ = s.writeRuleLocked(item)
		return err
	}
	s.publishLocked(ev)
	return nil
}

func (s *Store) CreateMemory(scope Scope, projectID, content string) (Memory, error) {
	return s.AppendMemory(scope, projectID, content)
}

// AppendMemory adds a durable fact to the single Markdown memory document for
// a scope. It is used by agent-driven and automatic memory capture.
func (s *Store) AppendMemory(scope Scope, projectID, content string) (Memory, error) {
	content, err := validate(scope, projectID, content, maxMemoryRunes)
	if err != nil {
		return Memory{}, err
	}
	projectID = normalizedProjectID(scope, projectID)
	s.mu.Lock()
	defer s.mu.Unlock()
	if scope == ScopeProject {
		if err := s.ensureProjectLoadedLocked(projectID); err != nil {
			return Memory{}, err
		}
	}
	now := time.Now()
	item, exists := s.memoryForScopeLocked(scope, projectID)
	previous := item
	if exists {
		if strings.Contains(item.Content, content) {
			return item, nil
		}
		merged := item.Content + "\n\n" + content
		if utf8.RuneCountInString(merged) > maxMemoryRunes {
			return Memory{}, fmt.Errorf("%w: maximum is %d characters", ErrTooLarge, maxMemoryRunes)
		}
		item.Content = merged
		item.UpdatedAt = now
	} else {
		item = Memory{
			ID: newID(), Scope: scope, ProjectID: projectID, Content: content,
			CreatedAt: now, UpdatedAt: now,
		}
	}
	s.memories[item.ID] = item
	if exists {
		for id, candidate := range s.memories {
			if id != item.ID && candidate.Scope == scope && candidate.ProjectID == projectID {
				delete(s.memories, id)
			}
		}
	}
	if err := s.writeMemoryFileLocked(scope, projectID); err != nil {
		if exists {
			s.memories[item.ID] = previous
		} else {
			delete(s.memories, item.ID)
		}
		return Memory{}, err
	}
	kind := "memory_created"
	if exists {
		kind = "memory_updated"
	}
	ev := Event{Seq: s.seq + 1, Kind: kind, Memory: &item, Time: now}
	if err := s.appendMemoryEventLocked(ev); err != nil {
		if exists {
			s.memories[item.ID] = previous
		} else {
			delete(s.memories, item.ID)
		}
		_ = s.writeMemoryFileLocked(scope, projectID)
		return Memory{}, err
	}
	s.publishLocked(ev)
	return item, nil
}

// SetMemory replaces the complete Markdown document for a scope.
func (s *Store) SetMemory(scope Scope, projectID, content string) (Memory, error) {
	content, err := validate(scope, projectID, content, maxMemoryRunes)
	if err != nil {
		return Memory{}, err
	}
	projectID = normalizedProjectID(scope, projectID)
	s.mu.Lock()
	defer s.mu.Unlock()
	if scope == ScopeProject {
		if err := s.ensureProjectLoadedLocked(projectID); err != nil {
			return Memory{}, err
		}
	}
	now := time.Now()
	item, exists := s.memoryForScopeLocked(scope, projectID)
	if !exists {
		item = Memory{ID: newID(), Scope: scope, ProjectID: projectID, CreatedAt: now}
	}
	previous := item
	item.Content = content
	item.UpdatedAt = now
	s.memories[item.ID] = item
	for id, candidate := range s.memories {
		if id != item.ID && candidate.Scope == scope && candidate.ProjectID == projectID {
			delete(s.memories, id)
		}
	}
	if err := s.writeMemoryFileLocked(scope, projectID); err != nil {
		s.memories[item.ID] = previous
		return Memory{}, err
	}
	kind := "memory_created"
	if exists {
		kind = "memory_updated"
	}
	ev := Event{Seq: s.seq + 1, Kind: kind, Memory: &item, Time: now}
	if err := s.appendMemoryEventLocked(ev); err != nil {
		s.memories[item.ID] = previous
		_ = s.writeMemoryFileLocked(scope, projectID)
		return Memory{}, err
	}
	s.publishLocked(ev)
	return item, nil
}

func (s *Store) UpdateMemory(id, content string) (Memory, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.memories[id]
	if !ok {
		if err := s.loadAllProjectMemoriesLocked(); err != nil {
			return Memory{}, err
		}
		item, ok = s.memories[id]
	}
	if !ok {
		return Memory{}, ErrNotFound
	}
	cleaned, err := validate(item.Scope, item.ProjectID, content, maxMemoryRunes)
	if err != nil {
		return Memory{}, err
	}
	return s.setMemoryLocked(item, cleaned)
}

func (s *Store) setMemoryLocked(item Memory, content string) (Memory, error) {
	previous := item
	item.Content = content
	item.UpdatedAt = time.Now()
	s.memories[item.ID] = item
	for id, candidate := range s.memories {
		if id != item.ID && candidate.Scope == item.Scope && candidate.ProjectID == item.ProjectID {
			delete(s.memories, id)
		}
	}
	if err := s.writeMemoryFileLocked(item.Scope, item.ProjectID); err != nil {
		s.memories[item.ID] = previous
		return Memory{}, err
	}
	ev := Event{Seq: s.seq + 1, Kind: "memory_updated", Memory: &item, Time: item.UpdatedAt}
	if err := s.appendMemoryEventLocked(ev); err != nil {
		s.memories[item.ID] = previous
		_ = s.writeMemoryFileLocked(previous.Scope, previous.ProjectID)
		return Memory{}, err
	}
	s.publishLocked(ev)
	return item, nil
}

func (s *Store) DeleteMemory(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.memories[id]
	if !ok {
		if err := s.loadAllProjectMemoriesLocked(); err != nil {
			return err
		}
		item, ok = s.memories[id]
	}
	if !ok {
		return ErrNotFound
	}
	delete(s.memories, id)
	if err := s.writeMemoryFileLocked(item.Scope, item.ProjectID); err != nil {
		s.memories[id] = item
		return err
	}
	ev := Event{Seq: s.seq + 1, Kind: "memory_deleted", DeletedID: id, Time: time.Now()}
	if err := s.appendMemoryEventLocked(ev); err != nil {
		s.memories[id] = item
		_ = s.writeMemoryFileLocked(item.Scope, item.ProjectID)
		return err
	}
	s.publishLocked(ev)
	return nil
}

func (s *Store) Replay(after uint64) []Event {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]Event, 0)
	for _, ev := range s.events {
		if ev.Seq > after {
			items = append(items, ev)
		}
	}
	return items
}

func (s *Store) Subscribe(ctx context.Context) <-chan Event {
	s.mu.Lock()
	id := s.nextSubID
	s.nextSubID++
	ch := make(chan Event, 64)
	s.subscribers[id] = ch
	s.mu.Unlock()
	go func() {
		<-ctx.Done()
		s.mu.Lock()
		delete(s.subscribers, id)
		close(ch)
		s.mu.Unlock()
	}()
	return ch
}

func (s *Store) appendMemoryEventLocked(ev Event) error {
	diskEvent := ev
	diskEvent.Memory = nil
	if ev.Memory != nil {
		diskEvent.DeletedID = ev.Memory.ID
	}
	return s.appendEventLocked(diskEvent, ev)
}

func (s *Store) appendRuleEventLocked(ev Event) error {
	diskEvent := ev
	diskEvent.Rule = nil
	if ev.Rule != nil {
		diskEvent.DeletedID = ev.Rule.ID
	}
	return s.appendEventLocked(diskEvent, ev)
}

func (s *Store) appendEventLocked(diskEvent, liveEvent Event) error {
	file, err := os.OpenFile(s.eventPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	data, err := json.Marshal(diskEvent)
	if err == nil {
		data = append(data, '\n')
		_, err = file.Write(data)
	}
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	s.seq = liveEvent.Seq
	s.events = append(s.events, liveEvent)
	return nil
}

func (s *Store) publishLocked(ev Event) {
	for _, ch := range s.subscribers {
		select {
		case ch <- ev:
		default:
		}
	}
}

func (s *Store) loadEvents() error {
	file, err := os.Open(s.eventPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer file.Close()
	reader := bufio.NewReader(file)
	for {
		line, readErr := reader.ReadBytes('\n')
		if len(strings.TrimSpace(string(line))) > 0 {
			var ev Event
			if err := json.Unmarshal(line, &ev); err != nil {
				if errors.Is(readErr, io.EOF) {
					break
				}
				return fmt.Errorf("restore context event log: %w", err)
			}
			s.applyEvent(ev)
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return readErr
		}
	}
	return nil
}

func (s *Store) applyEvent(ev Event) {
	if ev.Seq > s.seq {
		s.seq = ev.Seq
	}
	s.events = append(s.events, ev)
}

func (s *Store) ensureProjectRulesLoadedLocked(projectID string) error {
	if projectID == "" || s.loadedRuleProjects[projectID] {
		return nil
	}
	return s.loadProjectRulesLocked(projectID)
}

func (s *Store) loadAllProjectRulesLocked() error {
	if s.listProjectPaths == nil {
		return nil
	}
	for projectID := range s.listProjectPaths() {
		if err := s.ensureProjectRulesLoadedLocked(projectID); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) loadGlobalRulesLocked() error {
	path := filepath.Join(s.ruleRoot, "user_rules.md")
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	items, err := parseRuleMarkdown(data, ScopeGlobal, "", "", path, info.ModTime())
	if err != nil {
		return fmt.Errorf("parse rule file %s: %w", path, err)
	}
	for _, item := range items {
		s.rules[item.ID] = item
		s.ruleFiles[item.ID] = path
	}
	return nil
}

func (s *Store) loadProjectRulesLocked(projectID string) error {
	root, err := s.projectPathLocked(projectID)
	if err != nil {
		return err
	}
	dir := filepath.Join(root, ".foya", "rules")
	if _, err := os.Stat(dir); errors.Is(err, os.ErrNotExist) {
		s.loadedRuleProjects[projectID] = true
		return nil
	} else if err != nil {
		return err
	}
	err = filepath.WalkDir(dir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		depth := ruleDirectoryDepth(relative, entry.IsDir())
		if entry.IsDir() {
			if relative != "." && depth > 3 {
				return filepath.SkipDir
			}
			return nil
		}
		if depth > 3 || strings.ToLower(filepath.Ext(entry.Name())) != ".md" {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rulePath := filepath.ToSlash(relative)
		items, err := parseRuleMarkdown(
			data, ScopeProject, projectID, rulePath, path, info.ModTime(),
		)
		if err != nil {
			return fmt.Errorf("parse rule file %s: %w", path, err)
		}
		if len(items) > 1 {
			return fmt.Errorf("parse rule file %s: project rule files contain one rule each", path)
		}
		for _, item := range items {
			s.rules[item.ID] = item
			s.ruleFiles[item.ID] = path
		}
		return nil
	})
	if err != nil {
		return err
	}
	s.loadedRuleProjects[projectID] = true
	return nil
}

func (s *Store) writeRuleLocked(item Rule) error {
	if item.Scope == ScopeGlobal {
		path := filepath.Join(s.ruleRoot, "user_rules.md")
		items := make([]Rule, 0)
		for _, candidate := range s.rules {
			if candidate.Scope == ScopeGlobal {
				items = append(items, candidate)
			}
		}
		sortRules(items)
		data, err := renderGlobalRulesMarkdown(items)
		if err != nil {
			return err
		}
		if err := writeAtomicFile(path, ".rules-*.tmp", data); err != nil {
			return err
		}
		for _, candidate := range items {
			s.ruleFiles[candidate.ID] = path
		}
		return nil
	}
	root, err := s.projectPathLocked(item.ProjectID)
	if err != nil {
		return err
	}
	relative := item.Path
	if relative == "" {
		relative = item.ID + ".md"
	}
	path := filepath.Join(root, ".foya", "rules", filepath.FromSlash(relative))
	data, err := renderProjectRuleMarkdown(item)
	if err != nil {
		return err
	}
	if err := writeAtomicFile(path, ".rule-*.tmp", data); err != nil {
		return err
	}
	s.ruleFiles[item.ID] = path
	return nil
}

func (s *Store) deleteRuleFileLocked(item Rule) error {
	if item.Scope == ScopeGlobal {
		if err := s.writeRuleLocked(item); err != nil {
			return err
		}
		delete(s.ruleFiles, item.ID)
		return nil
	}
	path := s.ruleFiles[item.ID]
	if path == "" {
		return ErrNotFound
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	delete(s.ruleFiles, item.ID)
	return nil
}

func (s *Store) projectPathLocked(projectID string) (string, error) {
	if s.resolveProjectPath != nil {
		if projectPath, ok := s.resolveProjectPath(projectID); ok {
			return projectPath, nil
		}
		return "", fmt.Errorf("resolve project path for %q", projectID)
	}
	return filepath.Join(s.fallbackProjectDir, projectID), nil
}

func renderGlobalRulesMarkdown(items []Rule) ([]byte, error) {
	var out strings.Builder
	out.WriteString("# Foya User Rules\n")
	for _, item := range items {
		data, err := json.Marshal(ruleMetadata{
			ID: item.ID, Name: item.Name, Description: item.Description,
			Trigger: item.Trigger, Globs: item.Globs, Path: item.Path,
			Scope: item.Scope, ProjectID: item.ProjectID,
			CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt,
		})
		if err != nil {
			return nil, err
		}
		out.WriteString("\n<!-- foya-rule: ")
		out.Write(data)
		out.WriteString(" -->\n")
		out.WriteString(item.Content)
		out.WriteByte('\n')
	}
	return []byte(out.String()), nil
}

func renderProjectRuleMarkdown(item Rule) ([]byte, error) {
	data, err := yaml.Marshal(ruleFrontmatter{
		ID: item.ID, Name: item.Name, Description: item.Description,
		Trigger: item.Trigger, Globs: item.Globs,
	})
	if err != nil {
		return nil, err
	}
	return []byte("---\n" + string(data) + "---\n" + item.Content + "\n"), nil
}

func parseRuleMarkdown(
	data []byte,
	scope Scope,
	projectID, rulePath, sourcePath string,
	modTime time.Time,
) ([]Rule, error) {
	if frontmatter, body, ok := splitFrontmatter(string(data)); ok {
		var meta ruleFrontmatter
		if err := yaml.Unmarshal([]byte(frontmatter), &meta); err != nil {
			return nil, err
		}
		content := strings.TrimSpace(body)
		if content == "" {
			return nil, nil
		}
		id := strings.TrimSpace(meta.ID)
		if id == "" {
			id = stableID(sourcePath)
		}
		item := Rule{
			ID: id, Name: meta.Name, Description: meta.Description,
			Trigger: meta.Trigger, Globs: append([]string(nil), meta.Globs...),
			Path: rulePath, Scope: scope, ProjectID: normalizedProjectID(scope, projectID),
			Content: content, CreatedAt: modTime, UpdatedAt: modTime,
		}
		if err := applyRuleDefaults(&item); err != nil {
			return nil, err
		}
		return []Rule{item}, nil
	}
	const prefix = "<!-- foya-rule: "
	const suffix = " -->"
	lines := strings.Split(string(data), "\n")
	var items []Rule
	var current *ruleMetadata
	var content strings.Builder
	foundMarker := false
	flush := func() {
		if current == nil {
			return
		}
		text := strings.TrimSpace(content.String())
		if text != "" && utf8.RuneCountInString(text) <= maxRuleRunes {
			createdAt := current.CreatedAt
			if createdAt.IsZero() {
				createdAt = modTime
			}
			updatedAt := current.UpdatedAt
			if updatedAt.IsZero() {
				updatedAt = modTime
			}
			items = append(items, Rule{
				ID: current.ID, Name: current.Name, Description: current.Description,
				Trigger: current.Trigger, Globs: append([]string(nil), current.Globs...),
				Path: current.Path, Scope: scope, ProjectID: normalizedProjectID(scope, projectID),
				Content: text, CreatedAt: createdAt, UpdatedAt: updatedAt,
			})
		}
		current = nil
		content.Reset()
	}
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, prefix) && strings.HasSuffix(trimmed, suffix) {
			foundMarker = true
			flush()
			var meta ruleMetadata
			raw := strings.TrimSuffix(strings.TrimPrefix(trimmed, prefix), suffix)
			if err := json.Unmarshal([]byte(raw), &meta); err != nil {
				return nil, err
			}
			if meta.ID == "" {
				return nil, errors.New("rule id is required")
			}
			current = &meta
			continue
		}
		if current != nil {
			content.WriteString(line)
			content.WriteByte('\n')
		}
	}
	flush()
	if foundMarker {
		for i := range items {
			if err := applyRuleDefaults(&items[i]); err != nil {
				return nil, err
			}
		}
		return items, nil
	}
	text := strings.TrimSpace(string(data))
	if strings.HasPrefix(text, "# Foya User Rules") {
		text = strings.TrimSpace(strings.TrimPrefix(text, "# Foya User Rules"))
	}
	if text == "" {
		return nil, nil
	}
	if utf8.RuneCountInString(text) > maxRuleRunes {
		return nil, ErrTooLarge
	}
	items = []Rule{{
		ID: stableID(sourcePath), Path: rulePath,
		Scope: scope, ProjectID: normalizedProjectID(scope, projectID),
		Content: text, CreatedAt: modTime, UpdatedAt: modTime,
	}}
	if err := applyRuleDefaults(&items[0]); err != nil {
		return nil, err
	}
	return items, nil
}

func applyRuleOptions(item *Rule, options []RuleOptions) error {
	if len(options) > 0 {
		option := options[0]
		item.Name = strings.TrimSpace(option.Name)
		item.Description = strings.TrimSpace(option.Description)
		item.Trigger = option.Trigger
		item.Globs = cleanStrings(option.Globs)
		if item.Scope == ScopeProject && strings.TrimSpace(option.Path) != "" {
			path, err := normalizeRulePath(option.Path)
			if err != nil {
				return err
			}
			item.Path = path
		}
	}
	return applyRuleDefaults(item)
}

func applyRuleDefaults(item *Rule) error {
	if item.Name == "" {
		name := strings.TrimSuffix(filepath.Base(item.Path), filepath.Ext(item.Path))
		if name == "" || name == "." {
			name = "rule-" + item.ID[:min(8, len(item.ID))]
		}
		item.Name = name
	}
	if item.Scope == ScopeProject && item.Path == "" {
		item.Path = defaultRulePath(item.Name, item.ID)
	}
	if item.Trigger == "" {
		if item.Scope == ScopeProject && ruleDirectoryDepth(item.Path, false) > 0 {
			item.Trigger = RuleGlob
		} else {
			item.Trigger = RuleAlways
		}
	}
	switch item.Trigger {
	case RuleAlways, RuleManual:
	case RuleModelDecision:
		if item.Description == "" {
			return fmt.Errorf("%w: model_decision requires description", ErrInvalidRule)
		}
	case RuleGlob:
		if len(item.Globs) == 0 {
			dir := filepath.ToSlash(filepath.Dir(item.Path))
			if dir == "." || dir == "" {
				return fmt.Errorf("%w: glob trigger requires globs", ErrInvalidRule)
			}
			item.Globs = []string{dir + "/**"}
		}
	default:
		return fmt.Errorf("%w: unsupported trigger %q", ErrInvalidRule, item.Trigger)
	}
	for _, pattern := range item.Globs {
		if strings.TrimSpace(pattern) == "" {
			return fmt.Errorf("%w: glob cannot be empty", ErrInvalidRule)
		}
	}
	return nil
}

func defaultRulePath(name, id string) string {
	var out strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			out.WriteRune(r)
			lastDash = false
		case r == '-' || r == '_':
			out.WriteRune(r)
			lastDash = r == '-'
		case !lastDash:
			out.WriteByte('-')
			lastDash = true
		}
	}
	slug := strings.Trim(out.String(), "-_")
	if slug == "" {
		slug = "rule-" + id[:min(8, len(id))]
	}
	return slug + ".md"
}

func normalizeRulePath(value string) (string, error) {
	value = filepath.ToSlash(strings.TrimSpace(value))
	value = strings.TrimPrefix(value, "/")
	cleaned := filepath.ToSlash(filepath.Clean(value))
	if cleaned == "." || cleaned == "" || strings.HasPrefix(cleaned, "../") {
		return "", fmt.Errorf("%w: invalid rule path", ErrInvalidRule)
	}
	if strings.ToLower(filepath.Ext(cleaned)) != ".md" {
		cleaned += ".md"
	}
	if ruleDirectoryDepth(cleaned, false) > 3 {
		return "", fmt.Errorf("%w: rule path exceeds three directory levels", ErrInvalidRule)
	}
	return cleaned, nil
}

func ruleDirectoryDepth(relative string, isDir bool) int {
	if relative == "." || relative == "" {
		return 0
	}
	parts := strings.Split(filepath.ToSlash(relative), "/")
	if !isDir {
		parts = parts[:len(parts)-1]
	}
	return len(parts)
}

func splitFrontmatter(source string) (string, string, bool) {
	normalized := strings.ReplaceAll(source, "\r\n", "\n")
	if !strings.HasPrefix(normalized, "---\n") {
		return "", source, false
	}
	end := strings.Index(normalized[4:], "\n---\n")
	if end < 0 {
		return "", source, false
	}
	end += 4
	return normalized[4:end], normalized[end+5:], true
}

func cleanStrings(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if cleaned := strings.TrimSpace(value); cleaned != "" {
			out = append(out, filepath.ToSlash(cleaned))
		}
	}
	return out
}

func (s *Store) ensureProjectLoadedLocked(projectID string) error {
	if projectID == "" || s.loadedProjects[projectID] {
		return nil
	}
	return s.loadMemoryFileLocked(ScopeProject, projectID)
}

// memoryForScopeLocked returns the single document for one scope. Older
// multi-entry files are merged on first access so migration is lossless.
func (s *Store) memoryForScopeLocked(scope Scope, projectID string) (Memory, bool) {
	projectID = normalizedProjectID(scope, projectID)
	var selected Memory
	found := false
	var fragments []string
	for _, item := range s.memories {
		if item.Scope != scope || item.ProjectID != projectID {
			continue
		}
		if !found || item.UpdatedAt.After(selected.UpdatedAt) {
			selected = item
			found = true
		}
		if strings.TrimSpace(item.Content) != "" {
			fragments = append(fragments, item.Content)
		}
	}
	if !found || len(fragments) < 2 {
		return selected, found
	}
	sort.Strings(fragments)
	selected.Content = strings.Join(fragments, "\n\n")
	for id, item := range s.memories {
		if id != selected.ID && item.Scope == scope && item.ProjectID == projectID {
			delete(s.memories, id)
		}
	}
	s.memories[selected.ID] = selected
	return selected, true
}

func (s *Store) loadAllProjectMemoriesLocked() error {
	if s.listProjectPaths == nil {
		return nil
	}
	for projectID := range s.listProjectPaths() {
		if err := s.ensureProjectLoadedLocked(projectID); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) loadMemoryFileLocked(scope Scope, projectID string) error {
	path, err := s.memoryFilePathLocked(scope, projectID)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		if scope == ScopeProject {
			s.loadedProjects[projectID] = true
		}
		return nil
	}
	if err != nil {
		return err
	}
	items, err := parseMemoryMarkdown(data, scope, projectID)
	if err != nil {
		return fmt.Errorf("parse memory file %s: %w", path, err)
	}
	for _, item := range items {
		s.memories[item.ID] = item
	}
	_, _ = s.memoryForScopeLocked(scope, projectID)
	if scope == ScopeProject {
		s.loadedProjects[projectID] = true
	}
	return nil
}

func (s *Store) writeMemoryFileLocked(scope Scope, projectID string) error {
	path, err := s.memoryFilePathLocked(scope, projectID)
	if err != nil {
		return err
	}
	items := make([]Memory, 0, 1)
	if item, ok := s.memoryForScopeLocked(scope, projectID); ok {
		items = append(items, item)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := renderMemoryMarkdown(scope, items)
	if err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".memory-*.tmp")
	if err != nil {
		return err
	}
	tempName := temp.Name()
	defer os.Remove(tempName)
	if err := temp.Chmod(0o600); err == nil {
		_, err = temp.Write(data)
	}
	if err == nil {
		err = temp.Sync()
	}
	if closeErr := temp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(tempName, path)
}

func (s *Store) memoryFilePathLocked(scope Scope, projectID string) (string, error) {
	if scope == ScopeGlobal {
		return filepath.Join(s.memoryRoot, "user_profile.md"), nil
	}
	projectPath := ""
	if s.resolveProjectPath != nil {
		var ok bool
		projectPath, ok = s.resolveProjectPath(projectID)
		if !ok {
			return "", fmt.Errorf("resolve project path for %q", projectID)
		}
	} else {
		projectPath = projectID
	}
	relative, err := projectPathFragment(projectPath)
	if err != nil {
		return "", err
	}
	return filepath.Join(s.memoryRoot, "projects", relative, "project_memory.md"), nil
}

func projectPathFragment(projectPath string) (string, error) {
	cleaned := filepath.Clean(strings.TrimSpace(projectPath))
	if cleaned == "" || cleaned == "." {
		return "", errors.New("project path is required")
	}
	volume := filepath.VolumeName(cleaned)
	cleaned = strings.TrimPrefix(cleaned, volume)
	cleaned = strings.TrimLeft(cleaned, `/\`)
	if cleaned == "" {
		cleaned = "_root"
	}
	parts := strings.FieldsFunc(cleaned, func(r rune) bool { return r == '/' || r == '\\' })
	if volume != "" {
		volumeParts := strings.FieldsFunc(volume, func(r rune) bool {
			return r == '/' || r == '\\' || r == ':'
		})
		parts = append(volumeParts, parts...)
	}
	for i, part := range parts {
		if part == "" || part == "." || part == ".." {
			return "", errors.New("invalid project path")
		}
		parts[i] = strings.ReplaceAll(part, ":", "_")
	}
	return filepath.Join(parts...), nil
}

func writeAtomicFile(path, pattern string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), pattern)
	if err != nil {
		return err
	}
	tempName := temp.Name()
	defer os.Remove(tempName)
	if err := temp.Chmod(0o600); err == nil {
		_, err = temp.Write(data)
	}
	if err == nil {
		err = temp.Sync()
	}
	if closeErr := temp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(tempName, path)
}

func renderMemoryMarkdown(scope Scope, items []Memory) ([]byte, error) {
	var out strings.Builder
	if scope == ScopeGlobal {
		out.WriteString("# Foya User Profile\n")
	} else {
		out.WriteString("# Foya Project Memory\n")
	}
	for _, item := range items {
		meta := memoryMetadata{
			ID: item.ID, Scope: item.Scope, ProjectID: item.ProjectID,
			CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt,
		}
		data, err := json.Marshal(meta)
		if err != nil {
			return nil, err
		}
		out.WriteString("\n<!-- foya-memory: ")
		out.Write(data)
		out.WriteString(" -->\n")
		out.WriteString(item.Content)
		out.WriteByte('\n')
	}
	return []byte(out.String()), nil
}

func parseMemoryMarkdown(data []byte, scope Scope, projectID string) ([]Memory, error) {
	const prefix = "<!-- foya-memory: "
	const suffix = " -->"
	lines := strings.Split(string(data), "\n")
	var items []Memory
	var current *memoryMetadata
	var content strings.Builder
	flush := func() {
		if current == nil {
			return
		}
		text := strings.TrimSpace(content.String())
		if text != "" && utf8.RuneCountInString(text) <= maxMemoryRunes {
			items = append(items, Memory{
				ID: current.ID, Scope: scope, ProjectID: normalizedProjectID(scope, projectID),
				Content: text, CreatedAt: current.CreatedAt, UpdatedAt: current.UpdatedAt,
			})
		}
		current = nil
		content.Reset()
	}
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, prefix) && strings.HasSuffix(trimmed, suffix) {
			flush()
			var meta memoryMetadata
			raw := strings.TrimSuffix(strings.TrimPrefix(trimmed, prefix), suffix)
			if err := json.Unmarshal([]byte(raw), &meta); err != nil {
				return nil, err
			}
			if meta.ID == "" {
				return nil, errors.New("memory id is required")
			}
			current = &meta
			continue
		}
		if current != nil {
			content.WriteString(line)
			content.WriteByte('\n')
		}
	}
	flush()
	return items, nil
}

func validate(scope Scope, projectID, content string, maxRunes int) (string, error) {
	if scope != ScopeGlobal && scope != ScopeProject {
		return "", ErrInvalidScope
	}
	if scope == ScopeProject && strings.TrimSpace(projectID) == "" {
		return "", fmt.Errorf("%w: project_id is required", ErrInvalidScope)
	}
	content = strings.TrimSpace(content)
	if content == "" {
		return "", ErrEmptyContent
	}
	if utf8.RuneCountInString(content) > maxRunes {
		return "", fmt.Errorf("%w: maximum is %d characters", ErrTooLarge, maxRunes)
	}
	return content, nil
}

func normalizedProjectID(scope Scope, projectID string) string {
	if scope == ScopeGlobal {
		return ""
	}
	return strings.TrimSpace(projectID)
}

func matches(itemScope Scope, itemProjectID string, scope Scope, projectID string) bool {
	if scope != "" {
		return itemScope == scope && (scope != ScopeProject || itemProjectID == projectID)
	}
	return itemScope == ScopeGlobal || (projectID != "" && itemProjectID == projectID)
}

func sortRules(items []Rule) {
	sort.Slice(items, func(i, j int) bool {
		if items[i].Scope != items[j].Scope {
			return items[i].Scope == ScopeGlobal
		}
		if !items[i].UpdatedAt.Equal(items[j].UpdatedAt) {
			return items[i].UpdatedAt.Before(items[j].UpdatedAt)
		}
		return items[i].ID < items[j].ID
	})
}

func sortMemories(items []Memory) {
	sort.Slice(items, func(i, j int) bool {
		if items[i].Scope != items[j].Scope {
			return items[i].Scope == ScopeGlobal
		}
		if !items[i].UpdatedAt.Equal(items[j].UpdatedAt) {
			return items[i].UpdatedAt.After(items[j].UpdatedAt)
		}
		return items[i].ID < items[j].ID
	})
}

func newID() string {
	value := make([]byte, 12)
	_, _ = rand.Read(value)
	return hex.EncodeToString(value)
}

func stableID(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:12])
}
