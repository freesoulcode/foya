package subagent

import (
	"testing"

	"github.com/freesoulcode/foya/internal/session"
	"github.com/freesoulcode/foya/internal/state"
	"github.com/freesoulcode/foya/internal/testkit"
)

func newTestStore(t testing.TB) state.Store {
	t.Helper()
	return state.NewStore(testkit.OpenDatabase(t))
}

func newTestSessionManager(t testing.TB) session.Manager {
	t.Helper()
	manager, err := session.NewManager(testkit.OpenDatabase(t))
	if err != nil {
		t.Fatal(err)
	}
	return manager
}
