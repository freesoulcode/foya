package tool

import (
	"testing"

	"github.com/freesoulcode/foya/internal/session"
	"github.com/freesoulcode/foya/internal/testkit"
)

func newTestSessionManager(t testing.TB) session.Manager {
	t.Helper()
	manager, err := session.NewSQLiteManager(testkit.OpenSQLite(t))
	if err != nil {
		t.Fatal(err)
	}
	return manager
}
