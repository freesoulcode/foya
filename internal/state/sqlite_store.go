package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/freesoulcode/foya/internal/compaction"
	"github.com/freesoulcode/foya/internal/event"
	"github.com/freesoulcode/foya/internal/message"
	"github.com/freesoulcode/foya/internal/provider"
	"github.com/freesoulcode/foya/internal/storage"
)

// SQLiteStore persists canonical events and rebuildable projections.
type SQLiteStore struct {
	db *storage.Database
}

// NewSQLiteStore creates an event store backed by the shared database.
func NewSQLiteStore(db *storage.Database) *SQLiteStore {
	return &SQLiteStore{db: db}
}

func (s *SQLiteStore) Append(
	ctx context.Context,
	ev event.Event,
) (event.Seq, error) {
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

func (s *SQLiteStore) Read(
	ctx context.Context,
	session string,
	after event.Seq,
) ([]event.Event, error) {
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

func (s *SQLiteStore) Delete(ctx context.Context, session string) error {
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
		`DELETE FROM compaction_checkpoints WHERE session_id = ?`,
		`DELETE FROM message_projection WHERE session_id = ?`,
		`DELETE FROM usage_records WHERE session_id = ?`,
		`DELETE FROM events WHERE session_id = ?`,
	} {
		if _, err := tx.ExecContext(ctx, query, session); err != nil {
			return fmt.Errorf("delete session events: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit session event deletion: %w", err)
	}
	return nil
}

func (s *SQLiteStore) History(
	ctx context.Context,
	session string,
) ([]message.Message, error) {
	events, err := loadActiveMessageEvents(ctx, s.db, session)
	if err != nil {
		return nil, err
	}
	var messages []message.Message
	for _, ev := range events {
		item, ok := messageFromEvent(ev)
		if !ok {
			continue
		}
		item.EventSeq = uint64(ev.Seq)
		messages = append(messages, item)
	}
	return messages, nil
}

func (s *SQLiteStore) ModelHistory(
	ctx context.Context,
	session string,
) ([]message.Message, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("begin model history: %w", err)
	}
	defer tx.Rollback()

	events, err := loadActiveMessageEvents(ctx, tx, session)
	if err != nil {
		return nil, err
	}
	checkpoint, ok, err := loadCheckpoint(ctx, tx, session)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit model history read: %w", err)
	}
	if !ok {
		return compaction.Project(events, nil), nil
	}
	return compaction.Project(events, checkpoint), nil
}

func (s *SQLiteStore) Events(
	ctx context.Context,
	session string,
) ([]event.Event, error) {
	return loadActiveMessageEvents(ctx, s.db, session)
}

func (s *SQLiteStore) Checkpoint(
	ctx context.Context,
	session string,
) (*compaction.Checkpoint, bool, error) {
	return loadCheckpoint(ctx, s.db, session)
}

func (s *SQLiteStore) RecordCheckpoint(
	ctx context.Context,
	checkpoint compaction.Checkpoint,
) (event.Event, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return event.Event{}, fmt.Errorf("begin checkpoint: %w", err)
	}
	defer tx.Rollback()

	active, err := loadActiveMessageEvents(ctx, tx, checkpoint.SessionID)
	if err != nil {
		return event.Event{}, err
	}
	if !compaction.ValidateCheckpoint(active, checkpoint) {
		return event.Event{}, fmt.Errorf("compaction checkpoint does not match source events")
	}
	ev := event.Event{
		Kind:    event.KindCompactionCompleted,
		Session: checkpoint.SessionID,
		Payload: checkpoint,
		Time:    checkpoint.CreatedAt,
	}
	seq, inserted, err := appendEventTx(ctx, tx, ev)
	if err != nil {
		return event.Event{}, err
	}
	if !inserted {
		return event.Event{}, errors.New("cannot checkpoint a deleted session")
	}
	ev.Seq = seq
	if err := tx.Commit(); err != nil {
		return event.Event{}, fmt.Errorf("commit checkpoint: %w", err)
	}
	return ev, nil
}

