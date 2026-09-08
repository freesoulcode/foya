package feishu

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
	interaction "github.com/freesoulcode/foya/internal/interaction"
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
	AppID        string           `json:"app_id"`
	AppSecret    string           `json:"app_secret,omitempty"`
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
	AppID        string           `json:"app_id"`
	AppSecret    string           `json:"app_secret,omitempty"`
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
	AppID        string           `json:"app_id"`
	HasAppSecret bool             `json:"has_app_secret"`
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

type channelFactory func(appID, appSecret string) Channel

type managedRun struct {
	status    Status
	lastError string
	cancel    context.CancelFunc
	done      chan struct{}
}

type Manager struct {
	opMu sync.Mutex
	mu   sync.RWMutex

	root           context.Context
	runtime        foyachannel.Runtime
	logger         *log.Logger
	dataDir        string
	catalogPath    string
	channelFactory channelFactory
	settings       map[string]Settings
	order          []string
	runs           map[string]*managedRun
	registrations  map[string]*registrationRun
	registerApp    appRegistrar
}

type persistedCatalog struct {
	Version int        `json:"version"`
	Bots    []Settings `json:"bots"`
}

type legacySettings struct {
	Version int `json:"version"`
	Settings
}

func NewManager(
	ctx context.Context,
	dataDir string,
	runtime foyachannel.Runtime,
	logger *log.Logger,
	autoStart bool,
) (*Manager, error) {
	return newManager(ctx, dataDir, runtime, logger, NewChannel, autoStart)
}

func newManager(
	ctx context.Context,
	dataDir string,
	runtime foyachannel.Runtime,
	logger *log.Logger,
	factory channelFactory,
	autoStart bool,
) (*Manager, error) {
	if ctx == nil {
		return nil, errors.New("Feishu channel manager context is required")
	}
	if runtime == nil {
		return nil, errors.New("Feishu channel runtime is required")
	}
	if logger == nil {
		logger = log.Default()
	}
	catalogPath := filepath.Join(dataDir, "feishu-channels.json")
	items, migrated, err := loadCatalog(catalogPath, filepath.Join(dataDir, "feishu-bot.json"))
	if err != nil {
		return nil, fmt.Errorf("load Feishu channel settings: %w", err)
	}
	manager := &Manager{
		root:           ctx,
		runtime:        runtime,
		logger:         logger,
		dataDir:        dataDir,
		catalogPath:    catalogPath,
		channelFactory: factory,
		settings:       make(map[string]Settings, len(items)),
		runs:           make(map[string]*managedRun),
		registrations:  make(map[string]*registrationRun),
		registerApp:    defaultAppRegistrar,
	}
	for _, item := range items {
		manager.settings[item.ID] = item
		manager.order = append(manager.order, item.ID)
	}
	if migrated {
		if err := saveCatalog(catalogPath, items); err != nil {
			return nil, err
		}
		if len(items) == 1 {
			_ = migrateSessionStore(
				filepath.Join(dataDir, "feishu-sessions.json"),
				manager.sessionPath(items[0].ID),
			)
		}
	}
	if autoStart {
		for _, id := range manager.order {
			if manager.settings[id].Enabled {
				if err := manager.start(id); err != nil {
					manager.runs[id] = &managedRun{status: StatusError, lastError: err.Error()}
					logger.Printf("start persisted Feishu channel %s: %v", id, err)
				}
			}
		}
	}
	return manager, nil
}

