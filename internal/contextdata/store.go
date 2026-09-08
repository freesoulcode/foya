// Package contextdata stores user-managed rules and durable memories.
//
// Rules and memories use human-readable Markdown files. The kernel event
// journal contains only change notifications for client synchronization.
package contextdata

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
