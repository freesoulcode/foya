package agent

import (
	"testing"

	"github.com/freesoulcode/foya/internal/session"
	"github.com/freesoulcode/foya/internal/state"
	"github.com/freesoulcode/foya/internal/testkit"
)

func newTestStore(t testing.TB) state.Store {
	t.Helper()
	return state.NewSQLiteStore(testkit.OpenSQLite(t))
}

func newTestSessionManager(t testing.TB) session.Manager {
	t.Helper()
	manager, err := session.NewSQLiteManager(testkit.OpenSQLite(t))
	if err != nil {
		t.Fatal(err)
	}
	return manager
}
