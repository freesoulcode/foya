package agent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/freesoulcode/foya/internal/artifact"
	conversation "github.com/freesoulcode/foya/internal/conversation"
	interaction "github.com/freesoulcode/foya/internal/interaction"

	modelapi "github.com/freesoulcode/foya/internal/model"

	"github.com/freesoulcode/foya/internal/skill"

	"github.com/freesoulcode/foya/internal/tool"
)

func (e *Engine) persistentContext(
	sessionID, activity string,
) ([]string, []string, []string) {
	e.mu.RLock()
	resolve := e.contextResolver
	e.mu.RUnlock()
	if resolve == nil {
		return nil, nil, nil
	}
	return resolve(e.resolveProjectID(sessionID), activity)
}

func (e *Engine) skillCatalog(
	ctx context.Context,
	sessionID string,
	selected *skillCatalogEntry,
) []skillCatalogEntry {
	e.mu.RLock()
	manager := e.skills
	e.mu.RUnlock()
	if manager == nil {
		return nil
	}
	items, err := manager.List(ctx, e.resolveProjectID(sessionID), e.resolveProjectPath(sessionID))
	if err != nil {
		return nil
	}
	out := make([]skillCatalogEntry, 0, len(items))
	availableTools := tool.DefaultSkillAvailableTools()
	capabilities := tool.DefaultSkillCapabilities()
	for _, item := range items {
		if !item.Enabled || !skill.IsInvocable(item, availableTools, capabilities) {
			continue
		}
		if selected != nil && item.Ref == selected.Ref {
			continue
		}
		out = append(out, promptSkillCatalogEntry(item))
	}
	return out
}

func (e *Engine) selectedSkillEntry(
	ctx context.Context,
	sessionID, ref string,
) (*skillCatalogEntry, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, nil
	}
	e.mu.RLock()
	manager := e.skills
	e.mu.RUnlock()
	if manager == nil {
		return nil, errors.New("skills are unavailable")
	}
	item, err := manager.Get(
		ctx,
		e.resolveProjectID(sessionID),
		e.resolveProjectPath(sessionID),
		ref,
	)
	if err != nil {
		return nil, fmt.Errorf("load selected skill %q: %w", ref, err)
	}
	if missingTools, missingCapabilities := skill.MissingRequirements(
		item,
		tool.DefaultSkillAvailableTools(),
		tool.DefaultSkillCapabilities(),
	); len(missingTools) > 0 || len(missingCapabilities) > 0 {
		return nil, fmt.Errorf(
			"selected skill %q requirements are not satisfied: missing_tools=%v missing_capabilities=%v",
			item.Name,
			missingTools,
			missingCapabilities,
		)
	}
	entry := promptSkillCatalogEntry(item)
	return &entry, nil
}

func promptSkillCatalogEntry(item skill.Skill) skillCatalogEntry {
	resources := make([]skillResourceEntry, 0, len(item.Resources))
	for _, resource := range item.Resources {
		resources = append(resources, skillResourceEntry{
			Path:      resource.Path,
			MediaType: resource.MediaType,
		})
	}
	return skillCatalogEntry{
		Ref:                  item.Ref,
		Name:                 item.Name,
		Description:          item.Description,
		Scope:                string(item.Scope),
		Pinned:               item.Pinned,
		AllowedTools:         append([]string(nil), item.AllowedTools...),
		RequiredTools:        append([]string(nil), item.RequiredTools...),
		RequiredCapabilities: append([]string(nil), item.RequiredCapabilities...),
		Resources:            resources,
		Body:                 item.Body,
	}
}

// resultText flattens tool output for model input and event payloads.
func resultText(r tool.Result) string {
	var sb string
	for _, p := range r.Content {
		if p.Type == "text" && p.Text != "" {
			if sb != "" {
				sb += "\n"
			}
			sb += p.Text
		} else if p.Type == "image" {
			if sb != "" {
				sb += "\n"
			}
			name := p.Name
			if name == "" {
				name = "image"
			}
			sb += "[Image: " + name + "]"
		} else if p.Type == "artifact_ref" && p.Attachment != nil {
			if sb != "" {
				sb += "\n"
			}
			name := p.Attachment.Name
			if name == "" {
				name = p.Attachment.ID
			}
			sb += "[Generated artifact: " + name + "]"
		}
	}
	if sb == "" {
		sb = "(no output)"
	}
	if r.IsError {
		sb = "Error: " + sb
	}
	return sb
}