func (s *SQLiteStore) Branch(
	ctx context.Context,
	session string,
	targetUserSeq event.Seq,
	editedContent string,
	allowEffects bool,
	expectedHeadSeq event.Seq,
) (BranchResult, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return BranchResult{}, fmt.Errorf("begin history branch: %w", err)
	}
	defer tx.Rollback()

	active, err := loadActiveMessageEvents(ctx, tx, session)
	if err != nil {
		return BranchResult{}, err
	}
	targetIndex := -1
	var target message.Message
	for i, ev := range active {
		if ev.Seq != targetUserSeq {
			continue
		}
		item, ok := messageFromEvent(ev)
		if !ok || item.Role != message.RoleUser {
			break
		}
		targetIndex = i
		target = item
		break
	}
	if targetIndex < 0 {
		return BranchResult{}, ErrActiveUserMessageNotFound
	}
	if target.Content == editedContent {
		return BranchResult{}, ErrMessageUnchanged
	}

	effects := branchEffects(active[targetIndex:])
	firstUser := true
	for _, ev := range active[:targetIndex] {
		item, ok := messageFromEvent(ev)
		if ok && item.Role == message.RoleUser {
			firstUser = false
			break
		}
	}
	var headSeq event.Seq
	if len(active) > 0 {
		headSeq = active[len(active)-1].Seq
	}
	if len(effects) > 0 && !allowEffects {
		return BranchResult{Effects: effects, HeadSeq: headSeq}, nil
	}
	if len(effects) > 0 && expectedHeadSeq != headSeq {
		return BranchResult{}, ErrBranchChanged
	}

	ev := event.Event{
		Kind:    event.KindHistoryBranched,
		Session: session,
		Time:    time.Now(),
		Payload: event.HistoryBranched{
			TargetUserSeq: targetUserSeq,
			Effects:       effects,
		},
	}
	seq, inserted, err := appendEventTx(ctx, tx, ev)
	if err != nil {
		return BranchResult{}, err
	}
	if !inserted {
		return BranchResult{}, ErrActiveUserMessageNotFound
	}
	ev.Seq = seq
	if err := tx.Commit(); err != nil {
		return BranchResult{}, fmt.Errorf("commit history branch: %w", err)
	}
	return BranchResult{
		Event:     ev,
		Effects:   effects,
		HeadSeq:   headSeq,
		FirstUser: firstUser,
		Applied:   true,
	}, nil
}

func appendEventTx(
	ctx context.Context,
	tx *sql.Tx,
	ev event.Event,
) (event.Seq, bool, error) {
	var deleted int
	err := tx.QueryRowContext(ctx, `
		SELECT 1 FROM deleted_sessions WHERE session_id = ?
	`, ev.Session).Scan(&deleted)
	switch {
	case err == nil:
		seq, seqErr := latestSequence(ctx, tx)
		return seq, false, seqErr
	case !errors.Is(err, sql.ErrNoRows):
		return 0, false, fmt.Errorf("check deleted session: %w", err)
	}

	payload, err := json.Marshal(ev.Payload)
	if err != nil {
		return 0, false, fmt.Errorf("marshal event payload: %w", err)
	}
	result, err := tx.ExecContext(ctx, `
		INSERT INTO events(session_id, run_id, kind, occurred_at_ns, payload_json)
		VALUES (?, ?, ?, ?, ?)
	`, ev.Session, ev.RunID, string(ev.Kind), ev.Time.UnixNano(), payload)
	if err != nil {
		return 0, false, fmt.Errorf("insert event: %w", err)
	}
	rawSeq, err := result.LastInsertId()
	if err != nil {
		return 0, false, fmt.Errorf("read event sequence: %w", err)
	}
	seq := event.Seq(rawSeq)
	ev.Seq = seq
	if err := applyProjectionTx(ctx, tx, ev, payload); err != nil {
		return 0, false, err
	}
	return seq, true, nil
}

