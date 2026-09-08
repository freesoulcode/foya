package feishu

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"testing"

	interaction "github.com/freesoulcode/foya/internal/interaction"
)

func TestManagerPersistsRedactsAndRestartsSettings(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dataDir := t.TempDir()
	runtime := newFakeBackend()
	var channels []*managedChannel
	factory := func(_, _ string) Channel {
		channel := &managedChannel{fakeChannel: fakeChannel{}}
		channels = append(channels, channel)
		return channel
	}
	manager, err := newManager(ctx, dataDir, runtime, log.Default(), factory, false)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()

	state, err := manager.Create(UpdateInput{
		Name:         "飞书 Bot",
		Locale:       "en-US",
		Enabled:      true,
		AppID:        "cli_test",
		AppSecret:    "secret",
		ApprovalMode: interaction.ModeAuto,
		AllowedUsers: []string{" ou_1 ", "ou_1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if state.Status != StatusRunning || !state.HasAppSecret || state.Locale != "en-US" {
		t.Fatalf("state = %#v", state)
	}
	if len(state.AllowedUsers) != 1 || state.AllowedUsers[0] != "ou_1" {
		t.Fatalf("allowed users = %#v", state.AllowedUsers)
	}

	data, err := os.ReadFile(filepath.Join(dataDir, "feishu-channels.json"))
	if err != nil {
		t.Fatal(err)
	}
	var persisted persistedCatalog
	if err := json.Unmarshal(data, &persisted); err != nil {
		t.Fatal(err)
	}
	if len(persisted.Bots) != 1 || persisted.Bots[0].AppSecret != "secret" {
		t.Fatalf("persisted bots = %#v", persisted.Bots)
	}

	state, err = manager.Update(state.ID, UpdateInput{
		Name:         "更新后的 Bot",
		Locale:       "en-US",
		Enabled:      true,
		AppID:        "cli_updated",
		ApprovalMode: interaction.ModeAuto,
		AllowedUsers: []string{"ou_1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !state.HasAppSecret || state.AppID != "cli_updated" || state.Name != "更新后的 Bot" {
		t.Fatalf("updated state = %#v", state)
	}
	if len(channels) != 2 {
		t.Fatalf("created %d channels, want 2", len(channels))
	}

	manager.Close()
	reloaded, err := newManager(ctx, dataDir, runtime, log.Default(), factory, true)
	if err != nil {
		t.Fatal(err)
	}
	defer reloaded.Close()
	reloadedItems := reloaded.List()
	if len(reloadedItems) != 1 {
		t.Fatalf("reloaded items = %#v", reloadedItems)
	}
	if got := reloadedItems[0]; got.Status != StatusRunning || !got.Enabled || got.Locale != "en-US" {
		t.Fatalf("reloaded state = %#v", got)
	}
	if len(channels) != 3 {
		t.Fatalf("created %d channels after reload, want 3", len(channels))
	}
}

func TestManagerRejectsIncompleteEnabledSettings(t *testing.T) {
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

	if _, err := manager.Create(UpdateInput{
		Name:         "飞书 Bot",
		Enabled:      true,
		AppID:        "cli_test",
		AppSecret:    "secret",
		ApprovalMode: interaction.ModeAuto,
	}); err == nil {
		t.Fatal("enabled bot accepted an empty access policy")
	}
}

type managedChannel struct {
	fakeChannel
}

func (c *managedChannel) Start(ctx context.Context) error {
	<-ctx.Done()
	return ctx.Err()
}
