// Package channelhub combines the supported messaging channel implementations.
package channelhub

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"

	foyachannel "github.com/freesoulcode/foya/internal/channel"
	"github.com/freesoulcode/foya/internal/channel/feishu"
	"github.com/freesoulcode/foya/internal/channel/telegram"
	"github.com/freesoulcode/foya/internal/interaction"
)

const (
	KindFeishu   = "feishu"
	KindTelegram = "telegram"
)

type State struct {
	ID           string           `json:"id"`
	Kind         string           `json:"kind"`
	Name         string           `json:"name"`
	Locale       string           `json:"locale"`
	Enabled      bool             `json:"enabled"`
	AppID        string           `json:"app_id,omitempty"`
	HasAppSecret bool             `json:"has_app_secret"`
	HasToken     bool             `json:"has_token"`
	ConnectionID string           `json:"connection_id,omitempty"`
	Model        string           `json:"model,omitempty"`
	ProjectID    string           `json:"project_id,omitempty"`
	ApprovalMode interaction.Mode `json:"approval_mode"`
	AllowedUsers []string         `json:"allowed_users"`
	AllowedChats []string         `json:"allowed_chats"`
	AllowAll     bool             `json:"allow_all"`
	Status       string           `json:"status"`
	LastError    string           `json:"last_error,omitempty"`
}

type UpdateInput struct {
	Kind         string           `json:"kind,omitempty"`
	Name         string           `json:"name"`
	Locale       string           `json:"locale,omitempty"`
	Enabled      bool             `json:"enabled"`
	AppID        string           `json:"app_id,omitempty"`
	AppSecret    string           `json:"app_secret,omitempty"`
	Token        string           `json:"token,omitempty"`
	ConnectionID string           `json:"connection_id,omitempty"`
	Model        string           `json:"model,omitempty"`
	ProjectID    string           `json:"project_id,omitempty"`
	ApprovalMode interaction.Mode `json:"approval_mode"`
	AllowedUsers []string         `json:"allowed_users"`
	AllowedChats []string         `json:"allowed_chats"`
	AllowAll     bool             `json:"allow_all"`
}

type Manager struct {
	providers *ProviderRegistry
	bindings  *foyachannel.ConversationBindingStore
	pairings  *foyachannel.ConversationPairingStore
}

func NewManager(
	ctx context.Context,
	dataDir string,
	runtime foyachannel.Runtime,
	bindings *foyachannel.ConversationBindingStore,
	logger *log.Logger,
	autoStart bool,
) (*Manager, error) {
	pairings := foyachannel.NewConversationPairingStore(bindings, runtime)
	feishuManager, err := feishu.NewManager(
		ctx,
		dataDir,
		runtime,
		bindings,
		pairings,
		logger,
		autoStart,
	)
	if err != nil {
		return nil, err
	}
	telegramManager, err := telegram.NewManager(
		ctx,
		dataDir,
		runtime,
		bindings,
		pairings,
		logger,
		autoStart,
	)
	if err != nil {
		feishuManager.Close()
		return nil, err
	}
	providers, err := NewProviderRegistry(
		feishuProvider{manager: feishuManager},
		telegramProvider{manager: telegramManager},
	)
	if err != nil {
		telegramManager.Close()
		feishuManager.Close()
		return nil, err
	}
	return &Manager{
		providers: providers,
		bindings:  bindings,
		pairings:  pairings,
	}, nil
}

func (m *Manager) List() []State {
	result := make([]State, 0)
	for _, provider := range m.providers.All() {
		result = append(result, provider.List()...)
	}
	return result
}

func (m *Manager) Get(id string) (State, bool) {
	for _, provider := range m.providers.All() {
		if item, ok := provider.Get(id); ok {
			return item, true
		}
	}
	return State{}, false
}

func (m *Manager) Create(input UpdateInput) (State, error) {
	provider, ok := m.providers.Get(input.Kind)
	if !ok {
		return State{}, errors.New("unsupported channel kind")
	}
	return provider.Create(input)
}

