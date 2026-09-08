package conversation

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	model "github.com/freesoulcode/foya/internal/model"
)

func (s *store) UsageSummary(
	ctx context.Context,
	query UsageQuery,
) (UsageSummary, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return UsageSummary{}, fmt.Errorf("begin usage summary: %w", err)
	}
	defer tx.Rollback()

	summary, err := loadUsageSummary(ctx, tx, query)
	if err != nil {
		return UsageSummary{}, err
	}
	if err := tx.Commit(); err != nil {
		return UsageSummary{}, fmt.Errorf("commit usage summary read: %w", err)
	}
	return summary, nil
}

func (s *store) Checkpoint(
	ctx context.Context,
	session string,
) (*Checkpoint, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, false, fmt.Errorf("begin checkpoint read: %w", err)
	}
	defer tx.Rollback()

	active, err := loadActiveMessageEvents(ctx, tx, session)
	if err != nil {
		return nil, false, err
	}
	checkpoint, ok, err := resolveCheckpointTx(ctx, tx, session, active)
	if err != nil {
		return nil, false, err
	}
	if err := tx.Commit(); err != nil {
		return nil, false, fmt.Errorf("commit checkpoint read: %w", err)
	}
	return checkpoint, ok, nil
}

func (s *store) AcceptedBoundary(
	ctx context.Context,
	session string,
	route string,
) (AcceptedBoundary, bool, error) {
	var boundary AcceptedBoundary
	var (
		rawThrough  int64
		rawAccepted int64
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT route, through_seq, input_tokens, output_tokens,
		       payload_units, accepted_at_ns
		FROM context_accepted_boundaries
		WHERE session_id = ? AND route = ?
	`, session, route).Scan(
		&boundary.Route,
		&rawThrough,
		&boundary.InputTokens,
		&boundary.OutputTokens,
		&boundary.PayloadUnits,
		&rawAccepted,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return AcceptedBoundary{}, false, nil
	}
	if err != nil {
		return AcceptedBoundary{}, false, fmt.Errorf(
			"read accepted context boundary: %w",
			err,
		)
	}
	boundary.SessionID = session
	boundary.ThroughSeq = Seq(rawThrough)
	boundary.CreatedAt = time.Unix(0, rawAccepted)
	return boundary, true, nil
}

func incrementMessageLedgerTx(
	ctx context.Context,
	tx *sql.Tx,
	ev Event,
) error {
	date := ledgerDate(ev.Time)
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO usage_message_daily_ledger(date, message_count)
		VALUES (?, 1)
		ON CONFLICT(date) DO UPDATE SET
			message_count = usage_message_daily_ledger.message_count + 1
	`, date); err != nil {
		return fmt.Errorf("update message usage ledger: %w", err)
	}
	if err := recordUsageSessionDayTx(ctx, tx, ev.Session, date); err != nil {
		return err
	}
	return nil
}

func incrementUsageLedgerTx(
	ctx context.Context,
	tx *sql.Tx,
	ev Event,
	usage model.Usage,
) error {
	date := ledgerDate(ev.Time)
	total := usage.TotalTokens
	if total <= 0 {
		total = usage.InputTokens + usage.OutputTokens
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO usage_daily_ledger(
			date, model, input_tokens, output_tokens,
			total_tokens, cached_tokens, request_count
		) VALUES (?, ?, ?, ?, ?, ?, 1)
		ON CONFLICT(date, model) DO UPDATE SET
			input_tokens = usage_daily_ledger.input_tokens + excluded.input_tokens,
			output_tokens = usage_daily_ledger.output_tokens + excluded.output_tokens,
			total_tokens = usage_daily_ledger.total_tokens + excluded.total_tokens,
			cached_tokens = usage_daily_ledger.cached_tokens + excluded.cached_tokens,
			request_count = usage_daily_ledger.request_count + excluded.request_count
	`, date, usage.Model, usage.InputTokens, usage.OutputTokens,
		total, usage.CachedTokens); err != nil {
		return fmt.Errorf("update token usage ledger: %w", err)
	}
	if total > 0 {
		if err := recordUsageSessionDayTx(ctx, tx, ev.Session, date); err != nil {
			return err
		}
	}
	return nil
}

func recordUsageSessionDayTx(
	ctx context.Context,
	tx *sql.Tx,
	sessionID, date string,
) error {
	rootID, err := rootSessionIDForLedger(ctx, tx, sessionID)
	if err != nil {
		return err
	}
	if rootID == "" {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT OR IGNORE INTO usage_session_days(date, root_session_id)
		VALUES (?, ?)
	`, date, rootID); err != nil {
		return fmt.Errorf("record usage session day: %w", err)
	}
	return nil
}

func rootSessionIDForLedger(
	ctx context.Context,
	tx *sql.Tx,
	sessionID string,
) (string, error) {
	current := sessionID
	root := sessionID
	seen := make(map[string]struct{})
	for current != "" {
		if _, exists := seen[current]; exists {
			return root, nil
		}
		seen[current] = struct{}{}

		var parentID string
		err := tx.QueryRowContext(ctx, `
			SELECT parent_id FROM sessions WHERE id = ?
		`, current).Scan(&parentID)
		if errors.Is(err, sql.ErrNoRows) {
			return root, nil
		}
		if err != nil {
			return "", fmt.Errorf("resolve root session for usage ledger: %w", err)
		}
		root = current
		if parentID == "" {
			return root, nil
		}
		current = parentID
	}
	return root, nil
}

func ledgerDate(value time.Time) string {
	if value.IsZero() {
		value = time.Now()
	}
	return value.In(time.Local).Format(time.DateOnly)
}

func usageFromPayload(payload any) (model.Usage, bool) {
	switch value := payload.(type) {
	case model.Usage:
		return value, true
	case *model.Usage:
		if value != nil {
			return *value, true
		}
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return model.Usage{}, false
	}
	var usage model.Usage
	if err := json.Unmarshal(data, &usage); err != nil {
		return model.Usage{}, false
	}
	return usage, true
}
