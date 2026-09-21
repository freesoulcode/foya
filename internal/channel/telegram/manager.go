package telegram

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"

	foyachannel "github.com/freesoulcode/foya/internal/channel"
	"github.com/freesoulcode/foya/internal/interaction"
)

type Status string

const (
	StatusStopped Status = "stopped"
	StatusRunning Status = "running"
	StatusError   Status = "error"
)

type Settings struct {
	ID           string           `json:"id"`
	Name         string           `json:"name"`
	Locale       string           `json:"locale,omitempty"`
	Enabled      bool             `json:"enabled"`
	Token        string           `json:"token,omitempty"`
	ConnectionID string           `json:"connection_id,omitempty"`
	Model        string           `json:"model,omitempty"`
	ProjectID    string           `json:"project_id,omitempty"`
	ApprovalMode interaction.Mode `json:"approval_mode"`
	AllowedUsers []string         `json:"allowed_users"`
	AllowedChats []string         `json:"allowed_chats"`
	AllowAll     bool             `json:"allow_all"`
}

type UpdateInput struct {
	Name         string           `json:"name"`
	Locale       string           `json:"locale,omitempty"`
	Enabled      bool             `json:"enabled"`
	Token        string           `json:"token,omitempty"`
	ConnectionID string           `json:"connection_id,omitempty"`
	Model        string           `json:"model,omitempty"`
	ProjectID    string           `json:"project_id,omitempty"`
	ApprovalMode interaction.Mode `json:"approval_mode"`
	AllowedUsers []string         `json:"allowed_users"`
	AllowedChats []string         `json:"allowed_chats"`
	AllowAll     bool             `json:"allow_all"`
}

type State struct {
	ID           string           `json:"id"`
	Kind         string           `json:"kind"`
	Name         string           `json:"name"`
	Locale       string           `json:"locale"`
	Enabled      bool             `json:"enabled"`
	HasToken     bool             `json:"has_token"`
	ConnectionID string           `json:"connection_id,omitempty"`
	Model        string           `json:"model,omitempty"`
	ProjectID    string           `json:"project_id,omitempty"`
	ApprovalMode interaction.Mode `json:"approval_mode"`
	AllowedUsers []string         `json:"allowed_users"`
	AllowedChats []string         `json:"allowed_chats"`
	AllowAll     bool             `json:"allow_all"`
	Status       Status           `json:"status"`
	LastError    string           `json:"last_error,omitempty"`
}

type apiFactory func(string) API

type managedRun struct {
	status    Status
	lastError string
	cancel    context.CancelFunc
	done      chan struct{}
}

type Manager struct {
	opMu sync.Mutex
	mu   sync.RWMutex

	root       context.Context
	runtime    foyachannel.Runtime
	bindings   *foyachannel.ConversationBindingStore
	pairings   *foyachannel.ConversationPairingStore
	logger     *log.Logger
	path       string
	apiFactory apiFactory
	settings   map[string]Settings
	order      []string
	runs       map[string]*managedRun
}

type persistedCatalog struct {
	Version  int        `json:"version"`
	Channels []Settings `json:"channels"`
}

func NewManager(
	ctx context.Context,
	dataDir string,
	runtime foyachannel.Runtime,
	bindings *foyachannel.ConversationBindingStore,
	pairings *foyachannel.ConversationPairingStore,
	logger *log.Logger,
	autoStart bool,
) (*Manager, error) {
	return newManager(
		ctx,
		dataDir,
		runtime,
		bindings,
		pairings,
		logger,
		func(token string) API { return NewClient(token) },
		autoStart,
	)
}

