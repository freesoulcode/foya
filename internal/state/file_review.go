package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/freesoulcode/foya/internal/event"
)

func (s *store) ResolveFileReview(
	ctx context.Context,
	session string,
	expectedThroughSeq event.Seq,
	action string,
	fileResults []event.RewindFileResult,
	journalID string,
) (event.Event, error) {
	if action != event.FileReviewKept && action != event.FileReviewUndone {
		return event.Event{}, errors.New("invalid file review action")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return event.Event{}, fmt.Errorf("begin file review resolution: %w", err)
	}
	defer tx.Rollback()

	var current sql.NullInt64
	if err := tx.QueryRowContext(ctx, `
		SELECT MAX(f.event_seq)
		FROM file_changes AS f
		JOIN message_projection AS p ON p.event_seq = f.event_seq
		WHERE f.session_id = ?
		  AND f.review_state = 'pending'
		  AND p.active = 1
	`, session).Scan(&current); err != nil {
		return event.Event{}, fmt.Errorf("read pending file review head: %w", err)
	}
	if !current.Valid {
		return event.Event{}, ErrNoPendingFileChanges
	}
	if event.Seq(current.Int64) != expectedThroughSeq {
		return event.Event{}, ErrFileReviewChanged
	}

	ev := event.Event{
		Kind:    event.KindFileReviewResolved,
		Session: session,
		Time:    time.Now(),
		Payload: event.FileReviewResolved{
			Action:     action,
			ThroughSeq: expectedThroughSeq,
			Files:      append([]event.RewindFileResult(nil), fileResults...),
		},
	}
	seq, inserted, err := appendEventTx(ctx, tx, ev)
	if err != nil {
		return event.Event{}, err
	}
	if !inserted {
		return event.Event{}, ErrNoPendingFileChanges
	}
	ev.Seq = seq
	if err := markFileRewindCommittedTx(
		ctx,
		tx,
		journalID,
		session,
		0,
		expectedThroughSeq,
	); err != nil {
		return event.Event{}, err
	}
	if err := tx.Commit(); err != nil {
		return event.Event{}, fmt.Errorf("commit file review resolution: %w", err)
	}
	return ev, nil
}