func (m *Manager) Update(id string, input UpdateInput) (State, error) {
	current, ok := m.Get(id)
	if !ok {
		return State{}, os.ErrNotExist
	}
	if input.Kind != "" && strings.TrimSpace(input.Kind) != current.Kind {
		return State{}, errors.New("channel kind cannot be changed")
	}
	provider, ok := m.providers.Get(current.Kind)
	if !ok {
		return State{}, errors.New("unsupported channel kind")
	}
	return provider.Update(id, input)
}

func (m *Manager) Delete(id string) error {
	current, ok := m.Get(id)
	if !ok {
		return os.ErrNotExist
	}
	provider, ok := m.providers.Get(current.Kind)
	if !ok {
		return errors.New("unsupported channel kind")
	}
	return provider.Delete(id)
}

func (m *Manager) UnbindSession(ctx context.Context, sessionID string) error {
	return m.bindings.UnbindSession(ctx, sessionID)
}

func (m *Manager) BindingForSession(
	ctx context.Context,
	sessionID string,
) (foyachannel.ConversationBinding, error) {
	return m.bindings.ForSession(ctx, sessionID)
}

func (m *Manager) StartPairing(
	channelID, sessionID string,
) (foyachannel.ConversationPairing, error) {
	channel, ok := m.Get(strings.TrimSpace(channelID))
	if !ok {
		return foyachannel.ConversationPairing{}, os.ErrNotExist
	}
	if !channel.Enabled {
		return foyachannel.ConversationPairing{}, errors.New("messaging channel is disabled")
	}
	if channel.Status != "running" {
		if channel.LastError != "" {
			return foyachannel.ConversationPairing{}, fmt.Errorf(
				"messaging channel is unavailable: %s",
				channel.LastError,
			)
		}
		return foyachannel.ConversationPairing{}, errors.New("messaging channel is not running")
	}
	return m.pairings.Create(channel.ID, sessionID)
}

func (m *Manager) GetPairing(
	id string,
) (foyachannel.ConversationPairing, bool) {
	return m.pairings.Get(id)
}

func (m *Manager) CancelPairing(id string) error {
	return m.pairings.Cancel(id)
}

func (m *Manager) StartRegistration(
	input feishu.RegistrationInput,
) (feishu.RegistrationState, error) {
	provider, ok := m.providers.Get(KindFeishu)
	if !ok {
		return feishu.RegistrationState{}, errors.New("Feishu provider is unavailable")
	}
	registration, ok := provider.(feishuRegistrationProvider)
	if !ok {
		return feishu.RegistrationState{}, errors.New("Feishu registration is unavailable")
	}
	return registration.StartRegistration(input)
}

func (m *Manager) GetRegistration(id string) (feishu.RegistrationState, bool) {
	provider, ok := m.providers.Get(KindFeishu)
	if !ok {
		return feishu.RegistrationState{}, false
	}
	registration, ok := provider.(feishuRegistrationProvider)
	if !ok {
		return feishu.RegistrationState{}, false
	}
	return registration.GetRegistration(id)
}

func (m *Manager) CancelRegistration(id string) error {
	provider, ok := m.providers.Get(KindFeishu)
	if !ok {
		return os.ErrNotExist
	}
	registration, ok := provider.(feishuRegistrationProvider)
	if !ok {
		return os.ErrNotExist
	}
	return registration.CancelRegistration(id)
}

func (m *Manager) Close() {
	for _, provider := range m.providers.All() {
		provider.Close()
	}
}

func feishuInput(input UpdateInput) feishu.UpdateInput {
	return feishu.UpdateInput{
		Name:         input.Name,
		Locale:       input.Locale,
		Enabled:      input.Enabled,
		AppID:        input.AppID,
		AppSecret:    input.AppSecret,
		ConnectionID: input.ConnectionID,
		Model:        input.Model,
		ProjectID:    input.ProjectID,
		ApprovalMode: input.ApprovalMode,
		AllowedUsers: input.AllowedUsers,
		AllowedChats: input.AllowedChats,
		AllowAll:     input.AllowAll,
	}
}

func telegramInput(input UpdateInput) telegram.UpdateInput {
	return telegram.UpdateInput{
		Name:         input.Name,
		Locale:       input.Locale,
		Enabled:      input.Enabled,
		Token:        input.Token,
		ConnectionID: input.ConnectionID,
		Model:        input.Model,
		ProjectID:    input.ProjectID,
		ApprovalMode: input.ApprovalMode,
		AllowedUsers: input.AllowedUsers,
		AllowedChats: input.AllowedChats,
		AllowAll:     input.AllowAll,
	}
}

