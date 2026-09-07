// Package project owns the application-level catalog of working directories.
package project

import (
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
)

var ErrNotFound = errors.New("project not found")

type Project struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	Path      string     `json:"path"`
	Available bool       `json:"available"`
	Pinned    bool       `json:"pinned,omitempty"`
	PinnedAt  *time.Time `json:"pinned_at,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
}

type storedProject struct {
	ID               string     `json:"id"`
	Name             string     `json:"name"`
	Path             string     `json:"path"`
	Pinned           bool       `json:"pinned,omitempty"`
	PinnedAt         *time.Time `json:"pinned_at,omitempty"`
	LegacyArchivedAt *time.Time `json:"archived_at,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

type Manager struct {
	dataDir  string
	mu       sync.RWMutex
	projects map[string]storedProject
}

func NewManager(dataDir string) (*Manager, error) {
	manager := &Manager{dataDir: dataDir, projects: make(map[string]storedProject)}
	if err := manager.load(); err != nil {
		return nil, err
	}
	return manager, nil
}

func (m *Manager) List() []Project {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Project, 0, len(m.projects))
	for _, item := range m.projects {
		out = append(out, publicProject(item))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Pinned != out[j].Pinned {
			return out[i].Pinned
		}
		if out[i].Pinned && out[i].PinnedAt != nil && out[j].PinnedAt != nil &&
			!out[i].PinnedAt.Equal(*out[j].PinnedAt) {
			return out[i].PinnedAt.After(*out[j].PinnedAt)
		}
		if !out[i].UpdatedAt.Equal(out[j].UpdatedAt) {
			return out[i].UpdatedAt.After(out[j].UpdatedAt)
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out
}

func (m *Manager) Update(id string, name *string, pinned *bool) (Project, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	item, ok := m.projects[id]
	if !ok {
		return Project{}, ErrNotFound
	}
	previous := item
	now := time.Now()
	if name != nil {
		next := strings.TrimSpace(*name)
		if next == "" {
			return Project{}, errors.New("project name is required")
		}
		item.Name = next
	}
	if pinned != nil {
		item.Pinned = *pinned
		if *pinned {
			item.PinnedAt = &now
		} else {
			item.PinnedAt = nil
		}
	}
	item.UpdatedAt = now
	m.projects[id] = item
	if err := m.saveLocked(); err != nil {
		m.projects[id] = previous
		return Project{}, err
	}
	return publicProject(item), nil
}

// Delete permanently removes a project from Foya's catalog. It never touches
// the project directory or any files below it.
func (m *Manager) Delete(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	item, ok := m.projects[id]
	if !ok {
		return ErrNotFound
	}
	delete(m.projects, id)
	if err := m.saveLocked(); err != nil {
		m.projects[id] = item
		return err
	}
	return nil
}

func (m *Manager) Get(id string) (Project, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	item, ok := m.projects[id]
	if !ok {
		return Project{}, false
	}
	return publicProject(item), true
}

func (m *Manager) Create(path, name string) (Project, error) {
	canonical, err := canonicalDirectory(path)
	if err != nil {
		return Project{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	name = strings.TrimSpace(name)
	if name == "" {
		name = "New project"
	}
	item := storedProject{
		ID: newID(), Name: name, Path: canonical,
		CreatedAt: now, UpdatedAt: now,
	}
	m.projects[item.ID] = item
	if err := m.saveLocked(); err != nil {
		delete(m.projects, item.ID)
		return Project{}, err
	}
	return publicProject(item), nil
}

func (m *Manager) path() string {
	return filepath.Join(m.dataDir, "projects.json")
}

func (m *Manager) load() error {
	data, err := os.ReadFile(m.path())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var items []storedProject
	if err := json.Unmarshal(data, &items); err != nil {
		return fmt.Errorf("decode project catalog: %w", err)
	}
	hadArchived := false
	for _, item := range items {
		if item.LegacyArchivedAt != nil {
			hadArchived = true
			continue
		}
		if item.ID == "" || item.Path == "" {
			continue
		}
		m.projects[item.ID] = item
	}
	if hadArchived {
		return m.saveLocked()
	}
	return nil
}

func (m *Manager) saveLocked() error {
	if err := os.MkdirAll(m.dataDir, 0o700); err != nil {
		return err
	}
	items := make([]storedProject, 0, len(m.projects))
	for _, item := range m.projects {
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	data, err := json.MarshalIndent(items, "", "  ")
	if err != nil {
		return err
	}
	temp := m.path() + ".tmp"
	if err := os.WriteFile(temp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(temp, m.path())
}

func canonicalDirectory(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", errors.New("project path is required")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	canonical, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", fmt.Errorf("resolve project path: %w", err)
	}
	info, err := os.Stat(canonical)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", errors.New("project path is not a directory")
	}
	return filepath.Clean(canonical), nil
}

func publicProject(item storedProject) Project {
	info, err := os.Stat(item.Path)
	available := err == nil && info.IsDir()
	return Project{
		ID: item.ID, Name: item.Name, Path: item.Path, Available: available,
		Pinned: item.Pinned, PinnedAt: item.PinnedAt,
		CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt,
	}
}

func newID() string {
	value := make([]byte, 12)
	_, _ = rand.Read(value)
	return hex.EncodeToString(value)
}
