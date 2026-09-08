package conversation

import (
	"context"
	"fmt"
	"time"

	"github.com/freesoulcode/foya/internal/storage"
)

// store persists canonical events and rebuildable projections.
type store struct {
	db *storage.Database
}

// NewStore creates an event store backed by the shared database.
func NewStore(db *storage.Database) Store {
	return &store{db: db}
}

func (s *store) Append(
	ctx context.Context,
	ev Event,
) (Seq, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin append event: %w", err)
	}
	defer tx.Rollback()

	seq, _, err := appendEventTx(ctx, tx, ev)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit event: %w", err)
	}
	return seq, nil
}

func (s *store) Read(
	ctx context.Context,
	session string,
	after Seq,
) ([]Event, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT seq, kind, session_id, run_id, occurred_at_ns, payload_json
		FROM events
		WHERE session_id = ? AND seq > ?
		ORDER BY seq
	`, session, int64(after))
	if err != nil {
		return nil, fmt.Errorf("read events: %w", err)
	}
	defer rows.Close()
	return scanEvents(rows)
}

func (s *store) Delete(ctx context.Context, session string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin delete session events: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO deleted_sessions(session_id, deleted_at_ns)
		VALUES (?, ?)
		ON CONFLICT(session_id) DO NOTHING
	`, session, time.Now().UnixNano()); err != nil {
		return fmt.Errorf("record deleted session: %w", err)
	}
	for _, query := range []string{
		`DELETE FROM stream_snapshots WHERE session_id = ?`,
		`DELETE FROM file_rewind_journals WHERE session_id = ?`,
		`DELETE FROM compaction_checkpoints WHERE session_id = ?`,
		`DELETE FROM context_accepted_boundaries WHERE session_id = ?`,
		`DELETE FROM message_projection WHERE session_id = ?`,
		`DELETE FROM usage_records WHERE session_id = ?`,
		`DELETE FROM events WHERE session_id = ?`,
	} {
		if _, err := tx.ExecContext(ctx, query, session); err != nil {
			return fmt.Errorf("delete session events: %w", err)
		}
	}
	if err := gcFileBlobsTx(ctx, tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit session event deletion: %w", err)
	}
	return nil
}
