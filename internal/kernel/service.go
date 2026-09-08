package kernel

import (
	"errors"
	"fmt"

	"sync"
	"time"

	"github.com/freesoulcode/foya/internal/agent"
	"github.com/freesoulcode/foya/internal/artifact"
	"github.com/freesoulcode/foya/internal/broker"
	"github.com/freesoulcode/foya/internal/browseruse"
	canvas "github.com/freesoulcode/foya/internal/canvas"
	"github.com/freesoulcode/foya/internal/config"
	"github.com/freesoulcode/foya/internal/contextdata"
	conversation "github.com/freesoulcode/foya/internal/conversation"
	interaction "github.com/freesoulcode/foya/internal/interaction"
	subagent "github.com/freesoulcode/foya/internal/subagent"
	workflow "github.com/freesoulcode/foya/internal/workflow"

	"github.com/freesoulcode/foya/internal/mcpclient"

	model "github.com/freesoulcode/foya/internal/model"
	"github.com/freesoulcode/foya/internal/plugin"
	"github.com/freesoulcode/foya/internal/project"

	"github.com/freesoulcode/foya/internal/skill"

	"github.com/freesoulcode/foya/internal/terminal"
	"github.com/freesoulcode/foya/internal/tool"

	"github.com/freesoulcode/foya/internal/websearch"
)

var (
	ErrConnectionNotFound = fmt.Errorf("connection not found")
	ErrConnectionInUse    = fmt.Errorf("connection is in use by a session")
	ErrUnsupportedAuth    = fmt.Errorf("unsupported connection auth kind")
	ErrGenerationProvider = fmt.Errorf("generation provider failed")
)

// ForkSessionOptions controls user-visible metadata for a copied conversation.
type ForkSessionOptions struct {
	Title      string
	ThroughSeq conversation.Seq
}

// ProviderBuilder creates a provider and its default model from configuration.
type ProviderBuilder func(config.Provider) (model.Provider, string)

// Service is the transport-neutral application entry point.
type Service struct {
	sessions           conversation.Manager
	log                conversation.Store
	bus                *broker.Broker[conversation.Event]
	engine             *agent.Engine
	approval           interaction.Gateway
	terminal           terminal.Manager
	skills             *skill.Manager
	web                *websearch.Manager
	mcp                *mcpclient.Manager
	plugins            *plugin.Manager
	projects           *project.Manager
	agents             *subagent.DefinitionManager
	subagents          *subagent.Manager
	artifacts          artifact.Store
	canvases           *canvas.Store
	context            *contextdata.Store
	commands           *workflow.CommandManager
	workflows          *workflow.Manager
	questions          interaction.QuestionGateway
	backgroundCommands tool.BackgroundCommandManager
	browser            *browseruse.Controller

	buildProvider     ProviderBuilder
	dataDir           string
	homeDir           string
	mu                sync.RWMutex
	projectMu         sync.Mutex
	connections       map[string]config.Connection
	providers         map[string]model.Provider
	firstConnectionID string
	turns             *turnScheduler
	memoryWake        func()
}

const (
	canvasGenerationTimeout      = 5 * time.Minute
	canvasVideoGenerationTimeout = 30 * time.Minute
	canvasMediaNodeHeaderHeight  = 32
)

// SetCapabilityManagers attaches optional capability services assembled by the
// kernel composition root.
func (b *Service) SetCapabilityManagers(skills *skill.Manager, web *websearch.Manager, mcp *mcpclient.Manager) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.skills = skills
	b.web = web
	b.mcp = mcp
}

func (b *Service) SetPluginManager(manager *plugin.Manager) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.plugins = manager
}

func (b *Service) SetBrowserController(controller *browseruse.Controller) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.browser = controller
}

func (b *Service) ResolveBrowserAction(
	sessionID, requestID string,
	result browseruse.ActionResult,
) error {
	if _, ok := b.sessions.Get(sessionID); !ok {
		return conversation.ErrNotFound
	}
	b.mu.RLock()
	controller := b.browser
	b.mu.RUnlock()
	if controller == nil {
		return errors.New("browser use is unavailable")
	}
	return controller.Resolve(sessionID, requestID, result)
}

func (b *Service) SetProjectManager(projects *project.Manager) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.projects = projects
}

func (b *Service) SetAgentManager(agents *subagent.DefinitionManager) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.agents = agents
}

func (b *Service) SetSubAgentManager(manager *subagent.Manager) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.subagents = manager
}

func (b *Service) SetArtifactStore(store artifact.Store) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.artifacts = store
	b.engine.SetArtifactStore(store)
}

func (b *Service) SetCanvasStore(store *canvas.Store) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.canvases = store
}

func (b *Service) canvasStore() (*canvas.Store, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if b.canvases == nil {
		return nil, errors.New("canvas store is unavailable")
	}
	return b.canvases, nil
}

// NewService assembles the transport-neutral application service.
func NewService(
	sessions conversation.Manager,
	log conversation.Store,
	bus *broker.Broker[conversation.Event],
	engine *agent.Engine,
	gw interaction.Gateway,
	terminalManager terminal.Manager,
	build ProviderBuilder,
	provCfg config.Provider,
	dataDir string,
) *Service {
	return &Service{
		sessions:      sessions,
		log:           log,
		bus:           bus,
		engine:        engine,
		approval:      gw,
		terminal:      terminalManager,
		buildProvider: build,
		dataDir:       dataDir,
		connections:   make(map[string]config.Connection),
		providers:     make(map[string]model.Provider),
		turns:         newTurnScheduler(),
	}
}