func newManager(
	ctx context.Context,
	dataDir string,
	runtime foyachannel.Runtime,
	bindings *foyachannel.ConversationBindingStore,
	pairings *foyachannel.ConversationPairingStore,
	logger *log.Logger,
	factory apiFactory,
	autoStart bool,
) (*Manager, error) {
	if ctx == nil {
		return nil, errors.New("Telegram channel manager context is required")
	}
	if runtime == nil {
		return nil, errors.New("Telegram channel runtime is required")
	}
	if bindings == nil {
		return nil, errors.New("Telegram conversation bindings are required")
	}
	if pairings == nil {
		return nil, errors.New("Telegram conversation pairings are required")
	}
	if logger == nil {
		logger = log.Default()
	}
	path := filepath.Join(dataDir, "telegram-channels.json")
	items, err := loadCatalog(path)
	if err != nil {
		return nil, fmt.Errorf("load Telegram channel settings: %w", err)
	}
	manager := &Manager{
		root:       ctx,
		runtime:    runtime,
		bindings:   bindings,
		pairings:   pairings,
		logger:     logger,
		path:       path,
		apiFactory: factory,
		settings:   make(map[string]Settings, len(items)),
		runs:       make(map[string]*managedRun),
	}
	for _, item := range items {
		manager.settings[item.ID] = item
		manager.order = append(manager.order, item.ID)
	}
	if autoStart {
		for _, id := range manager.order {
			if manager.settings[id].Enabled {
				if err := manager.start(id); err != nil {
					manager.runs[id] = &managedRun{
						status: StatusError, lastError: err.Error(),
					}
					logger.Printf("start persisted Telegram channel %s: %v", id, err)
				}
			}
		}
	}
	return manager, nil
}

func (m *Manager) List() []State {
	m.mu.RLock()
	defer m.mu.RUnlock()
	items := make([]State, 0, len(m.order))
	for _, id := range m.order {
		items = append(items, m.stateLocked(id))
	}
	return items
}

func (m *Manager) Get(id string) (State, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if _, ok := m.settings[id]; !ok {
		return State{}, false
	}
	return m.stateLocked(id), true
}

func (m *Manager) Create(input UpdateInput) (State, error) {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	id, err := newChannelID()
	if err != nil {
		return State{}, err
	}
	settings := settingsFromInput(id, input, "")
	if settings.Name == "" {
		settings.Name = fmt.Sprintf("Telegram %d", len(m.order)+1)
	}
	if err := validateSettings(settings); err != nil {
		return State{}, err
	}
	if err := m.validateUniqueToken("", settings.Token); err != nil {
		return State{}, err
	}
	m.mu.RLock()
	items := m.catalogLocked(Settings{}, false)
	m.mu.RUnlock()
	items = append(items, settings)
	if err := saveCatalog(m.path, items); err != nil {
		return State{}, err
	}
	m.mu.Lock()
	m.settings[id] = settings
	m.order = append(m.order, id)
	m.mu.Unlock()
	if settings.Enabled {
		if err := m.start(id); err != nil {
			m.mu.Lock()
			m.runs[id] = &managedRun{status: StatusError, lastError: err.Error()}
			m.mu.Unlock()
			return m.state(id), err
		}
	}
	return m.state(id), nil
}

func (m *Manager) Update(id string, input UpdateInput) (State, error) {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	m.mu.RLock()
	current, ok := m.settings[id]
	m.mu.RUnlock()
	if !ok {
		return State{}, os.ErrNotExist
	}
	settings := settingsFromInput(id, input, current.Token)
	if settings.Name == "" {
		settings.Name = current.Name
	}
	if err := validateSettings(settings); err != nil {
		return State{}, err
	}
	if err := m.validateUniqueToken(id, settings.Token); err != nil {
		return State{}, err
	}
	m.mu.RLock()
	items := m.catalogLocked(settings, true)
	m.mu.RUnlock()
	if err := saveCatalog(m.path, items); err != nil {
		return State{}, err
	}
	m.stopCurrent(id)
	m.mu.Lock()
	m.settings[id] = settings
	m.mu.Unlock()
	if settings.Enabled {
		if err := m.start(id); err != nil {
			m.mu.Lock()
			m.runs[id] = &managedRun{status: StatusError, lastError: err.Error()}
			m.mu.Unlock()
			return m.state(id), err
		}
	}
	return m.state(id), nil
}

