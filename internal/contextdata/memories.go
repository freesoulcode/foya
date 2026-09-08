package contextdata

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

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
