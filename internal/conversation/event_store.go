package conversation

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

func scanEvents(rows *sql.Rows) ([]Event, error) {
	var events []Event
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
		kind := Kind(rawKind)
		events = append(events, Event{
			Seq:     Seq(rawSeq),
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

func latestSequence(ctx context.Context, queryer eventQueryer) (Seq, error) {
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
	return Seq(value.Int64), nil
}

var _ Store = (*store)(nil)
