package conversation

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

func (s *store) Rewind(
	ctx context.Context,
	session string,
	targetUserSeq Seq,
	confirm bool,
	expectedHeadSeq Seq,
	fileResults []RewindFileResult,
	journalID string,
) (RewindResult, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return RewindResult{}, fmt.Errorf("begin history rewind: %w", err)
	}
	defer tx.Rollback()

	active, err := loadActiveMessageEvents(ctx, tx, session)
	if err != nil {
		return RewindResult{}, err
	}
	targetIndex := -1
	var target Message
	for i, ev := range active {
		if ev.Seq != targetUserSeq {
			continue
		}
		item, ok := messageFromEvent(ev)
		if !ok || item.Role != RoleUser {
			break
		}
		item.EventSeq = uint64(ev.Seq)
		targetIndex = i
		target = item
		break
	}
	if targetIndex < 0 {
		return RewindResult{}, ErrActiveUserMessageNotFound
	}

	fileChanges, err := loadRewindFileChanges(ctx, tx, session, targetUserSeq)
	if err != nil {
		return RewindResult{}, err
	}
	firstUser := true
	for _, ev := range active[:targetIndex] {
		item, ok := messageFromEvent(ev)
		if ok && item.Role == RoleUser {
			firstUser = false
			break
		}
	}
	var headSeq Seq
	if len(active) > 0 {
		headSeq = active[len(active)-1].Seq
	}
	result := RewindResult{
		Message:     target,
		FileChanges: fileChanges,
		HeadSeq:     headSeq,
		FirstUser:   firstUser,
	}
	if !confirm {
		return result, nil
	}
	if expectedHeadSeq != headSeq {
		return RewindResult{}, ErrHistoryChanged
	}

	ev := Event{
		Kind:    KindHistoryRewound,
		Session: session,
		Time:    time.Now(),
		Payload: HistoryRewound{
			TargetUserSeq: targetUserSeq,
			Files:         append([]RewindFileResult(nil), fileResults...),
		},
	}
	seq, inserted, err := appendEventTx(ctx, tx, ev)
	if err != nil {
		return RewindResult{}, err
	}
	if !inserted {
		return RewindResult{}, ErrActiveUserMessageNotFound
	}
	ev.Seq = seq
	if err := markFileRewindCommittedTx(
		ctx,
		tx,
		journalID,
		session,
		targetUserSeq,
		expectedHeadSeq,
	); err != nil {
		return RewindResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return RewindResult{}, fmt.Errorf("commit history rewind: %w", err)
	}
	result.Event = ev
	result.Applied = true
	return result, nil
}

func appendEventTx(
	ctx context.Context,
	tx *sql.Tx,
	ev Event,
) (Seq, bool, error) {
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

	ev, err = persistEventFileChangeTx(ctx, tx, ev)
	if err != nil {
		return 0, false, err
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
	seq := Seq(rawSeq)
	ev.Seq = seq
	if err := applyProjectionTx(ctx, tx, ev, payload); err != nil {
		return 0, false, err
	}
	return seq, true, nil
}

func applyProjectionTx(
	ctx context.Context,
	tx *sql.Tx,
	ev Event,
	payload []byte,
) error {
	switch ev.Kind {
	case KindMessageEnd, KindMessageImported:
		item, ok := messageFromEvent(ev)
		if !ok {
			return fmt.Errorf("%s payload is not a message", ev.Kind)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO message_projection(event_seq, session_id, role, active)
			VALUES (?, ?, ?, 1)
		`, int64(ev.Seq), ev.Session, string(item.Role)); err != nil {
			return fmt.Errorf("project message event: %w", err)
		}
		if item.FileChange != nil {
			var beforeBlob any
			if item.FileChange.BeforeExists {
				beforeBlob = item.FileChange.BeforeBlob
			}
			reviewState := "pending"
			if ev.Kind == KindMessageImported {
				reviewState = FileReviewKept
			}
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO file_changes(
					event_seq, session_id, path, before_exists, before_mode, after_mode,
					before_blob_hash, after_blob_hash, review_state
				) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
			`, int64(ev.Seq), ev.Session, item.FileChange.Path,
				item.FileChange.BeforeExists, item.FileChange.BeforeMode,
				item.FileChange.AfterMode, beforeBlob,
				item.FileChange.AfterBlob, reviewState); err != nil {
				return fmt.Errorf("project file change: %w", err)
			}
			if err := pruneFileChangesTx(ctx, tx, ev.Session, time.Now()); err != nil {
				return err
			}
		}
		if ev.Kind == KindMessageEnd &&
			(item.Role == RoleUser || item.Role == RoleAssistant) {
			if err := incrementMessageLedgerTx(ctx, tx, ev); err != nil {
				return err
			}
		}
	case KindHistoryRewound:
		rewind, ok := historyRewindFromPayload(ev.Payload)
		if !ok {
			return fmt.Errorf("%s payload is invalid", ev.Kind)
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE message_projection
			SET active = 0
			WHERE session_id = ? AND active = 1 AND event_seq >= ?
		`, ev.Session, int64(rewind.TargetUserSeq)); err != nil {
			return fmt.Errorf("project history update: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM compaction_checkpoints WHERE session_id = ?
		`, ev.Session); err != nil {
			return fmt.Errorf("invalidate compaction checkpoint: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM context_accepted_boundaries WHERE session_id = ?
		`, ev.Session); err != nil {
			return fmt.Errorf("invalidate accepted context boundary: %w", err)
		}
	case KindFileReviewResolved:
		resolved, ok := fileReviewResolvedFromPayload(ev.Payload)
		if !ok {
			return errors.New("file_review_resolved payload is invalid")
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE file_changes
			SET review_state = ?
			WHERE session_id = ?
			  AND review_state = 'pending'
			  AND event_seq <= ?
			  AND event_seq IN (
				SELECT event_seq
				FROM message_projection
				WHERE session_id = ? AND active = 1
			  )
		`, resolved.Action, ev.Session, int64(resolved.ThroughSeq), ev.Session); err != nil {
			return fmt.Errorf("project file review resolution: %w", err)
		}
	case KindUsageUpdated:
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
		if err := incrementUsageLedgerTx(ctx, tx, ev, usage); err != nil {
			return err
		}
	case KindCompactionCompleted:
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
	case KindContextRequestAccepted:
		boundary, ok := acceptedBoundaryFromPayload(ev.Payload)
		if !ok || boundary.SessionID != ev.Session {
			return errors.New("context_request_accepted payload is invalid")
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO context_accepted_boundaries(
				session_id, event_seq, route, through_seq,
				input_tokens, output_tokens, payload_units, accepted_at_ns
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(session_id, route) DO UPDATE SET
				event_seq = excluded.event_seq,
				through_seq = excluded.through_seq,
				input_tokens = excluded.input_tokens,
				output_tokens = excluded.output_tokens,
				payload_units = excluded.payload_units,
				accepted_at_ns = excluded.accepted_at_ns
		`, ev.Session, int64(ev.Seq), boundary.Route,
			int64(boundary.ThroughSeq), boundary.InputTokens,
			boundary.OutputTokens, boundary.PayloadUnits,
			boundary.CreatedAt.UnixNano()); err != nil {
			return fmt.Errorf("project accepted context boundary: %w", err)
		}
	}
	return nil
}
