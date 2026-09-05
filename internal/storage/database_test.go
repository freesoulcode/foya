package storage

import (
	"os"
	"testing"
)

func TestOpenCreatesPrivateDatabaseWithRequiredPragmas(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	info, err := os.Stat(db.Path())
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("database mode = %o, want 600", info.Mode().Perm())
	}

	var journalMode string
	if err := db.QueryRow(`PRAGMA journal_mode`).Scan(&journalMode); err != nil {
		t.Fatal(err)
	}
	if journalMode != "wal" {
		t.Fatalf("journal_mode = %q, want wal", journalMode)
	}
	var foreignKeys int
	if err := db.QueryRow(`PRAGMA foreign_keys`).Scan(&foreignKeys); err != nil {
		t.Fatal(err)
	}
	if foreignKeys != 1 {
		t.Fatalf("foreign_keys = %d, want 1", foreignKeys)
	}
	var autoVacuum int
	if err := db.QueryRow(`PRAGMA auto_vacuum`).Scan(&autoVacuum); err != nil {
		t.Fatal(err)
	}
	if autoVacuum != 2 {
		t.Fatalf("auto_vacuum = %d, want incremental (2)", autoVacuum)
	}

	for _, table := range []string{
		"file_blobs",
		"file_changes",
		"file_rewind_journals",
		"file_rewind_journal_files",
		"context_accepted_boundaries",
		"usage_daily_ledger",
		"usage_message_daily_ledger",
		"usage_session_days",
	} {
		var name string
		if err := db.QueryRow(`
			SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?
		`, table).Scan(&name); err != nil {
			t.Fatalf("missing table %s: %v", table, err)
		}
	}
}
