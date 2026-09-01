package memorymaint

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/freesoulcode/foya/internal/contextdata"
	"github.com/freesoulcode/foya/internal/event"
	"github.com/freesoulcode/foya/internal/message"
	"github.com/freesoulcode/foya/internal/provider"
	"github.com/freesoulcode/foya/internal/session"
	"github.com/freesoulcode/foya/internal/state"
)

type testCompleter struct {
	mu    sync.Mutex
	calls int
	reply string
}

func (c *testCompleter) Complete(context.Context, provider.Request) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	return c.reply, nil
}

func (c *testCompleter) Calls() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

func TestRunExtractsStableRootSessionOnlyOnce(t *testing.T) {
	dataDir := t.TempDir()
	sessions := session.NewMemManager()
	log := state.NewMemLog()
	store, err := contextdata.NewStore(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	root, err := sessions.Create(session.CreateOptions{ProjectID: "project-1", Model: "test"})
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	appendMessage(t, log, root.ID, old, message.Message{
		Role: message.RoleUser, Content: "Remember that this project uses Go modules.",
	})
	appendMessage(t, log, root.ID, old.Add(time.Second), message.Message{
		Role: message.RoleAssistant, Content: "Confirmed the module configuration.",
	})
	completer := &testCompleter{reply: `{"memories":["The project uses Go modules."]}`}
	manager, err := New(dataDir, sessions, log, store, func(string) (provider.Completer, string, string, bool) {
		return completer, "test", "", true
	})
	if err != nil {
		t.Fatal(err)
	}
	manager.settings.IdleAfter = 0
	manager.settings.MinInterval = time.Hour

	if err := manager.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	items := store.ListMemories(contextdata.ScopeProject, "project-1")
	if len(items) != 1 || items[0].Content != "The project uses Go modules." {
		t.Fatalf("memories = %#v", items)
	}
	if err := manager.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := completer.Calls(); got != 1 {
		t.Fatalf("extraction calls = %d, want 1", got)
	}
}

func TestRunThrottlesConsecutiveRootSessionsPerProject(t *testing.T) {
	dataDir := t.TempDir()
	sessions := session.NewMemManager()
	log := state.NewMemLog()
	store, err := contextdata.NewStore(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	completer := &testCompleter{reply: `{"memories":["Use focused tests."]}`}
	manager, err := New(dataDir, sessions, log, store, func(string) (provider.Completer, string, string, bool) {
		return completer, "test", "", true
	})
	if err != nil {
		t.Fatal(err)
	}
	manager.settings.IdleAfter = 0
	manager.settings.MinInterval = time.Hour

	for i := 0; i < 2; i++ {
		item, err := sessions.Create(session.CreateOptions{ProjectID: "project-1", Model: "test"})
		if err != nil {
			t.Fatal(err)
		}
		appendMessage(t, log, item.ID, time.Now().Add(-time.Hour), message.Message{
			Role: message.RoleUser, Content: "Please remember this project preference.",
		})
		appendMessage(t, log, item.ID, time.Now().Add(-time.Hour+time.Second), message.Message{
			Role: message.RoleAssistant, Content: "I will keep that preference.",
		})
	}
	if err := manager.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := completer.Calls(); got != 1 {
		t.Fatalf("extraction calls = %d, want 1", got)
	}
}

func TestRunSkipsActiveSessions(t *testing.T) {
	dataDir := t.TempDir()
	sessions := session.NewMemManager()
	log := state.NewMemLog()
	store, err := contextdata.NewStore(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	item, err := sessions.Create(session.CreateOptions{ProjectID: "project-1", Model: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sessions.SetPhase(item.ID, session.PhaseTurn); err != nil {
		t.Fatal(err)
	}
	appendMessage(t, log, item.ID, time.Now().Add(-time.Hour), message.Message{
		Role: message.RoleUser, Content: "Remember this project preference.",
	})
	completer := &testCompleter{reply: `{"memories":["Use focused tests."]}`}
	manager, err := New(dataDir, sessions, log, store, func(string) (provider.Completer, string, string, bool) {
		return completer, "test", "", true
	})
	if err != nil {
		t.Fatal(err)
	}
	manager.settings.IdleAfter = 0
	if err := manager.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := completer.Calls(); got != 0 {
		t.Fatalf("extraction calls = %d, want 0", got)
	}
}

func TestRunSkipsExtractionWhenMemoryIsDisabled(t *testing.T) {
	dataDir := t.TempDir()
	sessions := session.NewMemManager()
	log := state.NewMemLog()
	store, err := contextdata.NewStore(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateMemorySettings(contextdata.MemorySettings{Enabled: false}); err != nil {
		t.Fatal(err)
	}
	item, err := sessions.Create(session.CreateOptions{Model: "test"})
	if err != nil {
		t.Fatal(err)
	}
	appendMessage(t, log, item.ID, time.Now().Add(-time.Hour), message.Message{
		Role: message.RoleUser, Content: "Remember this preference.",
	})
	completer := &testCompleter{reply: `{"memories":["Use focused tests."]}`}
	manager, err := New(dataDir, sessions, log, store, func(string) (provider.Completer, string, string, bool) {
		return completer, "test", "", true
	})
	if err != nil {
		t.Fatal(err)
	}
	manager.settings.IdleAfter = 0
	if err := manager.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := completer.Calls(); got != 0 {
		t.Fatalf("extraction calls = %d, want 0", got)
	}
}

func appendMessage(
	t *testing.T,
	log *state.MemLog,
	sessionID string,
	at time.Time,
	item message.Message,
) {
	t.Helper()
	if _, err := log.Append(context.Background(), event.Event{
		Kind: event.KindMessageEnd, Session: sessionID, Time: at, Payload: item,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestExtractRejectsNonJSON(t *testing.T) {
	completer := &testCompleter{reply: "not JSON"}
	if _, err := extract(context.Background(), completer, "test", "", "evidence"); err == nil {
		t.Fatal("expected parse error")
	}
}

func TestExtractDeduplicatesOutput(t *testing.T) {
	data, _ := json.Marshal(map[string][]string{
		"memories": {"Use Go.", "Use Go.", "Prefer concise replies."},
	})
	completer := &testCompleter{reply: string(data)}
	items, err := extract(context.Background(), completer, "test", "", "evidence")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("items = %#v", items)
	}
}
