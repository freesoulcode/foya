package feishu

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/freesoulcode/foya/internal/approval"
	larkregistration "github.com/larksuite/oapi-sdk-go/v3/scene/registration"
)

type RegistrationStatus string

const (
	RegistrationStarting   RegistrationStatus = "starting"
	RegistrationPending    RegistrationStatus = "pending"
	RegistrationCompleting RegistrationStatus = "completing"
	RegistrationCompleted  RegistrationStatus = "completed"
	RegistrationDenied     RegistrationStatus = "denied"
	RegistrationExpired    RegistrationStatus = "expired"
	RegistrationCancelled  RegistrationStatus = "cancelled"
	RegistrationError      RegistrationStatus = "error"

	registrationRetention = time.Hour
)

type RegistrationInput struct {
	Name         string        `json:"name"`
	Locale       string        `json:"locale,omitempty"`
	ConnectionID string        `json:"connection_id,omitempty"`
	Model        string        `json:"model,omitempty"`
	ProjectID    string        `json:"project_id,omitempty"`
	ApprovalMode approval.Mode `json:"approval_mode"`
	AllowedUsers []string      `json:"allowed_users"`
	AllowedChats []string      `json:"allowed_chats"`
	AllowAll     bool          `json:"allow_all"`
}

type RegistrationState struct {
	ID        string             `json:"id"`
	Status    RegistrationStatus `json:"status"`
	QRCodeURL string             `json:"qr_code_url,omitempty"`
	ExpiresAt string             `json:"expires_at,omitempty"`
	Channel   *State             `json:"channel,omitempty"`
	Error     string             `json:"error,omitempty"`
}

type appRegistrar func(
	context.Context,
	*larkregistration.Options,
) (*larkregistration.RegisterAppResult, error)

type registrationRun struct {
	state     RegistrationState
	cancel    context.CancelFunc
	updatedAt time.Time
}

func defaultAppRegistrar(
	ctx context.Context,
	options *larkregistration.Options,
) (*larkregistration.RegisterAppResult, error) {
	return larkregistration.RegisterApp(ctx, options)
}

func (m *Manager) StartRegistration(input RegistrationInput) (RegistrationState, error) {
	input = normalizeRegistrationInput(input)
	if input.ApprovalMode != approval.ModeAuto &&
		input.ApprovalMode != approval.ModeFullAccess {
		return RegistrationState{}, errors.New("Feishu channel approval mode must be auto or full_access")
	}

	id, err := newRegistrationID()
	if err != nil {
		return RegistrationState{}, err
	}
	ctx, cancel := context.WithCancel(m.root)
	now := time.Now()
	run := &registrationRun{
		state: RegistrationState{
			ID:     id,
			Status: RegistrationStarting,
		},
		cancel:    cancel,
		updatedAt: now,
	}

	m.mu.Lock()
	m.pruneRegistrationsLocked(now)
	m.registrations[id] = run
	m.mu.Unlock()

	state := run.state
	go m.runRegistration(ctx, id, input)
	return state, nil
}

func (m *Manager) GetRegistration(id string) (RegistrationState, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	run, ok := m.registrations[id]
	if !ok {
		return RegistrationState{}, false
	}
	return cloneRegistrationState(run.state), true
}

func (m *Manager) CancelRegistration(id string) error {
	m.mu.Lock()
	run, ok := m.registrations[id]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("%w: Feishu registration %q", os.ErrNotExist, id)
	}
	if run.state.Status == RegistrationCompleting || registrationFinished(run.state.Status) {
		m.mu.Unlock()
		return nil
	}
	run.state.Status = RegistrationCancelled
	run.state.Error = ""
	run.updatedAt = time.Now()
	cancel := run.cancel
	run.cancel = nil
	m.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return nil
}

