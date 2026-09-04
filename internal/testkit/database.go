// Package testkit provides temporary SQLite databases for package tests.
package testkit

import (
	"context"
	"sync"
	"testing"

	"github.com/freesoulcode/foya/internal/event"
	"github.com/freesoulcode/foya/internal/state"
	"github.com/freesoulcode/foya/internal/storage"
)

// OpenDatabase returns an isolated database which is closed with the test.
func OpenDatabase(t testing.TB) *storage.Database {
	t.Helper()
	db, err := storage.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close test database: %v", err)
		}
	})
	return db
}

// NewLog returns a lightweight event log for tests that only need Append/Read.
func NewLog() state.Log {
	return &log{events: make(map[string][]event.Event)}
}

type log struct {
	mu     sync.Mutex
	seq    event.Seq
	events map[string][]event.Event
}

func (l *log) Append(ctx context.Context, ev event.Event) (event.Seq, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.seq++
	ev.Seq = l.seq
	l.events[ev.Session] = append(l.events[ev.Session], ev)
	return ev.Seq, nil
}

func (l *log) Read(ctx context.Context, session string, after event.Seq) ([]event.Event, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []event.Event
	for _, ev := range l.events[session] {
		if ev.Seq > after {
			out = append(out, ev)
		}
	}
	return out, nil
}
