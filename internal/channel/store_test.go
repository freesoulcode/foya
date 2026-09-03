package channel

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSessionStorePersistsMappings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "sessions.json")
	store, err := OpenSessionStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Set("oc_1", "session-1"); err != nil {
		t.Fatal(err)
	}

	reloaded, err := OpenSessionStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := reloaded.Get("oc_1"); got != "session-1" {
		t.Fatalf("reloaded session = %q, want session-1", got)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("session store permissions = %o, want 600", got)
	}
}

func TestSessionStoreRejectsUnknownVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.json")
	if err := os.WriteFile(path, []byte(`{"version":2,"chats":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenSessionStore(path); err == nil {
		t.Fatal("opened a session store with an unknown version")
	}
}
