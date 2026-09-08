package conversation

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

func (s *store) RecordCheckpoint(
	ctx context.Context,
	checkpoint Checkpoint,
) (Event, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Event{}, fmt.Errorf("begin checkpoint: %w", err)
	}
	defer tx.Rollback()

	active, err := loadActiveMessageEvents(ctx, tx, checkpoint.SessionID)
	if err != nil {
		return Event{}, err
	}
	if !ValidateCheckpoint(active, checkpoint) {
		return Event{}, fmt.Errorf("compaction checkpoint does not match source events")
	}
	current, ok, err := resolveCheckpointTx(ctx, tx, checkpoint.SessionID, active)
	if err != nil {
		return Event{}, err
	}
	if ok {
		if checkpoint.ThroughSeq < current.ThroughSeq {
			return Event{}, fmt.Errorf("compaction checkpoint coverage moved backwards")
		}
		if current.CheckpointID != "" &&
			checkpoint.PreviousCheckpointID != current.CheckpointID {
			return Event{}, fmt.Errorf("compaction checkpoint lineage is stale")
		}
		if checkpoint.ThroughSeq == current.ThroughSeq &&
			checkpoint.PreviousCheckpointID == "" {
			return Event{}, fmt.Errorf("same-coverage checkpoint requires explicit lineage")
		}
	}
	ev := Event{
		Kind:    KindCompactionCompleted,
		Session: checkpoint.SessionID,
		Payload: checkpoint,
		Time:    checkpoint.CreatedAt,
	}
	seq, inserted, err := appendEventTx(ctx, tx, ev)
	if err != nil {
		return Event{}, err
	}
	if !inserted {
		return Event{}, errors.New("cannot checkpoint a deleted session")
	}
	ev.Seq = seq
	if err := tx.Commit(); err != nil {
		return Event{}, fmt.Errorf("commit checkpoint: %w", err)
	}
	return ev, nil
}

func (s *store) ImportMessages(
	ctx context.Context,
	session string,
	sourceSession string,
	throughSeq Seq,
	messages []Message,
) ([]Event, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin history import: %w", err)
	}
	defer tx.Rollback()

	now := time.Now()
	events := make([]Event, 0, len(messages)+1)
	forked := Event{
		Kind:    KindSessionForked,
		Session: session,
		Time:    now,
		Payload: SessionForked{
			SourceSessionID: sourceSession,
			ThroughSeq:      throughSeq,
			ForkedAt:        now,
			MessageCount:    len(messages),
		},
	}
	seq, inserted, err := appendEventTx(ctx, tx, forked)
	if err != nil {
		return nil, err
	}
	if !inserted {
		return nil, errors.New("cannot import history into a deleted session")
	}
	forked.Seq = seq
	events = append(events, forked)

	for _, item := range messages {
		item.EventSeq = 0
		ev := Event{
			Kind:    KindMessageImported,
			Session: session,
			Time:    now,
			Payload: item,
		}
		seq, inserted, err := appendEventTx(ctx, tx, ev)
		if err != nil {
			return nil, err
		}
		if !inserted {
			return nil, errors.New("cannot import history into a deleted session")
		}
		ev.Seq = seq
		events = append(events, ev)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit history import: %w", err)
	}
	return events, nil
}

type eventQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func loadActiveMessageEvents(
	ctx context.Context,
	queryer eventQueryer,
	session string,
) ([]Event, error) {
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
) (*Checkpoint, bool, error) {
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
	var checkpoint Checkpoint
	if err := json.Unmarshal(data, &checkpoint); err != nil {
		return nil, false, fmt.Errorf("decode compaction checkpoint: %w", err)
	}
	return &checkpoint, true, nil
}