func (e *Engine) persistToolArtifacts(
	ctx context.Context,
	sessionID, toolName string,
	result tool.Result,
) ([]conversation.AttachmentRef, error) {
	e.mu.RLock()
	store := e.artifacts
	e.mu.RUnlock()
	var refs []conversation.AttachmentRef
	var cleanupIDs []string
	for index, part := range result.Content {
		if part.Type == "artifact_ref" && part.Attachment != nil {
			refs = append(refs, *part.Attachment)
			if part.Attachment.ID != "" {
				cleanupIDs = append(cleanupIDs, part.Attachment.ID)
			}
			continue
		}
		if part.Type != "image" || len(part.Data) == 0 {
			continue
		}
		if store == nil {
			return nil, errors.New("artifact store is unavailable")
		}
		name := part.Name
		if name == "" {
			name = fmt.Sprintf("%s-image-%d", toolName, index+1)
		}
		ref, err := store.PutImage(ctx, sessionID, name, bytes.NewReader(part.Data))
		if err != nil {
			cleanupToolArtifacts(context.WithoutCancel(ctx), store, sessionID, cleanupIDs)
			return nil, fmt.Errorf("persist tool image %q: %w", name, err)
		}
		refs = append(refs, ref)
		cleanupIDs = append(cleanupIDs, ref.ID)
	}
	if len(refs) == 0 && len(cleanupIDs) == 0 {
		return refs, nil
	}
	ids := make([]string, 0, len(refs)+len(cleanupIDs))
	seen := make(map[string]struct{}, len(refs)+len(cleanupIDs))
	for _, ref := range refs {
		if ref.ID == "" {
			continue
		}
		if _, ok := seen[ref.ID]; ok {
			continue
		}
		seen[ref.ID] = struct{}{}
		ids = append(ids, ref.ID)
	}
	for _, id := range cleanupIDs {
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return refs, nil
	}
	if store == nil {
		cleanupToolArtifacts(context.WithoutCancel(ctx), store, sessionID, cleanupIDs)
		return nil, errors.New("artifact store is unavailable")
	}
	if err := store.Commit(ctx, sessionID, ids); err != nil {
		cleanupToolArtifacts(context.WithoutCancel(ctx), store, sessionID, cleanupIDs)
		return nil, fmt.Errorf("commit tool artifacts: %w", err)
	}
	return refs, nil
}

func cleanupToolArtifacts(ctx context.Context, store artifact.Store, sessionID string, artifactIDs []string) {
	if store == nil || sessionID == "" {
		return
	}
	seen := make(map[string]struct{}, len(artifactIDs))
	for _, id := range artifactIDs {
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		_ = store.Delete(ctx, sessionID, id)
	}
}

func (e *Engine) persistToolImages(
	ctx context.Context,
	sessionID, toolName string,
	result tool.Result,
) []conversation.AttachmentRef {
	refs, _ := e.persistToolArtifacts(ctx, sessionID, toolName, result)
	return refs
}

// resolveProjectPath resolves the stable project identity to its current path.
func (e *Engine) resolveProjectPath(sessionID string) string {
	if s, ok := e.sessions.Get(sessionID); ok && s.ProjectID != "" {
		e.mu.RLock()
		resolve := e.projectResolver
		e.mu.RUnlock()
		if resolve != nil {
			if path, found := resolve(s.ProjectID); found {
				return path
			}
		}
	}
	return ""
}

func (e *Engine) resolveWorkspacePath(ctx context.Context, sessionID, projectPath string) (string, bool, error) {
	if projectPath != "" {
		return projectPath, false, nil
	}
	e.mu.RLock()
	resolve := e.workspaceResolver
	e.mu.RUnlock()
	if resolve == nil {
		return "", false, nil
	}
	path, err := resolve(ctx, sessionID)
	if err != nil {
		return "", false, err
	}
	return path, path != "", nil
}

func (e *Engine) resolveProjectID(sessionID string) string {
	if s, ok := e.sessions.Get(sessionID); ok {
		return s.ProjectID
	}
	return ""
}

func (e *Engine) rootSessionID(sessionID string) string {
	if e.sessions == nil {
		return sessionID
	}
	rootID := sessionID
	for rootID != "" {
		item, ok := e.sessions.Get(rootID)
		if !ok || item.ParentID == "" {
			return rootID
		}
		rootID = item.ParentID
	}
	return sessionID
}

func (e *Engine) resolveApprovalMode(sessionID string) interaction.Mode {
	if s, ok := e.sessions.Get(sessionID); ok && s.ApprovalMode != "" {
		return interaction.Mode(s.ApprovalMode)
	}
	return interaction.ModeManual
}

func (e *Engine) approvalEventSession(sessionID string) string {
	return e.rootSessionID(sessionID)
}

// resolveReasoningEffort returns the Session-level override. The empty value
// deliberately leaves the provider's model default untouched.
func (e *Engine) resolveReasoningEffort(sessionID string) string {
	if s, ok := e.sessions.Get(sessionID); ok {
		return string(s.ReasoningEffort)
	}
	return ""
}

// generateTitle generates a chat title in the background.
func (e *Engine) generateTitle(ctx context.Context, sessionID, userText string) {
	prov := e.currentProvider(sessionID)
	model := e.currentModel(sessionID)
	e.mu.RLock()
	titleResolver := e.titleResolver
	e.mu.RUnlock()
	if titleResolver != nil {
		if fastProvider, fastModel := titleResolver(); fastProvider != nil && fastModel != "" {
			prov, model = fastProvider, fastModel
		}
	}
	var generated string
	if c, ok := prov.(modelapi.Completer); ok {
		generated = generateTitleFromModel(ctx, c, model, e.resolveReasoningEffort(sessionID), userText)
	}
	if generated == "" {
		generated = fallbackTitle(userText)
	}
	ok, err := e.sessions.SetGeneratedTitle(sessionID, generated)
	if err != nil || !ok {
		return
	}
	s, exists := e.sessions.Get(sessionID)
	if !exists {
		return
	}
	e.emit(ctx, sessionID, conversation.KindSessionUpdated, s, true)
}