func (m *Manager) Delete(id string) error {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	m.mu.RLock()
	if _, ok := m.settings[id]; !ok {
		m.mu.RUnlock()
		return os.ErrNotExist
	}
	items := make([]Settings, 0, len(m.order)-1)
	for _, itemID := range m.order {
		if itemID != id {
			items = append(items, m.settings[itemID])
		}
	}
	m.mu.RUnlock()
	if err := saveCatalog(m.path, items); err != nil {
		return err
	}
	m.stopCurrent(id)
	m.mu.Lock()
	delete(m.settings, id)
	for index, itemID := range m.order {
		if itemID == id {
			m.order = append(m.order[:index], m.order[index+1:]...)
			break
		}
	}
	m.mu.Unlock()
	return m.bindings.DeleteChannel(context.Background(), id)
}

func (m *Manager) Close() {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	m.mu.RLock()
	ids := append([]string(nil), m.order...)
	m.mu.RUnlock()
	for _, id := range ids {
		m.stopCurrent(id)
	}
}

func (m *Manager) start(id string) error {
	m.mu.RLock()
	settings, ok := m.settings[id]
	m.mu.RUnlock()
	if !ok {
		return os.ErrNotExist
	}
	if err := validateSettings(settings); err != nil {
		return err
	}
	bot, err := New(Config{
		ChannelID:    settings.ID,
		ConnectionID: settings.ConnectionID,
		Model:        settings.Model,
		ProjectID:    settings.ProjectID,
		ApprovalMode: settings.ApprovalMode,
		Locale:       settings.Locale,
		AllowedUsers: settings.AllowedUsers,
		AllowedChats: settings.AllowedChats,
		AllowAll:     settings.AllowAll,
	}, m.runtime, m.bindings, m.pairings, m.apiFactory(settings.Token), m.logger)
	if err != nil {
		return err
	}
	runCtx, cancel := context.WithCancel(m.root)
	run := &managedRun{status: StatusRunning, cancel: cancel, done: make(chan struct{})}
	m.mu.Lock()
	m.runs[id] = run
	m.mu.Unlock()
	go func() {
		runErr := bot.Run(runCtx)
		m.mu.Lock()
		if m.runs[id] == run {
			run.cancel = nil
			if runErr != nil && m.root.Err() == nil {
				run.status = StatusError
				run.lastError = runErr.Error()
			} else {
				run.status = StatusStopped
			}
		}
		m.mu.Unlock()
		close(run.done)
	}()
	return nil
}

func (m *Manager) stopCurrent(id string) {
	m.mu.Lock()
	run := m.runs[id]
	delete(m.runs, id)
	m.mu.Unlock()
	if run == nil || run.cancel == nil {
		return
	}
	run.cancel()
	<-run.done
}

func (m *Manager) state(id string) State {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.stateLocked(id)
}

func (m *Manager) stateLocked(id string) State {
	settings := m.settings[id]
	status := StatusStopped
	lastError := ""
	if run := m.runs[id]; run != nil {
		status = run.status
		lastError = run.lastError
	}
	return State{
		ID:           settings.ID,
		Kind:         "telegram",
		Name:         settings.Name,
		Locale:       settings.Locale,
		Enabled:      settings.Enabled,
		HasToken:     settings.Token != "",
		ConnectionID: settings.ConnectionID,
		Model:        settings.Model,
		ProjectID:    settings.ProjectID,
		ApprovalMode: settings.ApprovalMode,
		AllowedUsers: append([]string(nil), settings.AllowedUsers...),
		AllowedChats: append([]string(nil), settings.AllowedChats...),
		AllowAll:     settings.AllowAll,
		Status:       status,
		LastError:    lastError,
	}
}

func (m *Manager) catalogLocked(replacement Settings, replace bool) []Settings {
	items := make([]Settings, 0, len(m.order))
	for _, id := range m.order {
		if replace && id == replacement.ID {
			items = append(items, replacement)
		} else {
			items = append(items, m.settings[id])
		}
	}
	return items
}

