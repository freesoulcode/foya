package kernel

import (
	"context"
	"errors"
	"os"

	"github.com/freesoulcode/foya/internal/config"
	conversation "github.com/freesoulcode/foya/internal/conversation"
	"github.com/freesoulcode/foya/internal/project"
	subagent "github.com/freesoulcode/foya/internal/subagent"
)

func (b *Service) AgentLimits() (config.AgentLimits, error) {
	b.mu.RLock()
	manager := b.subagents
	b.mu.RUnlock()
	if manager == nil {
		return config.AgentLimits{}, errors.New("sub-agents are unavailable")
	}
	limits := manager.Limits()
	return config.AgentLimits{
		MaxGlobalConcurrency: limits.MaxGlobalConcurrency,
		MaxPerRoot:           limits.MaxPerRoot,
		MaxTreeTokens:        limits.MaxTreeTokens,
	}, nil
}

func (b *Service) UpdateAgentLimits(limits config.AgentLimits) error {
	if err := config.ValidateAgentLimits(limits); err != nil {
		return err
	}
	b.mu.RLock()
	manager := b.subagents
	dataDir := b.dataDir
	b.mu.RUnlock()
	if manager == nil {
		return errors.New("sub-agents are unavailable")
	}
	if err := config.SaveAgentLimits(dataDir, limits); err != nil {
		return err
	}
	manager.UpdateLimits(subagent.Limits{
		MaxGlobalConcurrency: limits.MaxGlobalConcurrency,
		MaxPerRoot:           limits.MaxPerRoot,
		MaxTreeTokens:        limits.MaxTreeTokens,
	})
	return nil
}

func (b *Service) AgentRuns(sessionID string) ([]subagent.Snapshot, error) {
	if _, ok := b.sessions.Get(sessionID); !ok {
		return nil, conversation.ErrNotFound
	}
	b.mu.RLock()
	manager := b.subagents
	b.mu.RUnlock()
	if manager == nil {
		return nil, errors.New("sub-agents are unavailable")
	}
	return manager.List(sessionID), nil
}

func (b *Service) AgentRun(sessionID, runID string) (subagent.Snapshot, error) {
	if _, ok := b.sessions.Get(sessionID); !ok {
		return subagent.Snapshot{}, conversation.ErrNotFound
	}
	b.mu.RLock()
	manager := b.subagents
	b.mu.RUnlock()
	if manager == nil {
		return subagent.Snapshot{}, errors.New("sub-agents are unavailable")
	}
	item, err := manager.Read(runID)
	if err == nil && item.ParentSessionID != sessionID {
		return subagent.Snapshot{}, os.ErrNotExist
	}
	return item, err
}

func (b *Service) StartAgent(ctx context.Context, request subagent.SpawnRequest) (subagent.Snapshot, error) {
	b.mu.RLock()
	manager := b.subagents
	b.mu.RUnlock()
	if manager == nil {
		return subagent.Snapshot{}, errors.New("sub-agents are unavailable")
	}
	return manager.Start(ctx, request)
}

func (b *Service) WaitAgents(
	ctx context.Context,
	sessionID string,
	ids []string,
	waitAll bool,
) ([]subagent.Snapshot, error) {
	b.mu.RLock()
	manager := b.subagents
	b.mu.RUnlock()
	if manager == nil {
		return nil, errors.New("sub-agents are unavailable")
	}
	for _, id := range ids {
		item, err := manager.Read(id)
		if err != nil {
			return nil, err
		}
		if item.ParentSessionID != sessionID {
			return nil, os.ErrNotExist
		}
	}
	return manager.Wait(ctx, ids, waitAll)
}

func (b *Service) CancelAgent(sessionID, runID string) error {
	if _, err := b.AgentRun(sessionID, runID); err != nil {
		return err
	}
	b.mu.RLock()
	manager := b.subagents
	b.mu.RUnlock()
	return manager.Cancel(runID)
}

func (b *Service) AgentBudget(sessionID string) (subagent.Budget, error) {
	if _, ok := b.sessions.Get(sessionID); !ok {
		return subagent.Budget{}, conversation.ErrNotFound
	}
	b.mu.RLock()
	manager := b.subagents
	b.mu.RUnlock()
	if manager == nil {
		return subagent.Budget{}, errors.New("sub-agents are unavailable")
	}
	return manager.Budget(sessionID), nil
}

// Agents returns effective builtin and user-level agent definitions.
func (b *Service) Agents(ctx context.Context) ([]subagent.Definition, error) {
	b.mu.RLock()
	manager := b.agents
	b.mu.RUnlock()
	if manager == nil {
		return nil, errors.New("agents are unavailable")
	}
	return manager.List(ctx, "", "")
}

// ProjectAgents returns effective definitions with the project's definitions
// taking precedence over user and builtin scopes.
func (b *Service) ProjectAgents(ctx context.Context, projectID string) ([]subagent.Definition, error) {
	b.mu.RLock()
	agentManager := b.agents
	projectManager := b.projects
	b.mu.RUnlock()
	if agentManager == nil || projectManager == nil {
		return nil, errors.New("project agents are unavailable")
	}
	item, ok := projectManager.Get(projectID)
	if !ok {
		return nil, project.ErrNotFound
	}
	return agentManager.List(ctx, item.ID, item.Path)
}
