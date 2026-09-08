package kernel

import (
	"context"
	"errors"

	"github.com/freesoulcode/foya/internal/contextdata"
	"github.com/freesoulcode/foya/internal/hooks"
	interaction "github.com/freesoulcode/foya/internal/interaction"
	"github.com/freesoulcode/foya/internal/tool"
	workflow "github.com/freesoulcode/foya/internal/workflow"
)

func (b *Service) SetContextStore(store *contextdata.Store) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.context = store
}

func (b *Service) SetCommandManager(manager *workflow.CommandManager) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.commands = manager
}

func (b *Service) SetWorkflowManager(manager *workflow.Manager) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.workflows = manager
}

// SetQuestionGateway attaches the human-input coordinator assembled by kernel.
func (b *Service) SetQuestionGateway(gateway interaction.QuestionGateway) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.questions = gateway
}

func (b *Service) SetBackgroundCommandManager(manager tool.BackgroundCommandManager) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.backgroundCommands = manager
}

// SetHooksHomeDir configures the user-level hook root. The Hook runtime itself
// reloads hook files for each lifecycle event, so settings updates take effect
// without restarting the kernel.
func (b *Service) SetHooksHomeDir(homeDir string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.homeDir = homeDir
}

// Hooks returns effective hooks for a global or project scope.
func (b *Service) Hooks(scope, projectID string) ([]hooks.Config, error) {
	b.mu.RLock()
	homeDir := b.homeDir
	b.mu.RUnlock()
	switch scope {
	case "global":
		items, _, err := hooks.LoadGlobal(homeDir)
		return items, err
	case "project":
		if projectID == "" {
			return nil, contextdata.ErrInvalidScope
		}
		item, err := b.Project(projectID)
		if err != nil {
			return nil, err
		}
		items, _, err := hooks.LoadProject(item.Path)
		return items, err
	default:
		return nil, contextdata.ErrInvalidScope
	}
}

// ReplaceHooks atomically replaces the hook file for one scope.
func (b *Service) ReplaceHooks(scope, projectID string, items []hooks.Config) error {
	b.mu.RLock()
	homeDir := b.homeDir
	b.mu.RUnlock()
	switch scope {
	case "global":
		return hooks.SaveGlobal(homeDir, items)
	case "project":
		if projectID == "" {
			return contextdata.ErrInvalidScope
		}
		item, err := b.Project(projectID)
		if err != nil {
			return err
		}
		return hooks.SaveProject(item.Path, items)
	default:
		return contextdata.ErrInvalidScope
	}
}

// SetMemoryMaintenanceWake installs the low-cost wake signal for automatic
// memory maintenance. The callback must never block session creation.
func (b *Service) SetMemoryMaintenanceWake(wake func()) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.memoryWake = wake
}

func (b *Service) MemorySettings() (contextdata.MemorySettings, error) {
	store, err := b.contextStore()
	if err != nil {
		return contextdata.MemorySettings{}, err
	}
	return store.MemorySettings(), nil
}

func (b *Service) UpdateMemorySettings(
	settings contextdata.MemorySettings,
) (contextdata.MemorySettings, error) {
	store, err := b.contextStore()
	if err != nil {
		return contextdata.MemorySettings{}, err
	}
	if err := store.UpdateMemorySettings(settings); err != nil {
		return contextdata.MemorySettings{}, err
	}
	if settings.Enabled {
		b.mu.RLock()
		wake := b.memoryWake
		b.mu.RUnlock()
		if wake != nil {
			wake()
		}
	}
	return store.MemorySettings(), nil
}

func (b *Service) Rules(scope contextdata.Scope, projectID string) ([]contextdata.Rule, error) {
	store, err := b.contextStore()
	if err != nil {
		return nil, err
	}
	if err := b.validateContextScope(scope, projectID); err != nil {
		return nil, err
	}
	return store.ListRules(scope, projectID), nil
}

func (b *Service) CreateRule(
	scope contextdata.Scope,
	projectID, content string,
	options ...contextdata.RuleOptions,
) (contextdata.Rule, error) {
	store, err := b.contextStore()
	if err != nil {
		return contextdata.Rule{}, err
	}
	if err := b.validateContextScope(scope, projectID); err != nil {
		return contextdata.Rule{}, err
	}
	return store.CreateRule(scope, projectID, content, options...)
}

