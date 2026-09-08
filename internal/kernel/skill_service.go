package kernel

import (
	"context"
	"errors"
	"fmt"
	"strings"

	conversation "github.com/freesoulcode/foya/internal/conversation"
	"github.com/freesoulcode/foya/internal/project"
	"github.com/freesoulcode/foya/internal/skill"
	"github.com/freesoulcode/foya/internal/tool"
)

func (b *Service) Skills(ctx context.Context) ([]skill.Skill, error) {
	b.mu.RLock()
	manager := b.skills
	b.mu.RUnlock()
	if manager == nil {
		return nil, errors.New("skills are unavailable")
	}
	return manager.List(ctx, "", "")
}

func (b *Service) InvocableSkills(ctx context.Context) ([]skill.Skill, error) {
	items, err := b.Skills(ctx)
	if err != nil {
		return nil, err
	}
	return invocableSkills(items), nil
}

func (b *Service) InvocableProjectSkills(ctx context.Context, projectID string) ([]skill.Skill, error) {
	b.mu.RLock()
	skillManager := b.skills
	projectManager := b.projects
	b.mu.RUnlock()
	if skillManager == nil || projectManager == nil {
		return nil, errors.New("project skills are unavailable")
	}
	item, ok := projectManager.Get(projectID)
	if !ok {
		return nil, project.ErrNotFound
	}
	items, err := skillManager.List(ctx, item.ID, item.Path)
	if err != nil {
		return nil, err
	}
	return invocableSkills(items), nil
}

func (b *Service) AllSkills(ctx context.Context) ([]skill.Skill, error) {
	b.mu.RLock()
	manager := b.skills
	b.mu.RUnlock()
	if manager == nil {
		return nil, errors.New("skills are unavailable")
	}
	return manager.ListAll(ctx, "", "")
}

func (b *Service) InspectSkills(ctx context.Context) (skill.ScanResult, error) {
	b.mu.RLock()
	manager := b.skills
	b.mu.RUnlock()
	if manager == nil {
		return skill.ScanResult{}, errors.New("skills are unavailable")
	}
	return manager.Inspect(ctx, "", "")
}

func (b *Service) ProjectSkills(ctx context.Context, projectID string) ([]skill.Skill, error) {
	b.mu.RLock()
	skillManager := b.skills
	projectManager := b.projects
	b.mu.RUnlock()
	if skillManager == nil || projectManager == nil {
		return nil, errors.New("project skills are unavailable")
	}
	item, ok := projectManager.Get(projectID)
	if !ok {
		return nil, project.ErrNotFound
	}
	return skillManager.ListAll(ctx, item.ID, item.Path)
}

func (b *Service) normalizeSelectedSkill(
	ctx context.Context,
	sessionID, ref string,
) (string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", nil
	}
	sessionItem, ok := b.sessions.Get(sessionID)
	if !ok {
		return "", conversation.ErrNotFound
	}
	b.mu.RLock()
	skillManager := b.skills
	projectManager := b.projects
	b.mu.RUnlock()
	if skillManager == nil {
		return "", errors.New("skills are unavailable")
	}
	projectPath := ""
	if sessionItem.ProjectID != "" {
		if projectManager == nil {
			return "", errors.New("projects are unavailable")
		}
		projectItem, exists := projectManager.Get(sessionItem.ProjectID)
		if !exists {
			return "", project.ErrNotFound
		}
		projectPath = projectItem.Path
	}
	item, err := skillManager.Get(
		ctx,
		sessionItem.ProjectID,
		projectPath,
		ref,
	)
	if err != nil {
		return "", fmt.Errorf("selected skill %q is unavailable: %w", ref, err)
	}
	if missingTools, missingCapabilities := skill.MissingRequirements(
		item,
		tool.DefaultSkillAvailableTools(),
		tool.DefaultSkillCapabilities(),
	); len(missingTools) > 0 || len(missingCapabilities) > 0 {
		return "", fmt.Errorf(
			"selected skill %q requirements are not satisfied: missing_tools=%v missing_capabilities=%v",
			item.Name,
			missingTools,
			missingCapabilities,
		)
	}
	return item.Ref, nil
}

func invocableSkills(items []skill.Skill) []skill.Skill {
	availableTools := tool.DefaultSkillAvailableTools()
	capabilities := tool.DefaultSkillCapabilities()
	out := make([]skill.Skill, 0, len(items))
	for _, item := range items {
		if item.Enabled && skill.IsInvocable(item, availableTools, capabilities) {
			out = append(out, item)
		}
	}
	return out
}

func (b *Service) InspectProjectSkills(ctx context.Context, projectID string) (skill.ScanResult, error) {
	b.mu.RLock()
	skillManager := b.skills
	projectManager := b.projects
	b.mu.RUnlock()
	if skillManager == nil || projectManager == nil {
		return skill.ScanResult{}, errors.New("project skills are unavailable")
	}
	item, ok := projectManager.Get(projectID)
	if !ok {
		return skill.ScanResult{}, project.ErrNotFound
	}
	return skillManager.Inspect(ctx, item.ID, item.Path)
}

func (b *Service) SetSkillEnabled(ref string, enabled bool) error {
	b.mu.RLock()
	manager := b.skills
	b.mu.RUnlock()
	if manager == nil {
		return errors.New("skills are unavailable")
	}
	return manager.SetEnabled(ref, enabled)
}

func (b *Service) SetSkillPinned(ref string, pinned bool) error {
	b.mu.RLock()
	manager := b.skills
	b.mu.RUnlock()
	if manager == nil {
		return errors.New("skills are unavailable")
	}
	return manager.SetPinned(ref, pinned)
}
