// Package backend 是传输无关的业务层:管理多连接、多会话、事件扇出、
// 实例级鉴权。它不关心底层是 Unix socket 还是 TCP,server 层把请求
// 转成对 Backend 的调用。
//
// 这是「一个内核多客户端」的落地关键:session 归 Backend 所有,多个
// 客户端连接可订阅同一 session,Backend 负责把事件扇出给所有订阅者。
package backend

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/freesoulcode/foya/internal/agent"
	"github.com/freesoulcode/foya/internal/agentdef"
	"github.com/freesoulcode/foya/internal/approval"
	"github.com/freesoulcode/foya/internal/artifact"
	"github.com/freesoulcode/foya/internal/broker"
	"github.com/freesoulcode/foya/internal/browseruse"
	"github.com/freesoulcode/foya/internal/canvas"
	"github.com/freesoulcode/foya/internal/command"
	"github.com/freesoulcode/foya/internal/config"
	"github.com/freesoulcode/foya/internal/contextdata"
	"github.com/freesoulcode/foya/internal/event"
	"github.com/freesoulcode/foya/internal/hooks"
	"github.com/freesoulcode/foya/internal/imagegen"
	"github.com/freesoulcode/foya/internal/mcpclient"
	"github.com/freesoulcode/foya/internal/message"
	"github.com/freesoulcode/foya/internal/project"
	"github.com/freesoulcode/foya/internal/provider"
	"github.com/freesoulcode/foya/internal/question"
	"github.com/freesoulcode/foya/internal/session"
	"github.com/freesoulcode/foya/internal/skill"
	"github.com/freesoulcode/foya/internal/state"
	"github.com/freesoulcode/foya/internal/subagent"
	"github.com/freesoulcode/foya/internal/terminal"
	"github.com/freesoulcode/foya/internal/tool"
	"github.com/freesoulcode/foya/internal/websearch"
	"github.com/freesoulcode/foya/internal/workflow"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var (
	ErrConnectionNotFound = fmt.Errorf("connection not found")
	ErrConnectionInUse    = fmt.Errorf("connection is in use by a session")
	ErrUnsupportedAuth    = fmt.Errorf("unsupported connection auth kind")
)

// ProviderBuilder 按 provider 配置构造 provider 与默认模型名。
type ProviderBuilder func(config.Provider) (provider.Provider, string)

// Backend 是内核业务的统一入口(传输无关)。
type Backend struct {
	sessions           session.Manager
	log                *state.MemLog
	bus                *broker.Broker[event.Event]
	engine             *agent.Engine
	approval           approval.Gateway
	terminal           terminal.Manager
	skills             *skill.Manager
	web                *websearch.Manager
	mcp                *mcpclient.Manager
	projects           *project.Manager
	agents             *agentdef.Manager
	subagents          *subagent.Manager
	artifacts          artifact.Store
	canvases           *canvas.Store
	context            *contextdata.Store
	commands           *command.Manager
	workflows          *workflow.Manager
	questions          question.Gateway
	backgroundCommands tool.BackgroundCommandManager
	browser            *browseruse.Controller

	buildProvider     ProviderBuilder
	dataDir           string
	homeDir           string
	mu                sync.RWMutex
	projectMu         sync.Mutex
	connections       map[string]config.Connection
	providers         map[string]provider.Provider
	firstConnectionID string
	turns             *turnScheduler
	memoryWake        func()
}

const canvasGenerationTimeout = 5 * time.Minute

// SetCapabilityManagers attaches optional capability services assembled by the
// kernel composition root.
func (b *Backend) SetCapabilityManagers(skills *skill.Manager, web *websearch.Manager, mcp *mcpclient.Manager) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.skills = skills
	b.web = web
	b.mcp = mcp
}

func (b *Backend) SetBrowserController(controller *browseruse.Controller) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.browser = controller
}

func (b *Backend) ResolveBrowserAction(
	sessionID, requestID string,
	result browseruse.ActionResult,
) error {
	if _, ok := b.sessions.Get(sessionID); !ok {
		return session.ErrNotFound
	}
	b.mu.RLock()
	controller := b.browser
	b.mu.RUnlock()
	if controller == nil {
		return errors.New("browser use is unavailable")
	}
	return controller.Resolve(sessionID, requestID, result)
}

func (b *Backend) SetProjectManager(projects *project.Manager) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.projects = projects
}

func (b *Backend) SetAgentManager(agents *agentdef.Manager) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.agents = agents
}

func (b *Backend) SetSubAgentManager(manager *subagent.Manager) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.subagents = manager
}

func (b *Backend) SetArtifactStore(store artifact.Store) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.artifacts = store
	b.engine.SetArtifactStore(store)
}

func (b *Backend) SetCanvasStore(store *canvas.Store) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.canvases = store
}

func (b *Backend) canvasStore() (*canvas.Store, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if b.canvases == nil {
		return nil, errors.New("canvas store is unavailable")
	}
	return b.canvases, nil
}

func (b *Backend) publishCanvas(ctx context.Context, kind event.Kind, doc canvas.Document) {
	ev := event.Event{Seq: event.Seq(doc.Revision), Kind: kind, Session: doc.ID, Time: time.Now(), Payload: doc}
	_ = b.bus.PublishMustDeliver(ctx, "canvas:"+doc.ID, ev)
}

func (b *Backend) CreateCanvas(ctx context.Context, input canvas.CreateInput) (canvas.Document, error) {
	if input.SessionID != "" {
		s, ok := b.sessions.Get(input.SessionID)
		if !ok {
			return canvas.Document{}, session.ErrNotFound
		}
		if input.ProjectID == "" {
			input.ProjectID = s.ProjectID
		}
	}
	store, err := b.canvasStore()
	if err != nil {
		return canvas.Document{}, err
	}
	doc, err := store.Create(input)
	if err == nil {
		b.publishCanvas(ctx, event.KindCanvasCreated, doc)
	}
	return doc, err
}

func (b *Backend) ListCanvases(sessionID string) ([]canvas.Document, error) {
	store, err := b.canvasStore()
	if err != nil {
		return nil, err
	}
	return store.List(sessionID), nil
}

func (b *Backend) Canvas(id string) (canvas.Document, error) {
	store, err := b.canvasStore()
	if err != nil {
		return canvas.Document{}, err
	}
	doc, ok := store.Get(id)
	if !ok {
		return canvas.Document{}, canvas.ErrNotFound
	}
	return doc, nil
}

