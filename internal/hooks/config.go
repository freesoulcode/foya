package hooks

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Event identifies a lifecycle point at which a configured hook may run.
type Event string

const (
	EventSessionStart      Event = "SessionStart"
	EventSessionEnd        Event = "SessionEnd"
	EventUserPromptSubmit  Event = "UserPromptSubmit"
	EventPreToolUse        Event = "PreToolUse"
	EventPostToolUse       Event = "PostToolUse"
	EventPermissionRequest Event = "PermissionRequest"
	EventSubagentStart     Event = "SubagentStart"
	EventSubagentStop      Event = "SubagentStop"
	EventPreCompact        Event = "PreCompact"
	EventPostCompact       Event = "PostCompact"
	EventStop              Event = "Stop"
	EventTurnComplete      Event = "TurnComplete"
	EventNotification      Event = "Notification"
)

// IsAsync reports whether this event is observational and cannot block the
// agent lifecycle.
func (e Event) IsAsync() bool {
	switch e {
	case EventSessionEnd, EventPostCompact, EventTurnComplete, EventNotification:
		return true
	default:
		return false
	}
}

// SupportsControlEffects reports whether a handler may block or modify the
// agent lifecycle.
func (e Event) SupportsControlEffects() bool {
	return !e.IsAsync()
}

// Config is one user-configured command executed for an Event.
type Config struct {
	ID      string `json:"id,omitempty"`
	Name    string `json:"name,omitempty"`
	Event   Event  `json:"event"`
	Matcher string `json:"matcher,omitempty"`
	Command string `json:"command"`
	Timeout int    `json:"timeout,omitempty"` // seconds; defaults to 30
	Async   bool   `json:"async,omitempty"`
	Enabled *bool  `json:"enabled,omitempty"`
}

// IsEnabled reports whether this hook is active. Missing enabled means true.
func (h Config) IsEnabled() bool {
	return h.Enabled == nil || *h.Enabled
}

// TimeoutDuration returns the configured timeout, defaulting to 30 seconds.
func (h Config) TimeoutDuration() time.Duration {
	if h.Timeout <= 0 {
		return 30 * time.Second
	}
	return time.Duration(h.Timeout) * time.Second
}

// MatchesTool reports whether this hook applies to a tool name. Empty matcher
// applies to every tool.
func (h Config) MatchesTool(toolName string) bool {
	if !h.IsEnabled() || h.Matcher == "" {
		return h.IsEnabled()
	}
	matched, err := regexp.MatchString(h.Matcher, toolName)
	return err == nil && matched
}

var (
	ErrInvalidConfig      = errors.New("invalid hook configuration")
	ErrInvalidEvent       = errors.New("invalid hook event")
	ErrInvalidHook        = errors.New("invalid hook")
	ErrInvalidHooksConfig = errors.New("invalid hooks configuration")
)

const configFileName = "hooks.json"

// GlobalConfigPath returns ~/.foya/hooks.json.
func GlobalConfigPath(homeDir string) string {
	return filepath.Join(homeDir, ".foya", configFileName)
}

// ProjectConfigPath returns <projectDir>/.foya/hooks.json.
func ProjectConfigPath(projectDir string) string {
	return filepath.Join(projectDir, ".foya", configFileName)
}

// Validate normalizes and validates one complete configuration list.
func Validate(items []Config) error {
	for index := range items {
		item := &items[index]
		item.Event = Event(strings.TrimSpace(string(item.Event)))
		item.ID = strings.TrimSpace(item.ID)
		item.Name = strings.TrimSpace(item.Name)
		item.Matcher = strings.TrimSpace(item.Matcher)
		item.Command = strings.TrimSpace(item.Command)

		switch item.Event {
		case EventSessionStart,
			EventSessionEnd,
			EventUserPromptSubmit,
			EventPreToolUse,
			EventPostToolUse,
			EventPermissionRequest,
			EventSubagentStart,
			EventSubagentStop,
			EventPreCompact,
			EventPostCompact,
			EventStop,
			EventTurnComplete,
			EventNotification:
		default:
			return fmt.Errorf("%w: hooks[%d].event %q: %w", ErrInvalidHooksConfig, index, item.Event, ErrInvalidEvent)
		}
		if item.Command == "" {
			return fmt.Errorf("%w: hooks[%d].command is required: %w", ErrInvalidHooksConfig, index, ErrInvalidHook)
		}
		if item.Timeout < 0 || item.Timeout > 10*60 {
			return fmt.Errorf("%w: hooks[%d].timeout must be between 0 and 600 seconds: %w", ErrInvalidHooksConfig, index, ErrInvalidHook)
		}
		if item.Matcher != "" {
			if _, err := regexp.Compile(item.Matcher); err != nil {
				return fmt.Errorf("%w: hooks[%d].matcher: %w", ErrInvalidHooksConfig, index, err)
			}
		}
	}
	return nil
}

// LoadGlobal reads ~/.foya/hooks.json. A missing file returns (nil, false, nil).
func LoadGlobal(homeDir string) ([]Config, bool, error) {
	return loadFile(GlobalConfigPath(homeDir))
}

// LoadProject reads <projectDir>/.foya/hooks.json. A missing file returns
// (nil, false, nil).
func LoadProject(projectDir string) ([]Config, bool, error) {
	return loadFile(ProjectConfigPath(projectDir))
}

// LoadForProject merges global then project hooks. Both scopes remain active;
// a same-ID project hook never silently replaces a global hook.
func LoadForProject(homeDir, projectDir string) ([]Config, error) {
	var global []Config
	if strings.TrimSpace(homeDir) != "" {
		var err error
		global, _, err = LoadGlobal(homeDir)
		if err != nil {
			return nil, fmt.Errorf("load global hooks: %w", err)
		}
	}
	var project []Config
	if strings.TrimSpace(projectDir) != "" {
		var err error
		project, _, err = LoadProject(projectDir)
		if err != nil {
			return nil, fmt.Errorf("load project hooks: %w", err)
		}
	}
	items := make([]Config, 0, len(global)+len(project))
	items = append(items, global...)
	items = append(items, project...)
	return items, nil
}

// SaveGlobal atomically persists ~/.foya/hooks.json.
func SaveGlobal(homeDir string, items []Config) error {
	return saveFile(GlobalConfigPath(homeDir), filepath.Join(homeDir, ".foya"), items)
}

// SaveProject atomically persists <projectDir>/.foya/hooks.json.
func SaveProject(projectDir string, items []Config) error {
	return saveFile(ProjectConfigPath(projectDir), filepath.Join(projectDir, ".foya"), items)
}

func loadFile(path string) ([]Config, bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, false, nil
		}
		return nil, false, err
	}
	var items []Config
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&items); err != nil {
		return nil, false, fmt.Errorf("decode hooks configuration: %w", err)
	}
	if err := Validate(items); err != nil {
		return nil, false, err
	}
	return items, true, nil
}

func saveFile(path, dir string, items []Config) error {
	if err := Validate(items); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(items, "", "  ")
	if err != nil {
		return fmt.Errorf("encode hooks configuration: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