func applyProjectionTx(
	ctx context.Context,
	tx *sql.Tx,
	ev event.Event,
	payload []byte,
) error {
	switch ev.Kind {
	case event.KindMessageEnd:
		item, ok := messageFromEvent(ev)
		if !ok {
			return errors.New("message_end payload is not a message")
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO message_projection(event_seq, session_id, role, active)
			VALUES (?, ?, ?, 1)
		`, int64(ev.Seq), ev.Session, string(item.Role)); err != nil {
			return fmt.Errorf("project message event: %w", err)
		}
	case event.KindHistoryBranched:
		branch, ok := historyBranchFromPayload(ev.Payload)
		if !ok {
			return errors.New("history_branched payload is invalid")
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE message_projection
			SET active = 0
			WHERE session_id = ? AND active = 1 AND event_seq >= ?
		`, ev.Session, int64(branch.TargetUserSeq)); err != nil {
			return fmt.Errorf("project history branch: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM compaction_checkpoints WHERE session_id = ?
		`, ev.Session); err != nil {
			return fmt.Errorf("invalidate compaction checkpoint: %w", err)
		}
	case event.KindUsageUpdated:
		usage, ok := usageFromPayload(ev.Payload)
		if !ok {
			return errors.New("usage_updated payload is invalid")
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO usage_records(
				event_seq, session_id, model, input_tokens, output_tokens,
				total_tokens, cached_tokens, occurred_at_ns
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		`, int64(ev.Seq), ev.Session, usage.Model, usage.InputTokens,
			usage.OutputTokens, usage.TotalTokens, usage.CachedTokens,
			ev.Time.UnixNano()); err != nil {
			return fmt.Errorf("project usage event: %w", err)
		}
	case event.KindCompactionCompleted:
		_, ok := checkpointFromPayload(ev.Payload)
		if !ok {
			return errors.New("compaction_completed payload is invalid")
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO compaction_checkpoints(session_id, event_seq, checkpoint_json)
			VALUES (?, ?, ?)
			ON CONFLICT(session_id) DO UPDATE SET
				event_seq = excluded.event_seq,
				checkpoint_json = excluded.checkpoint_json
		`, ev.Session, int64(ev.Seq), payload); err != nil {
			return fmt.Errorf("project compaction checkpoint: %w", err)
		}
	}
	return nil
}

type eventQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func loadActiveMessageEvents(
	ctx context.Context,
	queryer eventQueryer,
	session string,
) ([]event.Event, error) {
	rows, err := queryer.QueryContext(ctx, `
		SELECT e.seq, e.kind, e.session_id, e.run_id, e.occurred_at_ns, e.payload_json
		FROM message_projection AS p
		JOIN events AS e ON e.seq = p.event_seq
		WHERE p.session_id = ? AND p.active = 1
		ORDER BY p.event_seq
	`, session)
	if err != nil {
		return nil, fmt.Errorf("read active messages: %w", err)
	}
	defer rows.Close()
	return scanEvents(rows)
}

func loadCheckpoint(
	ctx context.Context,
	queryer eventQueryer,
	session string,
) (*compaction.Checkpoint, bool, error) {
	var data []byte
	err := queryer.QueryRowContext(ctx, `
		SELECT checkpoint_json
		FROM compaction_checkpoints
		WHERE session_id = ?
	`, session).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("read compaction checkpoint: %w", err)
	}
	var checkpoint compaction.Checkpoint
	if err := json.Unmarshal(data, &checkpoint); err != nil {
		return nil, false, fmt.Errorf("decode compaction checkpoint: %w", err)
	}
	return &checkpoint, true, nil
}

func scanEvents(rows *sql.Rows) ([]event.Event, error) {
	var events []event.Event
	for rows.Next() {
		var (
			rawSeq     int64
			rawKind    string
			session    string
			runID      string
			occurred   int64
			rawPayload []byte
		)
		if err := rows.Scan(
			&rawSeq, &rawKind, &session, &runID, &occurred, &rawPayload,
		); err != nil {
			return nil, fmt.Errorf("scan event: %w", err)
		}
		kind := event.Kind(rawKind)
		events = append(events, event.Event{
			Seq:     event.Seq(rawSeq),
			Kind:    kind,
			Session: session,
			RunID:   runID,
			Time:    time.Unix(0, occurred),
			Payload: decodePayload(kind, rawPayload),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate events: %w", err)
	}
	return events, nil
}

func latestSequence(ctx context.Context, queryer eventQueryer) (event.Seq, error) {
	var value sql.NullInt64
	err := queryer.QueryRowContext(ctx, `
		SELECT seq FROM sqlite_sequence WHERE name = 'events'
	`).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("read latest event sequence: %w", err)
	}
	return event.Seq(value.Int64), nil
}

func usageFromPayload(payload any) (provider.Usage, bool) {
	switch value := payload.(type) {
	case provider.Usage:
		return value, true
	case *provider.Usage:
		if value != nil {
			return *value, true
		}
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return provider.Usage{}, false
	}
	var usage provider.Usage
	if err := json.Unmarshal(data, &usage); err != nil {
		return provider.Usage{}, false
	}
	return usage, true
}

func checkpointFromPayload(payload any) (compaction.Checkpoint, bool) {
	switch value := payload.(type) {
	case compaction.Checkpoint:
		return value, true
	case *compaction.Checkpoint:
		if value != nil {
			return *value, true
		}
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return compaction.Checkpoint{}, false
	}
	var checkpoint compaction.Checkpoint
	if err := json.Unmarshal(data, &checkpoint); err != nil ||
		checkpoint.SessionID == "" {
		return compaction.Checkpoint{}, false
	}
	return checkpoint, true
}

var _ Store = (*SQLiteStore)(nil)
