package kernel

import (
	"context"
	"errors"

	conversation "github.com/freesoulcode/foya/internal/conversation"
	"github.com/freesoulcode/foya/internal/project"
)

func (b *Service) Projects() ([]project.Project, error) {
	b.mu.RLock()
	manager := b.projects
	b.mu.RUnlock()
	if manager == nil {
		return nil, errors.New("projects are unavailable")
	}
	return manager.List(), nil
}

func (b *Service) RegisterProject(path, name string) (project.Project, error) {
	b.projectMu.Lock()
	defer b.projectMu.Unlock()
	b.mu.RLock()
	manager := b.projects
	b.mu.RUnlock()
	if manager == nil {
		return project.Project{}, errors.New("projects are unavailable")
	}
	return manager.Create(path, name)
}

func (b *Service) Project(id string) (project.Project, error) {
	b.mu.RLock()
	manager := b.projects
	b.mu.RUnlock()
	if manager == nil {
		return project.Project{}, errors.New("projects are unavailable")
	}
	item, ok := manager.Get(id)
	if !ok {
		return project.Project{}, project.ErrNotFound
	}
	return item, nil
}

func (b *Service) UpdateProject(
	id string,
	name *string,
	pinned *bool,
) (project.Project, error) {
	b.projectMu.Lock()
	defer b.projectMu.Unlock()
	b.mu.RLock()
	manager := b.projects
	b.mu.RUnlock()
	if manager == nil {
		return project.Project{}, errors.New("projects are unavailable")
	}
	return manager.Update(id, name, pinned)
}

// DeleteProject removes a project container together with every session bound
// to it. The project directory is never touched; only Foya-owned session data
// (history, artifacts, pending work) is deleted.
func (b *Service) DeleteProject(ctx context.Context, id string) error {
	b.projectMu.Lock()
	defer b.projectMu.Unlock()
	b.mu.RLock()
	manager := b.projects
	b.mu.RUnlock()
	if manager == nil {
		return errors.New("projects are unavailable")
	}
	if _, ok := manager.Get(id); !ok {
		return project.ErrNotFound
	}
	for _, sessionID := range projectSessionRoots(b.sessions.List(), id) {
		if err := b.DeleteSession(ctx, sessionID); err != nil {
			return err
		}
	}
	return manager.Delete(id)
}

// projectSessionRoots returns project-bound sessions which are not descendants
// of another project-bound session. DeleteSession already deletes descendants,
// so this avoids duplicate deletion while preserving unrelated child trees.
func projectSessionRoots(items []*conversation.Session, projectID string) []string {
	byID := make(map[string]*conversation.Session, len(items))
	for _, item := range items {
		byID[item.ID] = item
	}
	roots := make([]string, 0)
	for _, item := range items {
		if item.ProjectID != projectID {
			continue
		}
		parentID := item.ParentID
		hasProjectAncestor := false
		for parentID != "" {
			parent, ok := byID[parentID]
			if !ok {
				break
			}
			if parent.ProjectID == projectID {
				hasProjectAncestor = true
				break
			}
			parentID = parent.ParentID
		}
		if !hasProjectAncestor {
			roots = append(roots, item.ID)
		}
	}
	return roots
}
