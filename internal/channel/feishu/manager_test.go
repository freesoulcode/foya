package feishu

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"testing"

	foyachannel "github.com/freesoulcode/foya/internal/channel"
	interaction "github.com/freesoulcode/foya/internal/interaction"
	"github.com/freesoulcode/foya/internal/testkit"
)

func TestManagerPersistsRedactsAndRestartsSettings(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dataDir := t.TempDir()
	runtime := newFakeBackend()
	bindings := foyachannel.NewConversationBindingStore(testkit.OpenDatabase(t))
	var channels []*managedChannel
	factory := func(_, _ string) Channel {
		channel := &managedChannel{fakeChannel: fakeChannel{}}
		channels = append(channels, channel)
		return channel
	}
	pairings := foyachannel.NewConversationPairingStore(bindings, runtime)
	manager, err := newManager(
		ctx, dataDir, runtime, bindings, pairings, log.Default(), factory, false,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()

	state, err := manager.Create(UpdateInput{
		Name:         "Feishu Bot",
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
	if len(persisted.Channels) != 1 || persisted.Channels[0].AppSecret != "secret" {
		t.Fatalf("persisted channels = %#v", persisted.Channels)
	}

	state, err = manager.Update(state.ID, UpdateInput{
		Name:         "Updated Bot",
		Locale:       "en-US",
		Enabled:      true,
		AppID:        "cli_updated",
		ApprovalMode: interaction.ModeAuto,
		AllowedUsers: []string{"ou_1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !state.HasAppSecret || state.AppID != "cli_updated" || state.Name != "Updated Bot" {
		t.Fatalf("updated state = %#v", state)
	}
	if len(channels) != 2 {
		t.Fatalf("created %d channels, want 2", len(channels))
	}

	manager.Close()
	reloaded, err := newManager(
		ctx, dataDir, runtime, bindings, pairings, log.Default(), factory, true,
	)
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

func TestManagerAllowsPairingOnlyAccess(t *testing.T) {
	runtime := newFakeBackend()
	bindings := foyachannel.NewConversationBindingStore(testkit.OpenDatabase(t))
	manager, err := newManager(
		context.Background(),
		t.TempDir(),
		runtime,
		bindings,
		foyachannel.NewConversationPairingStore(bindings, runtime),
		log.Default(),
		func(_, _ string) Channel { return &managedChannel{} },
		false,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()

	if _, err := manager.Create(UpdateInput{
		Name:         "Feishu Bot",
		Enabled:      true,
		AppID:        "cli_test",
		AppSecret:    "secret",
		ApprovalMode: interaction.ModeAuto,
	}); err != nil {
		t.Fatalf("pairing-only channel failed: %v", err)
	}
}

func TestManagerDeletesChannelConversations(t *testing.T) {
	db := testkit.OpenDatabase(t)
	bindings := foyachannel.NewConversationBindingStore(db)
	runtime := newFakeBackend()
	manager, err := newManager(
		context.Background(),
		t.TempDir(),
		runtime,
		bindings,
		foyachannel.NewConversationPairingStore(bindings, runtime),
		log.Default(),
		func(_, _ string) Channel { return &managedChannel{} },
		false,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	state, err := manager.Create(UpdateInput{Name: "Feishu", ApprovalMode: interaction.ModeAuto})
	if err != nil {
		t.Fatal(err)
	}
	if err := bindings.Observe(
		context.Background(),
		state.ID,
		"chat:oc_1",
		"group",
		"oc_1",
		"Release group",
	); err != nil {
		t.Fatal(err)
	}
	if err := bindings.Bind(
		context.Background(),
		state.ID,
		"chat:oc_1",
		"session-a",
	); err != nil {
		t.Fatal(err)
	}
	items, err := bindings.List(context.Background(), state.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ActiveSessionID != "session-a" {
		t.Fatalf("conversations = %#v", items)
	}
	if err := manager.Delete(state.ID); err != nil {
		t.Fatal(err)
	}
	items, err = bindings.List(context.Background(), state.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Fatalf("deleted channel conversations = %#v", items)
	}
}

func TestManagerRejectsVersionOneCatalog(t *testing.T) {
	dataDir := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(dataDir, "feishu-channels.json"),
		[]byte(`{"version":1,"bots":[]}`),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	runtime := newFakeBackend()
	bindings := foyachannel.NewConversationBindingStore(testkit.OpenDatabase(t))
	_, err := newManager(
		context.Background(),
		dataDir,
		runtime,
		bindings,
		foyachannel.NewConversationPairingStore(bindings, runtime),
		log.Default(),
		func(_, _ string) Channel { return &managedChannel{} },
		false,
	)
	if err == nil {
		t.Fatal("version 1 channel catalog was accepted")
	}
}

type managedChannel struct {
	fakeChannel
}

func (c *managedChannel) Start(ctx context.Context) error {
	<-ctx.Done()
	return ctx.Err()
}
