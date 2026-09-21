package server

import (
	"context"
	"net/http"
	"os"
	"testing"

	foyachannel "github.com/freesoulcode/foya/internal/channel"
	"github.com/freesoulcode/foya/internal/channel/feishu"
	"github.com/freesoulcode/foya/internal/channelhub"
	"github.com/freesoulcode/foya/internal/config"
	"github.com/freesoulcode/foya/internal/interaction"
)

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
		feishu.RegistrationInput{Name: "Foya Agent", ApprovalMode: interaction.ModeAuto},
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

func TestChannelAPIAcceptsTelegram(t *testing.T) {
	manager := &fakeFeishuBotManager{}
	server := &Server{channels: manager, mux: http.NewServeMux()}
	server.routes()

	var created channelhub.State
	if code := requestJSON(
		t,
		server.Handler(),
		http.MethodPost,
		"/channels",
		channelhub.UpdateInput{
			Kind:         channelhub.KindTelegram,
			Name:         "Telegram",
			Token:        "123:secret",
			ApprovalMode: interaction.ModeAuto,
			AllowAll:     true,
		},
		&created,
	); code != http.StatusCreated {
		t.Fatalf("create status = %d, want %d", code, http.StatusCreated)
	}
	if manager.input.Kind != channelhub.KindTelegram ||
		manager.input.Token != "123:secret" ||
		created.Kind != channelhub.KindTelegram {
		t.Fatalf("input = %#v, state = %#v", manager.input, created)
	}
}

func TestSessionChannelPairingAPI(t *testing.T) {
	_, sessionID, service := newQueueTestServerWithService(t)
	manager := &fakeFeishuBotManager{
		state: channelhub.State{
			ID: "feishu-1", Kind: "feishu", Name: "Team Bot",
		},
	}
	server := New(config.Config{}, service)
	server.SetChannelManager(manager)
	pairingRequest := channelPairingRequest{ChannelID: "feishu-1"}

	if code := requestJSON(
		t,
		server.Handler(),
		http.MethodPost,
		"/sessions/"+sessionID+"/channel-pairings",
		pairingRequest,
		nil,
	); code != http.StatusConflict {
		t.Fatalf("manual session pairing status = %d", code)
	}
	approval := string(interaction.ModeAuto)
	if _, err := service.UpdateSession(
		context.Background(),
		sessionID,
		nil,
		nil,
		nil,
		nil,
		&approval,
	); err != nil {
		t.Fatal(err)
	}

	var started foyachannel.ConversationPairing
	if code := requestJSON(
		t,
		server.Handler(),
		http.MethodPost,
		"/sessions/"+sessionID+"/channel-pairings",
		pairingRequest,
		&started,
	); code != http.StatusCreated {
		t.Fatalf("start pairing status = %d, pairing = %#v", code, started)
	}
	if manager.pairedSessionID != sessionID ||
		started.Command != "/bind A2BC34" {
		t.Fatalf("pairing = %#v, manager session = %q", started, manager.pairedSessionID)
	}
	var current foyachannel.ConversationPairing
	if code := requestJSON(
		t,
		server.Handler(),
		http.MethodGet,
		"/channel-pairings/"+started.ID,
		nil,
		&current,
	); code != http.StatusOK {
		t.Fatalf("get pairing status = %d", code)
	}
	if current.ID != started.ID || current.Status != foyachannel.PairingPending {
		t.Fatalf("current pairing = %#v", current)
	}
	if code := requestJSON(
		t,
		server.Handler(),
		http.MethodDelete,
		"/channel-pairings/"+started.ID,
		nil,
		nil,
	); code != http.StatusNoContent {
		t.Fatalf("cancel pairing status = %d", code)
	}

	manager.binding = foyachannel.ConversationBinding{
		ChannelID:       "feishu-1",
		ConversationKey: "chat:oc_1",
		Kind:            "group",
		ExternalID:      "oc_1",
		ActiveSessionID: sessionID,
	}
	var binding foyachannel.ConversationBinding
	if code := requestJSON(
		t,
		server.Handler(),
		http.MethodGet,
		"/sessions/"+sessionID+"/channel-binding",
		nil,
		&binding,
	); code != http.StatusOK || binding.ExternalID != "oc_1" {
		t.Fatalf("get binding status = %d, binding = %#v", code, binding)
	}

	if code := requestJSON(
		t,
		server.Handler(),
		http.MethodDelete,
		"/sessions/"+sessionID+"/channel-binding",
		nil,
		nil,
	); code != http.StatusNoContent || manager.unboundSessionID != sessionID {
		t.Fatalf("unbind status = %d, session = %q", code, manager.unboundSessionID)
	}
}

type fakeFeishuBotManager struct {
	state            channelhub.State
	input            channelhub.UpdateInput
	registration     feishu.RegistrationState
	pairing          foyachannel.ConversationPairing
	binding          foyachannel.ConversationBinding
	pairedSessionID  string
	unboundSessionID string
}

func (m *fakeFeishuBotManager) List() []channelhub.State {
	return []channelhub.State{m.state}
}

func (m *fakeFeishuBotManager) Get(id string) (channelhub.State, bool) {
	return m.state, id == m.state.ID
}

func (m *fakeFeishuBotManager) Create(
	input channelhub.UpdateInput,
) (channelhub.State, error) {
	m.input = input
	m.state.ID = "channel-created"
	m.state.Kind = input.Kind
	m.state.Enabled = input.Enabled
	m.state.AppID = input.AppID
	return m.state, nil
}

func (m *fakeFeishuBotManager) Update(
	_ string,
	input channelhub.UpdateInput,
) (channelhub.State, error) {
	m.input = input
	m.state.Enabled = input.Enabled
	m.state.AppID = input.AppID
	return m.state, nil
}

func (m *fakeFeishuBotManager) Delete(string) error {
	return nil
}

func (m *fakeFeishuBotManager) BindingForSession(
	_ context.Context,
	sessionID string,
) (foyachannel.ConversationBinding, error) {
	if m.binding.ActiveSessionID == sessionID {
		return m.binding, nil
	}
	return foyachannel.ConversationBinding{}, nil
}

func (m *fakeFeishuBotManager) UnbindSession(_ context.Context, sessionID string) error {
	m.unboundSessionID = sessionID
	return nil
}

func (m *fakeFeishuBotManager) StartPairing(
	channelID, sessionID string,
) (foyachannel.ConversationPairing, error) {
	m.pairedSessionID = sessionID
	m.pairing = foyachannel.ConversationPairing{
		ID:        "pairing-1",
		Code:      "A2BC34",
		Command:   "/bind A2BC34",
		ChannelID: channelID,
		SessionID: sessionID,
		Status:    foyachannel.PairingPending,
	}
	return m.pairing, nil
}

func (m *fakeFeishuBotManager) GetPairing(
	id string,
) (foyachannel.ConversationPairing, bool) {
	return m.pairing, id == m.pairing.ID
}

func (m *fakeFeishuBotManager) CancelPairing(id string) error {
	if id != m.pairing.ID {
		return foyachannel.ErrPairingNotFound
	}
	m.pairing.Status = foyachannel.PairingCancelled
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