func (m *Manager) List() []State {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]State, 0, len(m.order))
	for _, id := range m.order {
		result = append(result, m.stateLocked(id))
	}
	return result
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
		settings.Name = fmt.Sprintf("Feishu Bot %d", len(m.order)+1)
	}
	if err := validateSettings(settings); err != nil {
		return State{}, err
	}
	if err := m.validateUniqueAppID("", settings.AppID); err != nil {
		return State{}, err
	}

	m.mu.RLock()
	items := m.catalogLocked(Settings{}, false)
	m.mu.RUnlock()
	items = append(items, settings)
	if err := saveCatalog(m.catalogPath, items); err != nil {
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
	settings := settingsFromInput(id, input, current.AppSecret)
	if settings.Name == "" {
		settings.Name = current.Name
	}
	if err := validateSettings(settings); err != nil {
		return State{}, err
	}
	if err := m.validateUniqueAppID(id, settings.AppID); err != nil {
		return State{}, err
	}

	m.mu.RLock()
	items := m.catalogLocked(settings, true)
	m.mu.RUnlock()
	if err := saveCatalog(m.catalogPath, items); err != nil {
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
	if err := saveCatalog(m.catalogPath, items); err != nil {
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
	_ = os.Remove(m.sessionPath(id))
	return nil
}

func (m *Manager) Close() {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	m.mu.Lock()
	ids := append([]string(nil), m.order...)
	registrationCancels := make([]context.CancelFunc, 0, len(m.registrations))
	for _, run := range m.registrations {
		if run.cancel != nil {
			registrationCancels = append(registrationCancels, run.cancel)
		}
	}
	m.mu.Unlock()
	for _, cancel := range registrationCancels {
		cancel()
	}
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
		ConnectionID: settings.ConnectionID,
		Model:        settings.Model,
		ProjectID:    settings.ProjectID,
		ApprovalMode: settings.ApprovalMode,
		Locale:       settings.Locale,
		SessionPath:  m.sessionPath(id),
		AllowedUsers: settings.AllowedUsers,
		AllowedChats: settings.AllowedChats,
		AllowAll:     settings.AllowAll,
	}, m.runtime, m.channelFactory(settings.AppID, settings.AppSecret), m.logger)
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
	return stateFromSettings(settings, status, lastError)
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

func (m *Manager) sessionPath(id string) string {
	return filepath.Join(m.dataDir, "channels", "feishu", id+"-sessions.json")
}

func (m *Manager) validateUniqueAppID(excludedID, appID string) error {
	if appID == "" {
		return nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	for id, item := range m.settings {
		if id != excludedID && item.AppID == appID {
			return errors.New("a Feishu channel with this app ID already exists")
		}
	}
	return nil
}

func loadCatalog(catalogPath, legacyPath string) ([]Settings, bool, error) {
	data, err := os.ReadFile(catalogPath)
	if err == nil {
		var persisted persistedCatalog
		decoder := json.NewDecoder(strings.NewReader(string(data)))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&persisted); err != nil {
			return nil, false, err
		}
		if persisted.Version != 1 {
			return nil, false, errors.New("unsupported Feishu channel catalog version")
		}
		seen := make(map[string]struct{}, len(persisted.Bots))
		appIDs := make(map[string]struct{}, len(persisted.Bots))
		for index := range persisted.Bots {
			item := normalizeSettings(persisted.Bots[index])
			if item.ID == "" {
				return nil, false, errors.New("Feishu channel ID is required")
			}
			if _, exists := seen[item.ID]; exists {
				return nil, false, errors.New("duplicate Feishu channel ID")
			}
			if err := validateSettings(item); err != nil {
				return nil, false, err
			}
			if item.AppID != "" {
				if _, exists := appIDs[item.AppID]; exists {
					return nil, false, errors.New("duplicate Feishu app ID")
				}
				appIDs[item.AppID] = struct{}{}
			}
			seen[item.ID] = struct{}{}
			persisted.Bots[index] = item
		}
		return persisted.Bots, false, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, false, err
	}

	data, err = os.ReadFile(legacyPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	var persisted legacySettings
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&persisted); err != nil {
		return nil, false, err
	}
	if persisted.Version != 1 {
		return nil, false, errors.New("unsupported Feishu bot settings version")
	}
	id, err := newChannelID()
	if err != nil {
		return nil, false, err
	}
	settings := normalizeSettings(persisted.Settings)
	settings.ID = id
	settings.Name = "Feishu Bot"
	if err := validateSettings(settings); err != nil {
		return nil, false, err
	}
	return []Settings{settings}, true, nil
}

func saveCatalog(path string, items []Settings) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(persistedCatalog{Version: 1, Bots: items}, "", "  ")
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

func migrateSessionStore(source, destination string) error {
	if _, err := os.Stat(destination); err == nil {
		return nil
	}
	data, err := os.ReadFile(source)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return err
	}
	return os.WriteFile(destination, data, 0o600)
}

func settingsFromInput(id string, input UpdateInput, existingSecret string) Settings {
	secret := existingSecret
	if strings.TrimSpace(input.AppSecret) != "" {
		secret = strings.TrimSpace(input.AppSecret)
	}
	return normalizeSettings(Settings{
		ID:           id,
		Name:         input.Name,
		Locale:       input.Locale,
		Enabled:      input.Enabled,
		AppID:        input.AppID,
		AppSecret:    secret,
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
	settings.AppID = strings.TrimSpace(settings.AppID)
	settings.AppSecret = strings.TrimSpace(settings.AppSecret)
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
		return errors.New("Feishu channel approval mode must be auto or full_access")
	}
	if !settings.Enabled {
		return nil
	}
	if settings.AppID == "" {
		return errors.New("Feishu app ID is required")
	}
	if settings.AppSecret == "" {
		return errors.New("Feishu app secret is required")
	}
	if !settings.AllowAll && len(settings.AllowedUsers) == 0 && len(settings.AllowedChats) == 0 {
		return errors.New("configure at least one allowed user or chat, or explicitly enable allow-all")
	}
	return nil
}

func stateFromSettings(settings Settings, status Status, lastError string) State {
	return State{
		ID:           settings.ID,
		Kind:         "feishu",
		Name:         settings.Name,
		Locale:       settings.Locale,
		Enabled:      settings.Enabled,
		AppID:        settings.AppID,
		HasAppSecret: settings.AppSecret != "",
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

func newChannelID() (string, error) {
	raw := make([]byte, 8)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return "feishu-" + hex.EncodeToString(raw), nil
}
