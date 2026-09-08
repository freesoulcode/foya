package server

import (
	"net/http"
	"os"
	"testing"

	"github.com/freesoulcode/foya/internal/channel/feishu"
	interaction "github.com/freesoulcode/foya/internal/interaction"
)

func TestFeishuBotSettingsAPI(t *testing.T) {
	manager := &fakeFeishuBotManager{state: feishu.State{
		Enabled:      false,
		AppID:        "cli_test",
		HasAppSecret: true,
		ApprovalMode: interaction.ModeAuto,
		Status:       feishu.StatusStopped,
	}}
	server := &Server{channels: manager, mux: http.NewServeMux()}
	server.routes()

	var current feishu.State
	if code := requestJSON(
		t,
		server.Handler(),
		http.MethodGet,
		"/settings/feishu-bot",
		nil,
		&current,
	); code != http.StatusOK {
		t.Fatalf("get status = %d, want %d", code, http.StatusOK)
	}
	if !current.HasAppSecret || current.AppID != "cli_test" {
		t.Fatalf("current state = %#v", current)
	}

	var updated feishu.State
	if code := requestJSON(
		t,
		server.Handler(),
		http.MethodPut,
		"/settings/feishu-bot",
		feishu.UpdateInput{
			Enabled:      true,
			AppID:        "cli_updated",
			ApprovalMode: interaction.ModeAuto,
			AllowAll:     true,
		},
		&updated,
	); code != http.StatusOK {
		t.Fatalf("update status = %d, want %d", code, http.StatusOK)
	}
	if !manager.input.Enabled || manager.input.AppID != "cli_updated" {
		t.Fatalf("update input = %#v", manager.input)
	}
}

func TestFeishuBotSettingsAPIUnavailable(t *testing.T) {
	server := &Server{mux: http.NewServeMux()}
	server.routes()
	if code := requestJSON(
		t,
		server.Handler(),
		http.MethodGet,
		"/settings/feishu-bot",
		nil,
		nil,
	); code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", code, http.StatusServiceUnavailable)
	}
}

func TestFeishuRegistrationAPI(t *testing.T) {
	manager := &fakeFeishuBotManager{}
	server := &Server{channels: manager, mux: http.NewServeMux()}
	server.routes()

	var started feishu.RegistrationState
	if code := requestJSON(
		t,
		server.Handler(),
		http.MethodPost,
		"/channels/feishu/registrations",
		feishu.RegistrationInput{Name: "Foya Agent"},
		&started,
	); code != http.StatusAccepted {
		t.Fatalf("start status = %d, want %d", code, http.StatusAccepted)
	}
	if started.Status != feishu.RegistrationPending || started.QRCodeURL == "" {
		t.Fatalf("started registration = %#v", started)
	}

	var current feishu.RegistrationState
	if code := requestJSON(
		t,
		server.Handler(),
		http.MethodGet,
		"/channels/feishu/registrations/"+started.ID,
		nil,
		&current,
	); code != http.StatusOK {
		t.Fatalf("get status = %d, want %d", code, http.StatusOK)
	}
	if current.ID != started.ID {
		t.Fatalf("current registration = %#v", current)
	}

	if code := requestJSON(
		t,
		server.Handler(),
		http.MethodDelete,
		"/channels/feishu/registrations/"+started.ID,
		nil,
		nil,
	); code != http.StatusNoContent {
		t.Fatalf("cancel status = %d, want %d", code, http.StatusNoContent)
	}
}

type fakeFeishuBotManager struct {
	state        feishu.State
	input        feishu.UpdateInput
	registration feishu.RegistrationState
}

func (m *fakeFeishuBotManager) List() []feishu.State {
	return []feishu.State{m.state}
}

func (m *fakeFeishuBotManager) Get(id string) (feishu.State, bool) {
	return m.state, id == m.state.ID
}

func (m *fakeFeishuBotManager) Create(input feishu.UpdateInput) (feishu.State, error) {
	m.input = input
	m.state.ID = "feishu-created"
	m.state.Enabled = input.Enabled
	m.state.AppID = input.AppID
	return m.state, nil
}

func (m *fakeFeishuBotManager) Update(_ string, input feishu.UpdateInput) (feishu.State, error) {
	m.input = input
	m.state.Enabled = input.Enabled
	m.state.AppID = input.AppID
	return m.state, nil
}

func (m *fakeFeishuBotManager) Delete(string) error {
	return nil
}

func (m *fakeFeishuBotManager) StartRegistration(
	_ feishu.RegistrationInput,
) (feishu.RegistrationState, error) {
	m.registration = feishu.RegistrationState{
		ID:        "registration-1",
		Status:    feishu.RegistrationPending,
		QRCodeURL: "https://accounts.feishu.cn/qr",
	}
	return m.registration, nil
}

func (m *fakeFeishuBotManager) GetRegistration(id string) (feishu.RegistrationState, bool) {
	return m.registration, id == m.registration.ID
}

func (m *fakeFeishuBotManager) CancelRegistration(id string) error {
	if id != m.registration.ID {
		return os.ErrNotExist
	}
	m.registration.Status = feishu.RegistrationCancelled
	return nil
}