func stateFromFeishu(item feishu.State) State {
	return State{
		ID:           item.ID,
		Kind:         KindFeishu,
		Name:         item.Name,
		Locale:       item.Locale,
		Enabled:      item.Enabled,
		AppID:        item.AppID,
		HasAppSecret: item.HasAppSecret,
		ConnectionID: item.ConnectionID,
		Model:        item.Model,
		ProjectID:    item.ProjectID,
		ApprovalMode: item.ApprovalMode,
		AllowedUsers: append([]string{}, item.AllowedUsers...),
		AllowedChats: append([]string{}, item.AllowedChats...),
		AllowAll:     item.AllowAll,
		Status:       string(item.Status),
		LastError:    item.LastError,
	}
}

func stateFromTelegram(item telegram.State) State {
	return State{
		ID:           item.ID,
		Kind:         KindTelegram,
		Name:         item.Name,
		Locale:       item.Locale,
		Enabled:      item.Enabled,
		HasToken:     item.HasToken,
		ConnectionID: item.ConnectionID,
		Model:        item.Model,
		ProjectID:    item.ProjectID,
		ApprovalMode: item.ApprovalMode,
		AllowedUsers: append([]string{}, item.AllowedUsers...),
		AllowedChats: append([]string{}, item.AllowedChats...),
		AllowAll:     item.AllowAll,
		Status:       string(item.Status),
		LastError:    item.LastError,
	}
}

type feishuRegistrationProvider interface {
	StartRegistration(feishu.RegistrationInput) (feishu.RegistrationState, error)
	GetRegistration(string) (feishu.RegistrationState, bool)
	CancelRegistration(string) error
}

type feishuProvider struct {
	manager *feishu.Manager
}

func (p feishuProvider) Kind() string { return KindFeishu }

func (p feishuProvider) List() []State {
	items := p.manager.List()
	result := make([]State, 0, len(items))
	for _, item := range items {
		result = append(result, stateFromFeishu(item))
	}
	return result
}

func (p feishuProvider) Get(id string) (State, bool) {
	item, ok := p.manager.Get(id)
	return stateFromFeishu(item), ok
}

func (p feishuProvider) Create(input UpdateInput) (State, error) {
	item, err := p.manager.Create(feishuInput(input))
	return stateFromFeishu(item), err
}

func (p feishuProvider) Update(id string, input UpdateInput) (State, error) {
	item, err := p.manager.Update(id, feishuInput(input))
	return stateFromFeishu(item), err
}

func (p feishuProvider) Delete(id string) error { return p.manager.Delete(id) }
func (p feishuProvider) Close()                 { p.manager.Close() }

func (p feishuProvider) StartRegistration(
	input feishu.RegistrationInput,
) (feishu.RegistrationState, error) {
	return p.manager.StartRegistration(input)
}

func (p feishuProvider) GetRegistration(id string) (feishu.RegistrationState, bool) {
	return p.manager.GetRegistration(id)
}

func (p feishuProvider) CancelRegistration(id string) error {
	return p.manager.CancelRegistration(id)
}

type telegramProvider struct {
	manager *telegram.Manager
}

func (p telegramProvider) Kind() string { return KindTelegram }

func (p telegramProvider) List() []State {
	items := p.manager.List()
	result := make([]State, 0, len(items))
	for _, item := range items {
		result = append(result, stateFromTelegram(item))
	}
	return result
}

func (p telegramProvider) Get(id string) (State, bool) {
	item, ok := p.manager.Get(id)
	return stateFromTelegram(item), ok
}

func (p telegramProvider) Create(input UpdateInput) (State, error) {
	item, err := p.manager.Create(telegramInput(input))
	return stateFromTelegram(item), err
}

func (p telegramProvider) Update(id string, input UpdateInput) (State, error) {
	item, err := p.manager.Update(id, telegramInput(input))
	return stateFromTelegram(item), err
}

func (p telegramProvider) Delete(id string) error { return p.manager.Delete(id) }
func (p telegramProvider) Close()                 { p.manager.Close() }
