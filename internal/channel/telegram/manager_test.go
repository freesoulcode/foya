package telegram

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"testing"

	foyachannel "github.com/freesoulcode/foya/internal/channel"
	"github.com/freesoulcode/foya/internal/interaction"
	"github.com/freesoulcode/foya/internal/testkit"
)

func TestManagerPersistsRedactsAndRestartsSettings(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dataDir := t.TempDir()
	runtime := newFakeRuntime()
	bindings := foyachannel.NewConversationBindingStore(testkit.OpenDatabase(t))
	pairings := foyachannel.NewConversationPairingStore(bindings, runtime)
	var clients []*fakeAPI
	factory := func(string) API {
		client := &fakeAPI{}
		clients = append(clients, client)
		return client
	}
	manager, err := newManager(
		ctx,
		dataDir,
		runtime,
		bindings,
		pairings,
		log.Default(),
		factory,
		false,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()

	state, err := manager.Create(UpdateInput{
		Name:         "Telegram",
		Locale:       "en-US",
		Enabled:      true,
		Token:        "123:secret",
		ApprovalMode: interaction.ModeAuto,
		AllowedUsers: []string{" @ada ", "ada"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if state.Status != StatusRunning || !state.HasToken || state.Locale != "en-US" {
		t.Fatalf("state = %#v", state)
	}
	if len(state.AllowedUsers) != 1 || state.AllowedUsers[0] != "ada" {
		t.Fatalf("allowed users = %#v", state.AllowedUsers)
	}

	data, err := os.ReadFile(filepath.Join(dataDir, "telegram-channels.json"))
	if err != nil {
		t.Fatal(err)
	}
	var persisted persistedCatalog
	if err := json.Unmarshal(data, &persisted); err != nil {
		t.Fatal(err)
	}
	if len(persisted.Channels) != 1 || persisted.Channels[0].Token != "123:secret" {
		t.Fatalf("persisted channels = %#v", persisted.Channels)
	}

	state, err = manager.Update(state.ID, UpdateInput{
		Name:         "Telegram updated",
		Locale:       "en-US",
		Enabled:      true,
		ApprovalMode: interaction.ModeAuto,
		AllowedUsers: []string{"ada"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !state.HasToken || state.Name != "Telegram updated" {
		t.Fatalf("updated state = %#v", state)
	}
	if len(clients) != 2 {
		t.Fatalf("created %d clients, want 2", len(clients))
	}

	manager.Close()
	reloaded, err := newManager(
		ctx,
		dataDir,
		runtime,
		bindings,
		pairings,
		log.Default(),
		factory,
		true,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer reloaded.Close()
	items := reloaded.List()
	if len(items) != 1 || items[0].Status != StatusRunning ||
		!items[0].Enabled || !items[0].HasToken {
		t.Fatalf("reloaded items = %#v", items)
	}
}

func TestManagerListsBindsAndDeletesConversations(t *testing.T) {
	db := testkit.OpenDatabase(t)
	bindings := foyachannel.NewConversationBindingStore(db)
	runtime := newFakeRuntime()
	manager, err := newManager(
		context.Background(),
		t.TempDir(),
		runtime,
		bindings,
		foyachannel.NewConversationPairingStore(bindings, runtime),
		log.Default(),
		func(string) API { return &fakeAPI{} },
		false,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	state, err := manager.Create(UpdateInput{
		Name: "Telegram", ApprovalMode: interaction.ModeAuto,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := bindings.Observe(
		context.Background(),
		state.ID,
		"chat:101",
		"private",
		"101",
		"Ada",
	); err != nil {
		t.Fatal(err)
	}
	if err := bindings.Bind(
		context.Background(),
		state.ID,
		"chat:101",
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

func TestManagerAllowsPairingOnlyAccess(t *testing.T) {
	runtime := newFakeRuntime()
	bindings := foyachannel.NewConversationBindingStore(testkit.OpenDatabase(t))
	manager, err := newManager(
		context.Background(),
		t.TempDir(),
		runtime,
		bindings,
		foyachannel.NewConversationPairingStore(bindings, runtime),
		log.Default(),
		func(string) API { return &fakeAPI{} },
		false,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()

	if _, err := manager.Create(UpdateInput{
		Name:         "Telegram",
		Enabled:      true,
		Token:        "123:secret",
		ApprovalMode: interaction.ModeAuto,
	}); err != nil {
		t.Fatalf("pairing-only channel failed: %v", err)
	}
}

func TestManagerRejectsDuplicateToken(t *testing.T) {
	runtime := newFakeRuntime()
	bindings := foyachannel.NewConversationBindingStore(testkit.OpenDatabase(t))
	manager, err := newManager(
		context.Background(),
		t.TempDir(),
		runtime,
		bindings,
		foyachannel.NewConversationPairingStore(bindings, runtime),
		log.Default(),
		func(string) API { return &fakeAPI{} },
		false,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	if _, err := manager.Create(UpdateInput{
		Name: "First", Token: "123:secret", ApprovalMode: interaction.ModeAuto,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Create(UpdateInput{
		Name: "Second", Token: "123:secret", ApprovalMode: interaction.ModeAuto,
	}); err == nil {
		t.Fatal("duplicate Telegram token was accepted")
	}
}