func (b *Backend) UpdateCanvas(ctx context.Context, id string, input canvas.UpdateInput) (canvas.Document, error) {
	store, err := b.canvasStore()
	if err != nil {
		return canvas.Document{}, err
	}
	doc, err := store.Update(id, input)
	if err == nil {
		b.publishCanvas(ctx, event.KindCanvasUpdated, doc)
	}
	return doc, err
}

func (b *Backend) DeleteCanvas(ctx context.Context, id string) error {
	store, err := b.canvasStore()
	if err != nil {
		return err
	}
	doc, ok := store.Get(id)
	if !ok {
		return canvas.ErrNotFound
	}
	if err := store.Delete(id); err != nil {
		return err
	}
	doc.Revision++
	b.publishCanvas(ctx, event.KindCanvasDeleted, doc)
	return nil
}

func (b *Backend) PutCanvasAsset(ctx context.Context, id, name, mediaType string, source io.Reader) (canvas.Asset, canvas.Document, error) {
	store, err := b.canvasStore()
	if err != nil {
		return canvas.Asset{}, canvas.Document{}, err
	}
	asset, err := store.PutAsset(ctx, id, name, mediaType, source)
	if err != nil {
		return canvas.Asset{}, canvas.Document{}, err
	}
	doc, _ := store.Get(id)
	b.publishCanvas(ctx, event.KindCanvasUpdated, doc)
	return asset, doc, nil
}

func (b *Backend) ReadCanvasAsset(ctx context.Context, id, assetID string) ([]byte, canvas.Asset, error) {
	store, err := b.canvasStore()
	if err != nil {
		return nil, canvas.Asset{}, err
	}
	return store.ReadAsset(ctx, id, assetID)
}

func (b *Backend) GenerateCanvasImage(ctx context.Context, id string, input canvas.GenerateImageInput) (canvas.Document, error) {
	store, err := b.canvasStore()
	if err != nil {
		return canvas.Document{}, err
	}
	doc, ok := store.Get(id)
	if !ok {
		return canvas.Document{}, canvas.ErrNotFound
	}
	if doc.Revision != input.ExpectedRevision {
		return doc, canvas.ErrRevisionConflict
	}

	configIndex, outputIndex := -1, -1
	for index := range doc.Nodes {
		switch doc.Nodes[index].ID {
		case input.ConfigNodeID:
			configIndex = index
		case input.OutputNodeID:
			outputIndex = index
		}
	}
	if configIndex < 0 || outputIndex < 0 {
		return doc, errors.New("generation config and output nodes are required")
	}
	configNode := doc.Nodes[configIndex]
	if configNode.Type != "generation" || configNode.Generation == nil || configNode.Generation.Mode != "image" {
		return doc, errors.New("node is not an image generation configuration")
	}
	if doc.Nodes[outputIndex].Type != "image" {
		return doc, errors.New("generation output node must be an image")
	}

	connectionID := input.ConnectionID
	if connectionID == "" {
		connectionID = configNode.Generation.ConnectionID
	}
	b.mu.RLock()
	if connectionID == "" {
		connectionID = b.firstConnectionID
	}
	connection, exists := b.connections[connectionID]
	b.mu.RUnlock()
	if !exists {
		return b.failCanvasGeneration(ctx, store, doc, configIndex, outputIndex, fmt.Errorf("%w: %q", ErrConnectionNotFound, connectionID))
	}

	promptParts := make([]string, 0, 4)
	if value := strings.TrimSpace(configNode.Prompt); value != "" {
		promptParts = append(promptParts, value)
	}
	references := make([][]byte, 0, 4)
	for _, edge := range doc.Edges {
		if edge.ToNodeID != configNode.ID {
			continue
		}
		for _, node := range doc.Nodes {
			if node.ID != edge.FromNodeID {
				continue
			}
			if node.Type == "text" {
				if value := strings.TrimSpace(node.Text); value != "" {
					promptParts = append(promptParts, value)
				}
			}
			if node.Type == "image" && node.AssetID != "" {
				data, _, readErr := store.ReadAsset(ctx, doc.ID, node.AssetID)
				if readErr != nil {
					return b.failCanvasGeneration(ctx, store, doc, configIndex, outputIndex, readErr)
				}
				references = append(references, data)
			}
		}
	}
	prompt := strings.Join(promptParts, "\n\n")
	if prompt == "" {
		return b.failCanvasGeneration(ctx, store, doc, configIndex, outputIndex, errors.New("connect a prompt node or enter a prompt in the generation node"))
	}

	generationCtx, cancelGeneration := context.WithTimeout(ctx, canvasGenerationTimeout)
	defer cancelGeneration()
	result, generateErr := imagegen.NewOpenAI(imagegen.OpenAIConfig{
		BaseURL: connection.BaseURL,
		APIKey:  connection.APIKey,
	}).Generate(generationCtx, imagegen.Request{
		Model:       configNode.Generation.Model,
		Prompt:      prompt,
		AspectRatio: configNode.Generation.AspectRatio,
		Quality:     configNode.Generation.Quality,
		References:  references,
	})
	if generateErr != nil {
		if errors.Is(generateErr, context.DeadlineExceeded) {
			generateErr = fmt.Errorf("生图请求超时（超过 %s）", canvasGenerationTimeout)
		}
		return b.failCanvasGeneration(ctx, store, doc, configIndex, outputIndex, generateErr)
	}

	asset, err := store.PutAsset(ctx, doc.ID, "generated.png", result.MediaType, bytes.NewReader(result.Data))
	if err != nil {
		return b.failCanvasGeneration(ctx, store, doc, configIndex, outputIndex, err)
	}
	current, _ := store.Get(doc.ID)
	for index := range current.Nodes {
		switch current.Nodes[index].ID {
		case configNode.ID:
			current.Nodes[index].Status = "success"
			current.Nodes[index].Error = ""
		case input.OutputNodeID:
			current.Nodes[index].AssetID = asset.ID
			current.Nodes[index].Status = "success"
			current.Nodes[index].Error = ""
			if asset.Width > 0 && asset.Height > 0 {
				current.Nodes[index].Width = 360
				current.Nodes[index].Height = 360 * float64(asset.Height) / float64(asset.Width)
			}
		}
	}
	updated, err := store.Update(current.ID, canvas.UpdateInput{
		ExpectedRevision: current.Revision,
		Nodes:            &current.Nodes,
	})
	if err == nil {
		b.publishCanvas(ctx, event.KindCanvasUpdated, updated)
	}
	return updated, err
}

