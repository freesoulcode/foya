package subagent

import (
	"testing"

	conversation "github.com/freesoulcode/foya/internal/conversation"

	"github.com/freesoulcode/foya/internal/testkit"
)

func newTestStore(t testing.TB) conversation.Store {
	t.Helper()
	return conversation.NewStore(testkit.OpenDatabase(t))
}

func newTestSessionManager(t testing.TB) conversation.Manager {
	t.Helper()
	manager, err := conversation.NewManager(testkit.OpenDatabase(t))
	if err != nil {
		t.Fatal(err)
	}
	return manager
}
