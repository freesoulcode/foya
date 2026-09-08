package plugin

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

func rejectUnknown(values map[string]json.RawMessage, allowed ...string) error {
	set := make(map[string]bool, len(allowed))
	for _, key := range allowed {
		set[key] = true
	}
	for key := range values {
		if !set[key] {
			return fmt.Errorf("unknown field %q", key)
		}
	}
	return nil
}

func (m *Manager) statePath() string { return filepath.Join(m.dataDir, "plugins.json") }

func (m *Manager) loadState() error {
	data, err := os.ReadFile(m.statePath())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var state persistedState
	if err := json.Unmarshal(data, &state); err != nil {
		return fmt.Errorf("decode plugin state: %w", err)
	}
	for _, name := range state.Disabled {
		if validPluginName(name) {
			m.disabled[name] = true
		}
	}
	for name, source := range state.Sources {
		if validPluginName(name) {
			m.sources[name] = source
		}
	}
	return nil
}

func (m *Manager) saveStateLocked() error {
	disabled := make([]string, 0, len(m.disabled))
	for name := range m.disabled {
		disabled = append(disabled, name)
	}
	sort.Strings(disabled)
	data, err := json.MarshalIndent(persistedState{
		Disabled: disabled,
		Sources:  cloneStringMap(m.sources),
	}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(m.dataDir, 0o700); err != nil {
		return err
	}
	temp := m.statePath() + ".tmp"
	if err := os.WriteFile(temp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(temp, m.statePath()); err != nil {
		_ = os.Remove(temp)
		return err
	}
	return nil
}