func (b *Backend) failCanvasGeneration(
	ctx context.Context,
	store *canvas.Store,
	doc canvas.Document,
	configIndex, outputIndex int,
	cause error,
) (canvas.Document, error) {
	doc.Nodes[configIndex].Status = "error"
	doc.Nodes[configIndex].Error = cause.Error()
	doc.Nodes[outputIndex].Status = "error"
	doc.Nodes[outputIndex].Error = cause.Error()
	updated, updateErr := store.Update(doc.ID, canvas.UpdateInput{
		ExpectedRevision: doc.Revision,
		Nodes:            &doc.Nodes,
	})
	if updateErr == nil {
		b.publishCanvas(ctx, event.KindCanvasUpdated, updated)
		return updated, cause
	}
	return doc, cause
}

func (b *Backend) SubscribeCanvas(ctx context.Context, id string) (<-chan event.Event, error) {
	if _, err := b.Canvas(id); err != nil {
		return nil, err
	}
	return b.bus.Subscribe(ctx, "canvas:"+id), nil
}

func (b *Backend) SetContextStore(store *contextdata.Store) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.context = store
}

func (b *Backend) SetCommandManager(manager *command.Manager) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.commands = manager
}

func (b *Backend) SetWorkflowManager(manager *workflow.Manager) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.workflows = manager
}

// SetQuestionGateway attaches the human-input coordinator assembled by kernel.
func (b *Backend) SetQuestionGateway(gateway question.Gateway) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.questions = gateway
}

func (b *Backend) SetBackgroundCommandManager(manager tool.BackgroundCommandManager) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.backgroundCommands = manager
}

// SetHooksHomeDir configures the user-level hook root. The Hook runtime itself
// reloads hook files for each lifecycle event, so settings updates take effect
// without restarting the kernel.
func (b *Backend) SetHooksHomeDir(homeDir string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.homeDir = homeDir
}

