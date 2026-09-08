package conversation

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

func (s *store) ResolveFileReview(
	ctx context.Context,
	session string,
	expectedThroughSeq Seq,
	action string,
	fileResults []RewindFileResult,
	journalID string,
) (Event, error) {
	if action != FileReviewKept && action != FileReviewUndone {
		return Event{}, errors.New("invalid file review action")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Event{}, fmt.Errorf("begin file review resolution: %w", err)
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
		return Event{}, fmt.Errorf("read pending file review head: %w", err)
	}
	if !current.Valid {
		return Event{}, ErrNoPendingFileChanges
	}
	if Seq(current.Int64) != expectedThroughSeq {
		return Event{}, ErrFileReviewChanged
	}

	ev := Event{
		Kind:    KindFileReviewResolved,
		Session: session,
		Time:    time.Now(),
		Payload: FileReviewResolved{
			Action:     action,
			ThroughSeq: expectedThroughSeq,
			Files:      append([]RewindFileResult(nil), fileResults...),
		},
	}
	seq, inserted, err := appendEventTx(ctx, tx, ev)
	if err != nil {
		return Event{}, err
	}
	if !inserted {
		return Event{}, ErrNoPendingFileChanges
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
		return Event{}, err
	}
	if err := tx.Commit(); err != nil {
		return Event{}, fmt.Errorf("commit file review resolution: %w", err)
	}
	return ev, nil
}