func resolveCheckpointTx(
	ctx context.Context,
	tx *sql.Tx,
	session string,
	active []Event,
) (*Checkpoint, bool, error) {
	checkpoint, ok, loadErr := loadCheckpoint(ctx, tx, session)
	if loadErr != nil && !isCheckpointDecodeError(loadErr) {
		return nil, false, loadErr
	}
	if loadErr == nil && ok && ValidateCheckpoint(active, *checkpoint) {
		return checkpoint, true, nil
	}

	recovered, eventSeq, recoveredOK, err := recoverCheckpointFromEvents(
		ctx,
		tx,
		session,
		active,
	)
	if err != nil {
		return nil, false, err
	}
	if recoveredOK {
		payload, err := json.Marshal(recovered)
		if err != nil {
			return nil, false, fmt.Errorf("marshal recovered checkpoint: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO compaction_checkpoints(session_id, event_seq, checkpoint_json)
			VALUES (?, ?, ?)
			ON CONFLICT(session_id) DO UPDATE SET
				event_seq = excluded.event_seq,
				checkpoint_json = excluded.checkpoint_json
		`, session, int64(eventSeq), payload); err != nil {
			return nil, false, fmt.Errorf("repair compaction checkpoint: %w", err)
		}
		return recovered, true, nil
	}

	if ok || loadErr != nil {
		if _, err := tx.ExecContext(
			ctx,
			`DELETE FROM compaction_checkpoints WHERE session_id = ?`,
			session,
		); err != nil {
			return nil, false, fmt.Errorf("discard invalid compaction checkpoint: %w", err)
		}
	}
	return nil, false, nil
}

func recoverCheckpointFromEvents(
	ctx context.Context,
	tx *sql.Tx,
	session string,
	active []Event,
) (*Checkpoint, Seq, bool, error) {
	var (
		rawSeq  int64
		payload []byte
	)
	err := tx.QueryRowContext(ctx, `
		SELECT seq, payload_json
		FROM events
		WHERE session_id = ?
		  AND kind = ?
		  AND seq > COALESCE((
			SELECT MAX(seq)
			FROM events
			WHERE session_id = ? AND kind = ?
		  ), 0)
		ORDER BY seq DESC
		LIMIT 1
	`, session, string(KindCompactionCompleted),
		session, string(KindHistoryRewound)).Scan(&rawSeq, &payload)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, 0, false, nil
	}
	if err != nil {
		return nil, 0, false, fmt.Errorf("read checkpoint event: %w", err)
	}
	var candidate Checkpoint
	if err := json.Unmarshal(payload, &candidate); err != nil ||
		!ValidateCheckpoint(active, candidate) {
		return nil, 0, false, nil
	}
	return &candidate, Seq(rawSeq), true, nil
}

func isCheckpointDecodeError(err error) bool {
	var syntaxError *json.SyntaxError
	if errors.As(err, &syntaxError) {
		return true
	}
	var typeError *json.UnmarshalTypeError
	return errors.As(err, &typeError)
}

func checkpointFromPayload(payload any) (Checkpoint, bool) {
	switch value := payload.(type) {
	case Checkpoint:
		return value, true
	case *Checkpoint:
		if value != nil {
			return *value, true
		}
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return Checkpoint{}, false
	}
	var checkpoint Checkpoint
	if err := json.Unmarshal(data, &checkpoint); err != nil ||
		checkpoint.SessionID == "" {
		return Checkpoint{}, false
	}
	return checkpoint, true
}

func acceptedBoundaryFromPayload(payload any) (AcceptedBoundary, bool) {
	switch value := payload.(type) {
	case AcceptedBoundary:
		return value, true
	case *AcceptedBoundary:
		if value != nil {
			return *value, true
		}
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return AcceptedBoundary{}, false
	}
	var boundary AcceptedBoundary
	if err := json.Unmarshal(data, &boundary); err != nil ||
		boundary.SessionID == "" ||
		boundary.Route == "" ||
		boundary.ThroughSeq == 0 {
		return AcceptedBoundary{}, false
	}
	return boundary, true
}