// Hooks returns effective hooks for a global or project scope.
func (b *Backend) Hooks(scope, projectID string) ([]hooks.Config, error) {
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
func (b *Backend) ReplaceHooks(scope, projectID string, items []hooks.Config) error {
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
func (b *Backend) SetMemoryMaintenanceWake(wake func()) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.memoryWake = wake
}

func (b *Backend) MemorySettings() (contextdata.MemorySettings, error) {
	store, err := b.contextStore()
	if err != nil {
		return contextdata.MemorySettings{}, err
	}
	return store.MemorySettings(), nil
}

func (b *Backend) UpdateMemorySettings(
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

func (b *Backend) Rules(scope contextdata.Scope, projectID string) ([]contextdata.Rule, error) {
	store, err := b.contextStore()
	if err != nil {
		return nil, err
	}
	if err := b.validateContextScope(scope, projectID); err != nil {
		return nil, err
	}
	return store.ListRules(scope, projectID), nil
}

func (b *Backend) CreateRule(
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

func (b *Backend) UpdateRule(
	id, content string,
	options ...contextdata.RuleOptions,
) (contextdata.Rule, error) {
	store, err := b.contextStore()
	if err != nil {
		return contextdata.Rule{}, err
	}
	return store.UpdateRule(id, content, options...)
}

func (b *Backend) DeleteRule(id string) error {
	store, err := b.contextStore()
	if err != nil {
		return err
	}
	return store.DeleteRule(id)
}

func (b *Backend) Memories(scope contextdata.Scope, projectID string) ([]contextdata.Memory, error) {
	store, err := b.contextStore()
	if err != nil {
		return nil, err
	}
	if err := b.validateContextScope(scope, projectID); err != nil {
		return nil, err
	}
	return store.ListMemories(scope, projectID), nil
}

func (b *Backend) CreateMemory(
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
func (b *Backend) SetMemory(
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
func (b *Backend) Memory(scope contextdata.Scope, projectID string) (contextdata.Memory, error) {
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
func (b *Backend) ClearMemory(scope contextdata.Scope, projectID string) error {
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

func (b *Backend) UpdateMemory(id, content string) (contextdata.Memory, error) {
	store, err := b.contextStore()
	if err != nil {
		return contextdata.Memory{}, err
	}
	return store.UpdateMemory(id, content)
}

func (b *Backend) DeleteMemory(id string) error {
	store, err := b.contextStore()
	if err != nil {
		return err
	}
	return store.DeleteMemory(id)
}

func (b *Backend) ReplayContext(after uint64) ([]contextdata.Event, error) {
	store, err := b.contextStore()
	if err != nil {
		return nil, err
	}
	return store.Replay(after), nil
}

func (b *Backend) SubscribeContext(ctx context.Context) (<-chan contextdata.Event, error) {
	store, err := b.contextStore()
	if err != nil {
		return nil, err
	}
	return store.Subscribe(ctx), nil
}

func (b *Backend) contextStore() (*contextdata.Store, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if b.context == nil {
		return nil, errors.New("rules and memory are unavailable")
	}
	return b.context, nil
}

func (b *Backend) validateContextScope(scope contextdata.Scope, projectID string) error {
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

func (b *Backend) PutImage(
	ctx context.Context,
	sessionID, name string,
	source io.Reader,
) (message.AttachmentRef, error) {
	if _, ok := b.sessions.Get(sessionID); !ok {
		return message.AttachmentRef{}, session.ErrNotFound
	}
	b.mu.RLock()
	store := b.artifacts
	b.mu.RUnlock()
	if store == nil {
		return message.AttachmentRef{}, errors.New("artifact store is unavailable")
	}
	return store.PutImage(ctx, sessionID, name, source)
}

func (b *Backend) ReadArtifact(
	ctx context.Context,
	sessionID, artifactID string,
) ([]byte, message.AttachmentRef, error) {
	if _, ok := b.sessions.Get(sessionID); !ok {
		return nil, message.AttachmentRef{}, session.ErrNotFound
	}
	b.mu.RLock()
	store := b.artifacts
	b.mu.RUnlock()
	if store == nil {
		return nil, message.AttachmentRef{}, errors.New("artifact store is unavailable")
	}
	return store.Read(ctx, sessionID, artifactID)
}

func (b *Backend) DeleteArtifact(ctx context.Context, sessionID, artifactID string) error {
	if _, ok := b.sessions.Get(sessionID); !ok {
		return session.ErrNotFound
	}
	b.mu.RLock()
	store := b.artifacts
	b.mu.RUnlock()
	if store == nil {
		return errors.New("artifact store is unavailable")
	}
	return store.Delete(ctx, sessionID, artifactID)
}

func (b *Backend) AgentLimits() (config.AgentLimits, error) {
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

func (b *Backend) UpdateAgentLimits(limits config.AgentLimits) error {
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

func (b *Backend) AgentRuns(sessionID string) ([]subagent.Snapshot, error) {
	if _, ok := b.sessions.Get(sessionID); !ok {
		return nil, session.ErrNotFound
	}
	b.mu.RLock()
	manager := b.subagents
	b.mu.RUnlock()
	if manager == nil {
		return nil, errors.New("sub-agents are unavailable")
	}
	return manager.List(sessionID), nil
}

func (b *Backend) AgentRun(sessionID, runID string) (subagent.Snapshot, error) {
	if _, ok := b.sessions.Get(sessionID); !ok {
		return subagent.Snapshot{}, session.ErrNotFound
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

func (b *Backend) StartAgent(ctx context.Context, request subagent.SpawnRequest) (subagent.Snapshot, error) {
	b.mu.RLock()
	manager := b.subagents
	b.mu.RUnlock()
	if manager == nil {
		return subagent.Snapshot{}, errors.New("sub-agents are unavailable")
	}
	return manager.Start(ctx, request)
}

func (b *Backend) WaitAgents(
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

func (b *Backend) CancelAgent(sessionID, runID string) error {
	if _, err := b.AgentRun(sessionID, runID); err != nil {
		return err
	}
	b.mu.RLock()
	manager := b.subagents
	b.mu.RUnlock()
	return manager.Cancel(runID)
}

func (b *Backend) AgentBudget(sessionID string) (subagent.Budget, error) {
	if _, ok := b.sessions.Get(sessionID); !ok {
		return subagent.Budget{}, session.ErrNotFound
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
func (b *Backend) Agents(ctx context.Context) ([]agentdef.Definition, error) {
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
func (b *Backend) ProjectAgents(ctx context.Context, projectID string) ([]agentdef.Definition, error) {
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

func (b *Backend) Projects() ([]project.Project, error) {
	b.mu.RLock()
	manager := b.projects
	b.mu.RUnlock()
	if manager == nil {
		return nil, errors.New("projects are unavailable")
	}
	return manager.List(), nil
}

func (b *Backend) RegisterProject(path, name string) (project.Project, error) {
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

func (b *Backend) Project(id string) (project.Project, error) {
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

func (b *Backend) UpdateProject(
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
func (b *Backend) DeleteProject(ctx context.Context, id string) error {
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
func projectSessionRoots(items []*session.Session, projectID string) []string {
	byID := make(map[string]*session.Session, len(items))
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

func (b *Backend) Skills(ctx context.Context) ([]skill.Skill, error) {
	b.mu.RLock()
	manager := b.skills
	b.mu.RUnlock()
	if manager == nil {
		return nil, errors.New("skills are unavailable")
	}
	return manager.List(ctx, "", "")
}

func (b *Backend) AllSkills(ctx context.Context) ([]skill.Skill, error) {
	b.mu.RLock()
	manager := b.skills
	b.mu.RUnlock()
	if manager == nil {
		return nil, errors.New("skills are unavailable")
	}
	return manager.ListAll(ctx, "", "")
}

func (b *Backend) InspectSkills(ctx context.Context) (skill.ScanResult, error) {
	b.mu.RLock()
	manager := b.skills
	b.mu.RUnlock()
	if manager == nil {
		return skill.ScanResult{}, errors.New("skills are unavailable")
	}
	return manager.Inspect(ctx, "", "")
}

func (b *Backend) ProjectSkills(ctx context.Context, projectID string) ([]skill.Skill, error) {
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

func (b *Backend) InspectProjectSkills(ctx context.Context, projectID string) (skill.ScanResult, error) {
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

func (b *Backend) SetSkillEnabled(ref string, enabled bool) error {
	b.mu.RLock()
	manager := b.skills
	b.mu.RUnlock()
	if manager == nil {
		return errors.New("skills are unavailable")
	}
	return manager.SetEnabled(ref, enabled)
}

func (b *Backend) SetSkillPinned(ref string, pinned bool) error {
	b.mu.RLock()
	manager := b.skills
	b.mu.RUnlock()
	if manager == nil {
		return errors.New("skills are unavailable")
	}
	return manager.SetPinned(ref, pinned)
}

func (b *Backend) WebSearchSettings() (websearch.Settings, error) {
	b.mu.RLock()
	manager := b.web
	b.mu.RUnlock()
	if manager == nil {
		return websearch.Settings{}, errors.New("web search is unavailable")
	}
	return manager.Settings(), nil
}

func (b *Backend) UpdateWebSearchSettings(settings websearch.Settings) error {
	b.mu.RLock()
	manager := b.web
	b.mu.RUnlock()
	if manager == nil {
		return errors.New("web search is unavailable")
	}
	return manager.Update(settings)
}

func (b *Backend) TestWebSearch(ctx context.Context, providerID, query string) ([]provider.SearchResult, error) {
	b.mu.RLock()
	manager := b.web
	b.mu.RUnlock()
	if manager == nil {
		return nil, errors.New("web search is unavailable")
	}
	return manager.Test(ctx, providerID, query)
}

func (b *Backend) SearchWeb(ctx context.Context, query string) ([]provider.SearchResult, string, error) {
	b.mu.RLock()
	manager := b.web
	b.mu.RUnlock()
	if manager == nil {
		return nil, "", errors.New("web search is unavailable")
	}
	return manager.Search(ctx, websearch.Request{Query: query})
}

func (b *Backend) MCPConfig() (mcpclient.Config, error) {
	b.mu.RLock()
	manager := b.mcp
	b.mu.RUnlock()
	if manager == nil {
		return mcpclient.Config{}, errors.New("MCP is unavailable")
	}
	return manager.Config(), nil
}

func (b *Backend) ReplaceMCPConfig(ctx context.Context, config mcpclient.Config) error {
	b.mu.RLock()
	manager := b.mcp
	b.mu.RUnlock()
	if manager == nil {
		return errors.New("MCP is unavailable")
	}
	return manager.Replace(ctx, config)
}

func (b *Backend) MCPStatuses() ([]mcpclient.Status, error) {
	b.mu.RLock()
	manager := b.mcp
	b.mu.RUnlock()
	if manager == nil {
		return nil, errors.New("MCP is unavailable")
	}
	return manager.Statuses(), nil
}

func (b *Backend) SearchMCPRegistry(
	ctx context.Context,
	query string,
) ([]mcpclient.RegistryServer, error) {
	return mcpclient.SearchRegistry(ctx, query)
}

func (b *Backend) MCPResources(ctx context.Context, serverID string) ([]*mcp.Resource, error) {
	b.mu.RLock()
	manager := b.mcp
	b.mu.RUnlock()
	if manager == nil {
		return nil, errors.New("MCP is unavailable")
	}
	return manager.Resources(ctx, serverID)
}

func (b *Backend) MCPReadResource(ctx context.Context, serverID, uri string) (*mcp.ReadResourceResult, error) {
	b.mu.RLock()
	manager := b.mcp
	b.mu.RUnlock()
	if manager == nil {
		return nil, errors.New("MCP is unavailable")
	}
	return manager.ReadResource(ctx, serverID, uri)
}

func (b *Backend) MCPPrompts(ctx context.Context, serverID string) ([]*mcp.Prompt, error) {
	b.mu.RLock()
	manager := b.mcp
	b.mu.RUnlock()
	if manager == nil {
		return nil, errors.New("MCP is unavailable")
	}
	return manager.Prompts(ctx, serverID)
}

func (b *Backend) MCPGetPrompt(ctx context.Context, serverID, name string, args map[string]string) (*mcp.GetPromptResult, error) {
	b.mu.RLock()
	manager := b.mcp
	b.mu.RUnlock()
	if manager == nil {
		return nil, errors.New("MCP is unavailable")
	}
	return manager.GetPrompt(ctx, serverID, name, args)
}

// New 组装一个 Backend。
func New(
	sessions session.Manager,
	log *state.MemLog,
	bus *broker.Broker[event.Event],
	engine *agent.Engine,
	gw approval.Gateway,
	terminalManager terminal.Manager,
	build ProviderBuilder,
	provCfg config.Provider,
	dataDir string,
) *Backend {
	return &Backend{
		sessions:      sessions,
		log:           log,
		bus:           bus,
		engine:        engine,
		approval:      gw,
		terminal:      terminalManager,
		buildProvider: build,
		dataDir:       dataDir,
		connections:   make(map[string]config.Connection),
		providers:     make(map[string]provider.Provider),
		turns:         newTurnScheduler(),
	}
}

// CreateSession 新建会话。
func (b *Backend) CreateSession(opts session.CreateOptions) (*session.Session, error) {
	b.projectMu.Lock()
	defer b.projectMu.Unlock()
	b.mu.RLock()
	connectionID := opts.ConnectionID
	if connectionID == "" {
		connectionID = b.firstConnectionID
	}
	_, exists := b.connections[connectionID]
	b.mu.RUnlock()
	if connectionID != "" && !exists {
		return nil, fmt.Errorf("%w: %q", ErrConnectionNotFound, connectionID)
	}
	opts.ConnectionID = connectionID
	if err := b.resolveSessionProject(&opts); err != nil {
		return nil, err
	}
	created, err := b.sessions.Create(opts)
	if err != nil {
		return nil, err
	}
	// SessionStart runs after the session has a durable identity and before a
	// client can submit its first turn. Hook failures fail open and are
	// persisted as hook audit events by the engine.
	b.engine.RunSessionStart(context.Background(), created.ID)
	if created.ParentID == "" {
		b.mu.RLock()
		wake := b.memoryWake
		b.mu.RUnlock()
		if wake != nil {
			wake()
		}
	}
	return created, nil
}

// UpdateSession 局部更新会话可变字段。
func (b *Backend) UpdateSession(
	ctx context.Context,
	id string,
	connectionID, model, reasoningEffort, projectID, approvalMode *string,
) (*session.Session, error) {
	b.projectMu.Lock()
	defer b.projectMu.Unlock()
	if connectionID != nil {
		b.mu.RLock()
		_, exists := b.connections[*connectionID]
		b.mu.RUnlock()
		if !exists {
			return nil, fmt.Errorf("%w: %q", ErrConnectionNotFound, *connectionID)
		}
	}
	if projectID != nil && *projectID != "" {
		if _, err := b.Project(*projectID); err != nil {
			return nil, err
		}
	}
	updated, err := b.sessions.Update(id, connectionID, model, reasoningEffort, projectID, approvalMode)
	if err != nil {
		return nil, err
	}
	b.broadcastSession(ctx, updated)
	return updated, nil
}

func (b *Backend) resolveSessionProject(opts *session.CreateOptions) error {
	b.mu.RLock()
	manager := b.projects
	b.mu.RUnlock()
	if manager == nil {
		return nil
	}
	if opts.ProjectID != "" {
		_, ok := manager.Get(opts.ProjectID)
		if !ok {
			return project.ErrNotFound
		}
	}
	return nil
}

// RenameSession 手动改名。
func (b *Backend) RenameSession(ctx context.Context, id, title string) (*session.Session, error) {
	if err := b.sessions.Rename(id, title); err != nil {
		return nil, err
	}
	s, ok := b.sessions.Get(id)
	if !ok {
		return nil, session.ErrNotFound
	}
	b.broadcastSession(ctx, s)
	return s, nil
}

// PinSession 置顶/取消置顶会话,变更后广播 session_updated。
func (b *Backend) PinSession(ctx context.Context, id string, pinned bool) (*session.Session, error) {
	s, err := b.sessions.SetPinned(id, pinned)
	if err != nil {
		return nil, err
	}
	b.broadcastSession(ctx, s)
	return s, nil
}

// deleteTurnGrace 是删除会话时等待活跃回合彻底收尾的上限。
const deleteTurnGrace = 3 * time.Second

// DeleteSession 删除会话:
//  1. 先中断正在跑的回合并等其 goroutine 彻底退出,确保清理后不再有事件写入;
//  2. 删除会话元数据与事件日志(日志层标记删除,迟到事件在 Append 处丢弃);
//  3. 广播 session_deleted 通知在线客户端移除。
//
// session_deleted 属于会话目录层事件,只广播、不写入该会话分区日志——
// 会话已不存在,持久化它没有读者,重连时靠 list_sessions 自然收敛。
func (b *Backend) DeleteSession(ctx context.Context, id string) error {
	if _, ok := b.sessions.Get(id); !ok {
		return session.ErrNotFound
	}
	all := b.sessions.List()
	var ordered []string
	var visit func(string)
	visit = func(parentID string) {
		for _, item := range all {
			if item.ParentID == parentID {
				visit(item.ID)
			}
		}
		ordered = append(ordered, parentID)
	}
	visit(id)
	for _, sessionID := range ordered {
		b.stopSessionAndWait(sessionID, deleteTurnGrace)
		b.terminal.CloseSession(sessionID)
		b.approval.ClearSession(sessionID)
		b.mu.RLock()
		questions := b.questions
		b.mu.RUnlock()
		if questions != nil {
			questions.ClearSession(sessionID)
		}
		b.mu.RLock()
		backgroundCommands := b.backgroundCommands
		b.mu.RUnlock()
		if backgroundCommands != nil {
			backgroundCommands.ClearSession(sessionID)
		}
		b.mu.RLock()
		browserController := b.browser
		b.mu.RUnlock()
		if browserController != nil {
			browserController.ClearSession(sessionID)
		}
		if err := b.sessions.Delete(sessionID); err != nil {
			return err
		}
		b.log.Delete(sessionID)
		b.mu.RLock()
		artifactStore := b.artifacts
		b.mu.RUnlock()
		if artifactStore != nil {
			_ = artifactStore.DeleteSession(ctx, sessionID)
		}
		ev := event.Event{
			Kind:    event.KindSessionDeleted,
			Session: sessionID,
			Time:    time.Now(),
			Payload: map[string]string{"id": sessionID},
		}
		_ = b.bus.PublishMustDeliver(ctx, "session:"+sessionID, ev)
	}
	return nil
}

// broadcastSession 把会话当前状态作为 session_updated 事件持久化并广播。
func (b *Backend) broadcastSession(ctx context.Context, s *session.Session) {
	ev := event.Event{Kind: event.KindSessionUpdated, Session: s.ID, Time: time.Now(), Payload: s}
	seq, _ := b.log.Append(ctx, ev)
	ev.Seq = seq
	_ = b.bus.PublishMustDeliver(ctx, "session:"+s.ID, ev)
}

// ListSessions 列出会话。
func (b *Backend) ListSessions() []*session.Session {
	all := b.sessions.List()
	out := make([]*session.Session, 0, len(all))
	for _, item := range all {
		if item.ParentID == "" {
			out = append(out, item)
		}
	}
	return out
}

// ChildSessions returns direct children of one parent session.
func (b *Backend) ChildSessions(parentID string) ([]*session.Session, error) {
	if _, ok := b.sessions.Get(parentID); !ok {
		return nil, session.ErrNotFound
	}
	all := b.sessions.List()
	out := make([]*session.Session, 0)
	for _, item := range all {
		if item.ParentID == parentID {
			out = append(out, item)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out, nil
}

// CancelTurn 中断指定会话当前正在运行的回合(用户点停止)。
// 队列保留且暂停自动发送,由用户选择“立即发送”或再次发送后恢复。
func (b *Backend) CancelTurn(sessionID string) {
	b.cancelCurrentTurn(sessionID)
	b.mu.RLock()
	manager := b.subagents
	b.mu.RUnlock()
	if manager != nil {
		manager.CancelTree(sessionID)
	}
}

func (b *Backend) CancelTool(sessionID, toolCallID string) error {
	if _, ok := b.sessions.Get(sessionID); !ok {
		return session.ErrNotFound
	}
	if !b.engine.CancelTool(sessionID, toolCallID) {
		return ErrToolCallNotRunning
	}
	return nil
}

func (b *Backend) BackgroundTool(
	sessionID, toolCallID string,
) (tool.BackgroundCommandSnapshot, error) {
	if _, ok := b.sessions.Get(sessionID); !ok {
		return tool.BackgroundCommandSnapshot{}, session.ErrNotFound
	}
	b.mu.RLock()
	manager := b.backgroundCommands
	b.mu.RUnlock()
	if manager == nil {
		return tool.BackgroundCommandSnapshot{}, errors.New("background commands are unavailable")
	}
	return manager.Promote(sessionID, toolCallID)
}

func (b *Backend) RevealToolCommand(
	sessionID, toolCallID string,
) (tool.BackgroundCommandSnapshot, error) {
	if _, ok := b.sessions.Get(sessionID); !ok {
		return tool.BackgroundCommandSnapshot{}, session.ErrNotFound
	}
	b.mu.RLock()
	manager := b.backgroundCommands
	b.mu.RUnlock()
	if manager == nil {
		return tool.BackgroundCommandSnapshot{}, errors.New("managed commands are unavailable")
	}
	return manager.Reveal(sessionID, toolCallID)
}

func (b *Backend) ListBackgroundCommands(
	sessionID string,
) ([]tool.BackgroundCommandSnapshot, error) {
	if _, ok := b.sessions.Get(sessionID); !ok {
		return nil, session.ErrNotFound
	}
	b.mu.RLock()
	manager := b.backgroundCommands
	b.mu.RUnlock()
	if manager == nil {
		return nil, errors.New("background commands are unavailable")
	}
	return manager.List(sessionID), nil
}

func (b *Backend) GetBackgroundCommand(
	sessionID, commandID string,
) (tool.BackgroundCommandSnapshot, error) {
	if _, ok := b.sessions.Get(sessionID); !ok {
		return tool.BackgroundCommandSnapshot{}, session.ErrNotFound
	}
	b.mu.RLock()
	manager := b.backgroundCommands
	b.mu.RUnlock()
	if manager == nil {
		return tool.BackgroundCommandSnapshot{}, errors.New("background commands are unavailable")
	}
	return manager.Get(sessionID, commandID)
}

func (b *Backend) StopBackgroundCommand(
	sessionID, commandID string,
) (tool.BackgroundCommandSnapshot, error) {
	if _, ok := b.sessions.Get(sessionID); !ok {
		return tool.BackgroundCommandSnapshot{}, session.ErrNotFound
	}
	b.mu.RLock()
	manager := b.backgroundCommands
	b.mu.RUnlock()
	if manager == nil {
		return tool.BackgroundCommandSnapshot{}, errors.New("background commands are unavailable")
	}
	return manager.Stop(sessionID, commandID, "user")
}

// ResolveApproval 回执一个审批决策(由客户端经 REST 触发)。
func (b *Backend) ResolveApproval(requestID string, decision string) error {
	d := approval.Decision(decision)
	return b.approval.Resolve(requestID, d)
}

// AnswerQuestions supplies the complete response set for one pending ask_user
// request. The gateway's take semantics make this safe across clients.
func (b *Backend) AnswerQuestions(sessionID, batchID string, answers []question.Answer) error {
	if _, ok := b.sessions.Get(sessionID); !ok {
		return session.ErrNotFound
	}
	b.mu.RLock()
	gateway := b.questions
	b.mu.RUnlock()
	if gateway == nil {
		return errors.New("question gateway is unavailable")
	}
	return gateway.Answer(sessionID, batchID, answers)
}

// CancelQuestions abandons one pending ask_user request without cancelling the
// whole conversation.
func (b *Backend) CancelQuestions(sessionID, batchID string) error {
	if _, ok := b.sessions.Get(sessionID); !ok {
		return session.ErrNotFound
	}
	b.mu.RLock()
	gateway := b.questions
	b.mu.RUnlock()
	if gateway == nil {
		return errors.New("question gateway is unavailable")
	}
	return gateway.Cancel(sessionID, batchID)
}

// StartTerminal starts an interactive shell in the session project.
func (b *Backend) StartTerminal(
	ctx context.Context,
	sessionID string,
	cols, rows uint16,
) (terminal.Snapshot, error) {
	s, ok := b.sessions.Get(sessionID)
	if !ok {
		return terminal.Snapshot{}, session.ErrNotFound
	}
	projectPath := ""
	if s.ProjectID != "" {
		item, err := b.Project(s.ProjectID)
		if err != nil {
			return terminal.Snapshot{}, err
		}
		projectPath = item.Path
	}
	return b.terminal.Start(ctx, sessionID, projectPath, cols, rows)
}

// AttachTerminal returns the current recoverable terminal snapshot.
func (b *Backend) AttachTerminal(sessionID, ref string) (terminal.Snapshot, error) {
	if _, ok := b.sessions.Get(sessionID); !ok {
		return terminal.Snapshot{}, session.ErrNotFound
	}
	return b.terminal.Attach(sessionID, ref)
}

// WriteTerminal forwards user input to an interactive shell.
func (b *Backend) WriteTerminal(sessionID, ref, input string) error {
	return b.terminal.Write(sessionID, ref, input)
}

// ResizeTerminal updates the PTY geometry.
func (b *Backend) ResizeTerminal(sessionID, ref string, cols, rows uint16) error {
	return b.terminal.Resize(sessionID, ref, cols, rows)
}

// StopTerminal terminates one interactive shell.
func (b *Backend) StopTerminal(sessionID, ref string) error {
	return b.terminal.Stop(sessionID, ref)
}

// SubscribeTerminal streams ordered PTY output independently from chat events.
func (b *Backend) SubscribeTerminal(
	ctx context.Context,
	sessionID, ref string,
	after uint64,
) (<-chan terminal.DataEvent, error) {
	return b.terminal.Subscribe(ctx, sessionID, ref, after)
}

// Subscribe 订阅某会话的事件流。
func (b *Backend) Subscribe(ctx context.Context, sessionID string) <-chan event.Event {
	return b.bus.Subscribe(ctx, "session:"+sessionID)
}

// History 返回某会话的对话历史。
func (b *Backend) History(ctx context.Context, sessionID string) ([]message.Message, error) {
	return b.log.History(ctx, sessionID)
}

// Replay 返回某会话中序号大于 after 的历史事件。
func (b *Backend) Replay(ctx context.Context, sessionID string, after event.Seq) ([]event.Event, error) {
	return b.log.Read(ctx, sessionID, after)
}

// Connections returns the configured Connection catalog in its user-defined order.
func (b *Backend) Connections() []config.Connection {
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := make([]config.Connection, 0, len(b.connections))
	for _, connection := range b.connections {
		out = append(out, connection)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].SortOrder != out[j].SortOrder {
			return out[i].SortOrder < out[j].SortOrder
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func (b *Backend) Connection(id string) (config.Connection, bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	connection, ok := b.connections[id]
	return connection, ok
}

// ListModels lists the catalog from one Connection's provider.
func (b *Backend) ListModels(ctx context.Context, connectionID string) ([]provider.ModelInfo, error) {
	b.mu.RLock()
	prov, ok := b.providers[connectionID]
	b.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrConnectionNotFound, connectionID)
	}
	lister, ok := prov.(provider.ModelLister)
	if !ok {
		return nil, fmt.Errorf("connection %q does not support model discovery", connectionID)
	}
	return lister.ListModels(ctx)
}

// Usage 返回会话最近一次模型请求的 token 使用情况。
func (b *Backend) Usage(ctx context.Context, sessionID string) (*provider.Usage, error) {
	if _, ok := b.sessions.Get(sessionID); !ok {
		return nil, session.ErrNotFound
	}
	events, err := b.log.Read(ctx, sessionID, 0)
	if err != nil {
		return nil, err
	}
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Kind != event.KindUsageUpdated {
			continue
		}
		switch usage := events[i].Payload.(type) {
		case provider.Usage:
			return &usage, nil
		case *provider.Usage:
			return usage, nil
		}
	}
	return nil, nil
}

// SetConnections replaces the Connection catalog and rebuilds provider clients.
// Existing Sessions keep their connection_id and therefore remain pinned to
// their original endpoint as long as that Connection remains configured.
func (b *Backend) SetConnections(connections []config.Connection) {
	nextConnections := make(map[string]config.Connection, len(connections))
	nextProviders := make(map[string]provider.Provider, len(connections))
	normalized := make([]config.Connection, 0, len(connections))
	sort.SliceStable(connections, func(i, j int) bool {
		if connections[i].LegacyDefault != connections[j].LegacyDefault {
			return connections[i].LegacyDefault
		}
		return connections[i].SortOrder < connections[j].SortOrder
	})
	for _, connection := range connections {
		if connection.ID == "" {
			continue
		}
		if connection.Kind == "" {
			connection.Kind = "openai"
		}
		if connection.AuthKind == "" {
			connection.AuthKind = "api_key"
		}
		if connection.Name == "" {
			connection.Name = connection.ID
		}
		normalized = append(normalized, connection)
	}
	for index := range normalized {
		connection := normalized[index]
		connection.SortOrder = index
		connection.LegacyDefault = false
		normalized[index] = connection
		nextConnections[connection.ID] = connection
		if b.buildProvider != nil {
			prov, _ := b.buildProvider(connection.Provider())
			nextProviders[connection.ID] = prov
		}
	}
	b.mu.Lock()
	b.connections = nextConnections
	b.providers = nextProviders
	b.firstConnectionID = ""
	if len(normalized) > 0 {
		b.firstConnectionID = normalized[0].ID
	}
	b.mu.Unlock()
	b.engine.SetProviderResolver(b.resolveSessionProvider)
	b.engine.SetImageCapabilityResolver(b.resolveSessionImageCapability)
	b.engine.SetContextWindowResolver(b.resolveSessionContextWindow)
	if err := config.SaveConnections(b.dataDir, normalized); err != nil {
		fmt.Fprintf(os.Stderr, "persist connections config failed: %v\n", err)
	}
}

func (b *Backend) CreateConnection(input config.Connection) (config.Connection, error) {
	if input.ContextWindow < 0 {
		return config.Connection{}, fmt.Errorf("context_window must be zero or greater")
	}
	if input.ID == "" {
		input.ID = newConnectionID()
	}
	if input.Name == "" {
		input.Name = input.ID
	}
	if input.Kind == "" {
		input.Kind = "openai"
	}
	if input.AuthKind == "" {
		input.AuthKind = "api_key"
	}
	if input.AuthKind != "api_key" {
		return config.Connection{}, fmt.Errorf("%w: %q", ErrUnsupportedAuth, input.AuthKind)
	}
	b.mu.Lock()
	if _, exists := b.connections[input.ID]; exists {
		b.mu.Unlock()
		return config.Connection{}, fmt.Errorf("connection %q already exists", input.ID)
	}
	b.mu.Unlock()
	connections := b.Connections()
	input.SortOrder = len(connections)
	connections = append(connections, input)
	b.SetConnections(connections)
	created, _ := b.Connection(input.ID)
	return created, nil
}

func (b *Backend) UpdateConnection(id string, patch config.Connection) (config.Connection, error) {
	current, exists := b.Connection(id)
	if !exists {
		return config.Connection{}, fmt.Errorf("%w: %q", ErrConnectionNotFound, id)
	}
	if patch.ContextWindow < 0 {
		return config.Connection{}, fmt.Errorf("context_window must be zero or greater")
	}
	patch.ID = id
	if patch.Name == "" {
		patch.Name = current.Name
	}
	if patch.Kind == "" {
		patch.Kind = current.Kind
	}
	if patch.AuthKind == "" {
		patch.AuthKind = current.AuthKind
	}
	if patch.AuthKind != "api_key" {
		return config.Connection{}, fmt.Errorf("%w: %q", ErrUnsupportedAuth, patch.AuthKind)
	}
	if patch.BaseURL == "" {
		patch.BaseURL = current.BaseURL
	}
	if patch.APIKey == "" {
		patch.APIKey = current.APIKey
	}
	connections := b.Connections()
	targetIndex := patch.SortOrder
	if targetIndex < 0 {
		targetIndex = 0
	}
	if targetIndex >= len(connections) {
		targetIndex = len(connections) - 1
	}
	var updated config.Connection
	found := -1
	for index := range connections {
		if connections[index].ID == id {
			updated = patch
			found = index
			break
		}
	}
	if found >= 0 {
		connections = append(connections[:found], connections[found+1:]...)
		connections = append(connections, config.Connection{})
		copy(connections[targetIndex+1:], connections[targetIndex:])
		connections[targetIndex] = updated
	}
	b.SetConnections(connections)
	updated, _ = b.Connection(id)
	return updated, nil
}

func (b *Backend) DeleteConnection(id string) error {
	ctx := context.Background()
	var updated []*session.Session
	for _, sess := range b.sessions.List() {
		if sess.ConnectionID != id {
			continue
		}
		empty := ""
		next, err := b.sessions.Update(sess.ID, &empty, nil, nil, nil, nil)
		if err != nil {
			return fmt.Errorf("detach session %s connection %q: %w", sess.ID, id, err)
		}
		updated = append(updated, next)
	}
	connections := b.Connections()
	next := make([]config.Connection, 0, len(connections))
	found := false
	for _, connection := range connections {
		if connection.ID == id {
			found = true
			continue
		}
		next = append(next, connection)
	}
	if !found {
		return fmt.Errorf("%w: %q", ErrConnectionNotFound, id)
	}
	b.SetConnections(next)
	for _, s := range updated {
		b.broadcastSession(ctx, s)
	}
	return nil
}

func (b *Backend) resolveSessionProvider(sessionID string) provider.Provider {
	session, ok := b.sessions.Get(sessionID)
	if !ok || session.ConnectionID == "" {
		return nil
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.providers[session.ConnectionID]
}

func (b *Backend) resolveSessionImageCapability(sessionID, model string) *bool {
	item, ok := b.sessions.Get(sessionID)
	if !ok || item.ConnectionID == "" {
		return nil
	}
	connection, ok := b.Connection(item.ConnectionID)
	if !ok {
		return nil
	}
	settings, configured := connection.ModelSettings[model]
	supported := !configured || settings.ImageInput
	return &supported
}

func (b *Backend) resolveSessionContextWindow(sessionID, model string) *int64 {
	item, ok := b.sessions.Get(sessionID)
	if !ok || item.ConnectionID == "" {
		return nil
	}
	connection, ok := b.Connection(item.ConnectionID)
	if !ok {
		return nil
	}
	window := connection.ModelSettings[model].ContextWindow
	if window <= 0 {
		return nil
	}
	return &window
}

// MemoryCompleter returns the source session's configured model for the
// background memory worker.
func (b *Backend) MemoryCompleter(sessionID string) (provider.Completer, string, string, bool) {
	item, ok := b.sessions.Get(sessionID)
	if !ok {
		return nil, "", "", false
	}
	prov := b.resolveSessionProvider(sessionID)
	completer, ok := prov.(provider.Completer)
	if !ok {
		return nil, "", "", false
	}
	if item.Model == "" {
		return nil, "", "", false
	}
	return completer, item.Model, string(item.ReasoningEffort), true
}

func newConnectionID() string {
	buf := make([]byte, 8)
	_, _ = rand.Read(buf)
	return "conn_" + hex.EncodeToString(buf)
}