func (b *Service) UpdateRule(
	id, content string,
	options ...contextdata.RuleOptions,
) (contextdata.Rule, error) {
	store, err := b.contextStore()
	if err != nil {
		return contextdata.Rule{}, err
	}
	return store.UpdateRule(id, content, options...)
}

func (b *Service) DeleteRule(id string) error {
	store, err := b.contextStore()
	if err != nil {
		return err
	}
	return store.DeleteRule(id)
}

func (b *Service) Memories(scope contextdata.Scope, projectID string) ([]contextdata.Memory, error) {
	store, err := b.contextStore()
	if err != nil {
		return nil, err
	}
	if err := b.validateContextScope(scope, projectID); err != nil {
		return nil, err
	}
	return store.ListMemories(scope, projectID), nil
}

func (b *Service) CreateMemory(
	scope contextdata.Scope,
	projectID, content string,
) (contextdata.Memory, error) {
	store, err := b.contextStore()
	if err != nil {
		return contextdata.Memory{}, err
	}
	if err := b.validateContextScope(scope, projectID); err != nil {
		return contextdata.Memory{}, err
	}
	return store.AppendMemory(scope, projectID, content)
}

// SetMemory replaces the one Markdown memory document for a scope.
func (b *Service) SetMemory(
	scope contextdata.Scope,
	projectID, content string,
) (contextdata.Memory, error) {
	store, err := b.contextStore()
	if err != nil {
		return contextdata.Memory{}, err
	}
	if err := b.validateContextScope(scope, projectID); err != nil {
		return contextdata.Memory{}, err
	}
	return store.SetMemory(scope, projectID, content)
}

// Memory returns the one document for the requested scope.
func (b *Service) Memory(scope contextdata.Scope, projectID string) (contextdata.Memory, error) {
	store, err := b.contextStore()
	if err != nil {
		return contextdata.Memory{}, err
	}
	if err := b.validateContextScope(scope, projectID); err != nil {
		return contextdata.Memory{}, err
	}
	items := store.ListMemories(scope, projectID)
	if len(items) == 0 {
		return contextdata.Memory{}, contextdata.ErrNotFound
	}
	return items[0], nil
}

// ClearMemory removes the complete document for a scope.
func (b *Service) ClearMemory(scope contextdata.Scope, projectID string) error {
	item, err := b.Memory(scope, projectID)
	if errors.Is(err, contextdata.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	store, err := b.contextStore()
	if err != nil {
		return err
	}
	return store.DeleteMemory(item.ID)
}

func (b *Service) UpdateMemory(id, content string) (contextdata.Memory, error) {
	store, err := b.contextStore()
	if err != nil {
		return contextdata.Memory{}, err
	}
	return store.UpdateMemory(id, content)
}

func (b *Service) DeleteMemory(id string) error {
	store, err := b.contextStore()
	if err != nil {
		return err
	}
	return store.DeleteMemory(id)
}

func (b *Service) ReplayContext(after uint64) ([]contextdata.Event, error) {
	store, err := b.contextStore()
	if err != nil {
		return nil, err
	}
	return store.Replay(after), nil
}

func (b *Service) SubscribeContext(ctx context.Context) (<-chan contextdata.Event, error) {
	store, err := b.contextStore()
	if err != nil {
		return nil, err
	}
	return store.Subscribe(ctx), nil
}

func (b *Service) contextStore() (*contextdata.Store, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if b.context == nil {
		return nil, errors.New("rules and memory are unavailable")
	}
	return b.context, nil
}

func (b *Service) validateContextScope(scope contextdata.Scope, projectID string) error {
	if scope != contextdata.ScopeGlobal && scope != contextdata.ScopeProject {
		return contextdata.ErrInvalidScope
	}
	if scope == contextdata.ScopeProject {
		if projectID == "" {
			return contextdata.ErrInvalidScope
		}
		if _, err := b.Project(projectID); err != nil {
			return err
		}
	}
	return nil
}
