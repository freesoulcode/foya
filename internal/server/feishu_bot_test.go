package server

import (
	"net/http"
	"testing"

	"github.com/freesoulcode/foya/internal/approval"
	"github.com/freesoulcode/foya/internal/channel/feishu"
)

func TestFeishuBotSettingsAPI(t *testing.T) {
	manager := &fakeFeishuBotManager{state: feishu.State{
		Enabled:      false,
		AppID:        "cli_test",
		HasAppSecret: true,
		ApprovalMode: approval.ModeAuto,
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
			ApprovalMode: approval.ModeAuto,
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

type fakeFeishuBotManager struct {
	state feishu.State
	input feishu.UpdateInput
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
