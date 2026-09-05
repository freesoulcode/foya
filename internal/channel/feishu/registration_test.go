package feishu

import (
	"context"
	"encoding/json"
	"log"
	"strings"
	"testing"
	"time"

	"github.com/freesoulcode/foya/internal/approval"
	larkregistration "github.com/larksuite/oapi-sdk-go/v3/scene/registration"
)

func TestRegistrationCreatesEnabledChannelWithoutExposingSecret(t *testing.T) {
	manager, err := newManager(
		context.Background(),
		t.TempDir(),
		newFakeBackend(),
		log.Default(),
		func(_, _ string) Channel { return &managedChannel{} },
		false,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()

	qrReady := make(chan struct{})
	authorized := make(chan struct{})
	manager.registerApp = func(
		ctx context.Context,
		options *larkregistration.Options,
	) (*larkregistration.RegisterAppResult, error) {
		if !options.CreateOnly || options.Addons == nil ||
			options.Addons.Preset == nil || *options.Addons.Preset {
			t.Errorf("registration options = %#v", options)
		}
		if options.AppPreset == nil || options.AppPreset.Name != "扫码 Bot" {
			t.Errorf("app preset = %#v", options.AppPreset)
		}
		options.OnQRCode(&larkregistration.QRCodeInfo{
			URL:      "https://accounts.feishu.cn/qr",
			ExpireIn: 600,
		})
		close(qrReady)
		select {
		case <-authorized:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return &larkregistration.RegisterAppResult{
			ClientID:     "cli_registered",
			ClientSecret: "generated-secret",
			UserInfo: &larkregistration.UserInfo{
				OpenID:      "ou_scanner",
				TenantBrand: "feishu",
			},
		}, nil
	}

	started, err := manager.StartRegistration(RegistrationInput{
		Name:         "扫码 Bot",
		ApprovalMode: approval.ModeAuto,
	})
	if err != nil {
		t.Fatal(err)
	}
	<-qrReady
	pending, ok := manager.GetRegistration(started.ID)
	if !ok || pending.Status != RegistrationPending ||
		pending.QRCodeURL != "https://accounts.feishu.cn/qr" ||
		pending.ExpiresAt == "" {
		t.Fatalf("pending registration = %#v, ok = %v", pending, ok)
	}

	close(authorized)
	completed := waitForRegistrationStatus(t, manager, started.ID, RegistrationCompleted)
	if completed.Channel == nil {
		t.Fatalf("completed registration = %#v", completed)
	}
	if completed.Channel.AppID != "cli_registered" ||
		!completed.Channel.HasAppSecret ||
		!completed.Channel.Enabled ||
		len(completed.Channel.AllowedUsers) != 1 ||
		completed.Channel.AllowedUsers[0] != "ou_scanner" {
		t.Fatalf("created channel = %#v", completed.Channel)
	}
	encoded, err := json.Marshal(completed)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "generated-secret") {
		t.Fatalf("registration response exposed app secret: %s", encoded)
	}
}

func TestRegistrationCanBeCancelled(t *testing.T) {
	manager, err := newManager(
		context.Background(),
		t.TempDir(),
		newFakeBackend(),
		log.Default(),
		func(_, _ string) Channel { return &managedChannel{} },
		false,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()

	qrReady := make(chan struct{})
	manager.registerApp = func(
		ctx context.Context,
		options *larkregistration.Options,
	) (*larkregistration.RegisterAppResult, error) {
		options.OnQRCode(&larkregistration.QRCodeInfo{
			URL:      "https://accounts.feishu.cn/qr",
			ExpireIn: 600,
		})
		close(qrReady)
		<-ctx.Done()
		return nil, ctx.Err()
	}

	started, err := manager.StartRegistration(RegistrationInput{})
	if err != nil {
		t.Fatal(err)
	}
	<-qrReady
	if err := manager.CancelRegistration(started.ID); err != nil {
		t.Fatal(err)
	}
	cancelled := waitForRegistrationStatus(t, manager, started.ID, RegistrationCancelled)
	if cancelled.Channel != nil || cancelled.Error != "" {
		t.Fatalf("cancelled registration = %#v", cancelled)
	}
}

func waitForRegistrationStatus(
	t *testing.T,
	manager *Manager,
	id string,
	want RegistrationStatus,
) RegistrationState {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		state, ok := manager.GetRegistration(id)
		if !ok {
			t.Fatalf("registration %q disappeared", id)
		}
		if state.Status == want {
			return state
		}
		time.Sleep(5 * time.Millisecond)
	}
	state, _ := manager.GetRegistration(id)
	t.Fatalf("registration status = %q, want %q", state.Status, want)
	return RegistrationState{}
}