func (m *Manager) runRegistration(ctx context.Context, id string, input RegistrationInput) {
	minimalPreset := false
	options := &larkregistration.Options{
		Source:     "foya",
		CreateOnly: true,
		AppPreset: &larkregistration.AppPreset{
			Name: input.Name,
			Desc: localizedMessage(input.Locale, "app_description"),
		},
		Addons: &larkregistration.AppAddons{
			Preset: &minimalPreset,
			Scopes: larkregistration.AppAddonsScopes{
				Tenant: []string{
					"application:bot.basic_info:read",
					"im:message:send_as_bot",
					"im:message.group_at_msg:readonly",
					"im:message.p2p_msg:readonly",
					"im:message.reactions:write_only",
					"im:resource",
				},
			},
			Events: larkregistration.AppAddonsEvents{
				Items: larkregistration.AppAddonsEventItems{
					Tenant: []string{"im.message.receive_v1"},
				},
			},
		},
		OnQRCode: func(info *larkregistration.QRCodeInfo) {
			m.mu.Lock()
			defer m.mu.Unlock()
			run, ok := m.registrations[id]
			if !ok || registrationFinished(run.state.Status) {
				return
			}
			run.state.Status = RegistrationPending
			run.state.QRCodeURL = info.URL
			run.state.ExpiresAt = time.Now().
				Add(time.Duration(info.ExpireIn) * time.Second).
				UTC().
				Format(time.RFC3339)
			run.updatedAt = time.Now()
		},
	}

	result, err := m.registerApp(ctx, options)
	if err != nil {
		m.finishRegistrationError(id, err)
		return
	}
	if result == nil || strings.TrimSpace(result.ClientID) == "" ||
		strings.TrimSpace(result.ClientSecret) == "" {
		m.finishRegistrationError(
			id,
			errors.New(localizedMessage(input.Locale, "missing_credentials")),
		)
		return
	}
	if result.UserInfo != nil && result.UserInfo.TenantBrand != "" &&
		result.UserInfo.TenantBrand != "feishu" {
		m.finishRegistrationError(
			id,
			errors.New(localizedMessage(input.Locale, "unsupported_account")),
		)
		return
	}

	m.mu.Lock()
	run, ok := m.registrations[id]
	if !ok || registrationFinished(run.state.Status) {
		m.mu.Unlock()
		return
	}
	run.state.Status = RegistrationCompleting
	run.state.QRCodeURL = ""
	run.updatedAt = time.Now()
	m.mu.Unlock()

	if result.UserInfo != nil && result.UserInfo.OpenID != "" {
		input.AllowedUsers = append(input.AllowedUsers, result.UserInfo.OpenID)
	}
	input.AllowedUsers = cleanIDs(input.AllowedUsers)
	enabled := input.AllowAll || len(input.AllowedUsers) > 0 || len(input.AllowedChats) > 0
	channel, createErr := m.Create(UpdateInput{
		Name:         input.Name,
		Locale:       input.Locale,
		Enabled:      enabled,
		AppID:        result.ClientID,
		AppSecret:    result.ClientSecret,
		ConnectionID: input.ConnectionID,
		Model:        input.Model,
		ProjectID:    input.ProjectID,
		ApprovalMode: input.ApprovalMode,
		AllowedUsers: input.AllowedUsers,
		AllowedChats: input.AllowedChats,
		AllowAll:     input.AllowAll,
	})
	if createErr != nil && channel.ID == "" {
		m.finishRegistrationError(id, createErr)
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	run, ok = m.registrations[id]
	if !ok || run.state.Status == RegistrationCancelled {
		return
	}
	run.state.Status = RegistrationCompleted
	run.state.Channel = &channel
	run.state.Error = ""
	if createErr != nil {
		run.state.Error = createErr.Error()
	}
	run.cancel = nil
	run.updatedAt = time.Now()
}

func (m *Manager) finishRegistrationError(id string, cause error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	run, ok := m.registrations[id]
	if !ok || run.state.Status == RegistrationCancelled {
		return
	}
	var denied *larkregistration.AccessDeniedError
	var expired *larkregistration.ExpiredError
	switch {
	case errors.Is(cause, context.Canceled):
		run.state.Status = RegistrationCancelled
	case errors.As(cause, &denied):
		run.state.Status = RegistrationDenied
	case errors.As(cause, &expired),
		errors.Is(cause, context.DeadlineExceeded):
		run.state.Status = RegistrationExpired
	default:
		run.state.Status = RegistrationError
	}
	if run.state.Status != RegistrationCancelled {
		run.state.Error = cause.Error()
	}
	run.state.QRCodeURL = ""
	run.cancel = nil
	run.updatedAt = time.Now()
}

func (m *Manager) pruneRegistrationsLocked(now time.Time) {
	for id, run := range m.registrations {
		if registrationFinished(run.state.Status) &&
			now.Sub(run.updatedAt) >= registrationRetention {
			delete(m.registrations, id)
		}
	}
}

func registrationFinished(status RegistrationStatus) bool {
	switch status {
	case RegistrationCompleted, RegistrationDenied, RegistrationExpired,
		RegistrationCancelled, RegistrationError:
		return true
	default:
		return false
	}
}

func normalizeRegistrationInput(input RegistrationInput) RegistrationInput {
	input.Name = strings.TrimSpace(input.Name)
	if input.Name == "" {
		input.Name = "Feishu Bot"
	}
	input.ConnectionID = strings.TrimSpace(input.ConnectionID)
	input.Model = strings.TrimSpace(input.Model)
	input.ProjectID = strings.TrimSpace(input.ProjectID)
	input.Locale = strings.TrimSpace(input.Locale)
	if input.Locale != "en-US" && input.Locale != "zh-CN" {
		input.Locale = "zh-CN"
	}
	input.AllowedUsers = cleanIDs(input.AllowedUsers)
	input.AllowedChats = cleanIDs(input.AllowedChats)
	if input.ApprovalMode == "" {
		input.ApprovalMode = approval.ModeAuto
	}
	return input
}

func cloneRegistrationState(state RegistrationState) RegistrationState {
	if state.Channel != nil {
		channel := *state.Channel
		channel.AllowedUsers = append([]string(nil), channel.AllowedUsers...)
		channel.AllowedChats = append([]string(nil), channel.AllowedChats...)
		state.Channel = &channel
	}
	return state
}

func newRegistrationID() (string, error) {
	raw := make([]byte, 8)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate Feishu registration ID: %w", err)
	}
	return "feishu-registration-" + hex.EncodeToString(raw), nil
}
