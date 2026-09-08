package conversation

import (
	"context"
	"fmt"
)

func (s *store) History(
	ctx context.Context,
	session string,
) ([]Message, error) {
	events, err := loadActiveMessageEvents(ctx, s.db, session)
	if err != nil {
		return nil, err
	}
	var messages []Message
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

func (s *store) ModelHistory(
	ctx context.Context,
	session string,
) ([]Message, error) {
	projection, err := s.ModelContext(ctx, session, "")
	if err != nil {
		return nil, err
	}
	return projection.Messages, nil
}

func (s *store) ModelContext(
	ctx context.Context,
	session string,
	route string,
) (ModelProjection, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ModelProjection{}, fmt.Errorf("begin model context: %w", err)
	}
	defer tx.Rollback()

	events, err := loadActiveMessageEvents(ctx, tx, session)
	if err != nil {
		return ModelProjection{}, err
	}
	checkpoint, ok, err := resolveCheckpointTx(ctx, tx, session, events)
	if err != nil {
		return ModelProjection{}, err
	}
	if err := tx.Commit(); err != nil {
		return ModelProjection{}, fmt.Errorf("commit model context read: %w", err)
	}
	if !ok {
		return ProjectForRoute(events, nil, route), nil
	}
	return ProjectForRoute(events, checkpoint, route), nil
}

func (s *store) Events(
	ctx context.Context,
	session string,
) ([]Event, error) {
	return loadActiveMessageEvents(ctx, s.db, session)
}

func (s *store) ActiveMessageEvent(
	ctx context.Context,
	session string,
	seq Seq,
) (Event, bool, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT e.seq, e.kind, e.session_id, e.run_id, e.occurred_at_ns, e.payload_json
		FROM message_projection AS p
		JOIN events AS e ON e.seq = p.event_seq
		WHERE p.session_id = ? AND p.event_seq = ? AND p.active = 1
	`, session, int64(seq))
	if err != nil {
		return Event{}, false, fmt.Errorf("read active message event: %w", err)
	}
	defer rows.Close()
	events, err := scanEvents(rows)
	if err != nil {
		return Event{}, false, err
	}
	if len(events) == 0 {
		return Event{}, false, nil
	}
	return events[0], true, nil
}