func (m *Manager) validateUniqueToken(excludedID, token string) error {
	if token == "" {
		return nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	for id, item := range m.settings {
		if id != excludedID && item.Token == token {
			return errors.New("a Telegram channel with this token already exists")
		}
	}
	return nil
}

func loadCatalog(path string) ([]Settings, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var persisted persistedCatalog
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&persisted); err != nil {
		return nil, err
	}
	if persisted.Version != 1 {
		return nil, errors.New("unsupported Telegram channel catalog version")
	}
	seen := make(map[string]struct{}, len(persisted.Channels))
	tokens := make(map[string]struct{}, len(persisted.Channels))
	for index := range persisted.Channels {
		item := normalizeSettings(persisted.Channels[index])
		if item.ID == "" {
			return nil, errors.New("Telegram channel ID is required")
		}
		if _, exists := seen[item.ID]; exists {
			return nil, errors.New("duplicate Telegram channel ID")
		}
		if err := validateSettings(item); err != nil {
			return nil, err
		}
		if item.Token != "" {
			if _, exists := tokens[item.Token]; exists {
				return nil, errors.New("duplicate Telegram bot token")
			}
			tokens[item.Token] = struct{}{}
		}
		seen[item.ID] = struct{}{}
		persisted.Channels[index] = item
	}
	return persisted.Channels, nil
}

func saveCatalog(path string, items []Settings) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(persistedCatalog{
		Version: 1, Channels: items,
	}, "", "  ")
	if err != nil {
		return err
	}
	temp := path + ".tmp"
	if err := os.WriteFile(temp, data, 0o600); err != nil {
		return err
	}
	if err := os.Chmod(temp, 0o600); err != nil {
		return err
	}
	return os.Rename(temp, path)
}

func settingsFromInput(id string, input UpdateInput, existingToken string) Settings {
	token := existingToken
	if strings.TrimSpace(input.Token) != "" {
		token = strings.TrimSpace(input.Token)
	}
	return normalizeSettings(Settings{
		ID:           id,
		Name:         input.Name,
		Locale:       input.Locale,
		Enabled:      input.Enabled,
		Token:        token,
		ConnectionID: input.ConnectionID,
		Model:        input.Model,
		ProjectID:    input.ProjectID,
		ApprovalMode: input.ApprovalMode,
		AllowedUsers: input.AllowedUsers,
		AllowedChats: input.AllowedChats,
		AllowAll:     input.AllowAll,
	})
}

func normalizeSettings(settings Settings) Settings {
	settings.ID = strings.TrimSpace(settings.ID)
	settings.Name = strings.TrimSpace(settings.Name)
	settings.Locale = strings.TrimSpace(settings.Locale)
	settings.Token = strings.TrimSpace(settings.Token)
	settings.ConnectionID = strings.TrimSpace(settings.ConnectionID)
	settings.Model = strings.TrimSpace(settings.Model)
	settings.ProjectID = strings.TrimSpace(settings.ProjectID)
	settings.AllowedUsers = cleanIDs(settings.AllowedUsers)
	settings.AllowedChats = cleanIDs(settings.AllowedChats)
	if settings.ApprovalMode == "" {
		settings.ApprovalMode = interaction.ModeAuto
	}
	if settings.Locale != "en-US" && settings.Locale != "zh-CN" {
		settings.Locale = "zh-CN"
	}
	return settings
}

func validateSettings(settings Settings) error {
	if settings.ApprovalMode != interaction.ModeAuto &&
		settings.ApprovalMode != interaction.ModeFullAccess {
		return errors.New("Telegram channel approval mode must be auto or full_access")
	}
	if !settings.Enabled {
		return nil
	}
	if settings.Token == "" {
		return errors.New("Telegram bot token is required")
	}
	return nil
}

func newChannelID() (string, error) {
	raw := make([]byte, 8)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return "telegram-" + hex.EncodeToString(raw), nil
}
