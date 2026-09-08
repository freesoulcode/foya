package conversation

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

func loadUsageSummary(
	ctx context.Context,
	tx *sql.Tx,
	query UsageQuery,
) (UsageSummary, error) {
	var summary UsageSummary
	if err := tx.QueryRowContext(ctx, `
		SELECT
			COALESCE(SUM(input_tokens), 0),
			COALESCE(SUM(output_tokens), 0),
			COALESCE(SUM(total_tokens), 0),
			COALESCE(SUM(cached_tokens), 0)
		FROM usage_daily_ledger
		WHERE date >= ? AND date < ?
	`, query.RangeStart, query.RangeEnd).Scan(
		&summary.InputTokens,
		&summary.OutputTokens,
		&summary.TotalTokens,
		&summary.CachedTokens,
	); err != nil {
		return UsageSummary{}, fmt.Errorf("read usage token totals: %w", err)
	}

	var messageCount int64
	if err := tx.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(message_count), 0)
		FROM usage_message_daily_ledger
		WHERE date >= ? AND date < ?
	`, query.RangeStart, query.RangeEnd).Scan(&messageCount); err != nil {
		return UsageSummary{}, fmt.Errorf("read usage message total: %w", err)
	}
	summary.MessageCount = int(messageCount)

	var sessionCount int64
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(DISTINCT root_session_id)
		FROM usage_session_days
		WHERE date >= ? AND date < ?
	`, query.RangeStart, query.RangeEnd).Scan(&sessionCount); err != nil {
		return UsageSummary{}, fmt.Errorf("read usage session total: %w", err)
	}
	summary.SessionCount = int(sessionCount)

	var activeDays int64
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM (
			SELECT date
			FROM usage_message_daily_ledger
			WHERE date >= ? AND date < ? AND message_count > 0
			UNION
			SELECT date
			FROM usage_daily_ledger
			WHERE date >= ? AND date < ?
			GROUP BY date
			HAVING SUM(total_tokens) > 0
		)
	`, query.RangeStart, query.RangeEnd, query.RangeStart, query.RangeEnd).Scan(&activeDays); err != nil {
		return UsageSummary{}, fmt.Errorf("read usage active days: %w", err)
	}
	summary.ActiveDays = int(activeDays)

	activity, err := loadUsageActivity(ctx, tx, query.ActivityStart, query.Today)
	if err != nil {
		return UsageSummary{}, err
	}
	summary.Activity = activity

	models, err := loadUsageModels(ctx, tx, query.RangeStart, query.RangeEnd)
	if err != nil {
		return UsageSummary{}, err
	}
	summary.ModelUsage = models

	activeDates, err := loadUsageActiveDates(ctx, tx, query.Today)
	if err != nil {
		return UsageSummary{}, err
	}
	summary.CurrentStreak = usageStreak(query.Today, activeDates)
	return summary, nil
}

func loadUsageActivity(
	ctx context.Context,
	tx *sql.Tx,
	startDate, endDate string,
) ([]UsageDay, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT date, SUM(message_count), SUM(token_count)
		FROM (
			SELECT date, message_count, 0 AS token_count
			FROM usage_message_daily_ledger
			WHERE date >= ? AND date <= ?
			UNION ALL
			SELECT date, 0 AS message_count, total_tokens AS token_count
			FROM usage_daily_ledger
			WHERE date >= ? AND date <= ?
		)
		GROUP BY date
		ORDER BY date
	`, startDate, endDate, startDate, endDate)
	if err != nil {
		return nil, fmt.Errorf("read usage activity: %w", err)
	}
	defer rows.Close()

	var activity []UsageDay
	for rows.Next() {
		var day UsageDay
		if err := rows.Scan(&day.Date, &day.MessageCount, &day.TokenCount); err != nil {
			return nil, fmt.Errorf("scan usage activity: %w", err)
		}
		activity = append(activity, day)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate usage activity: %w", err)
	}
	return activity, nil
}

func loadUsageModels(
	ctx context.Context,
	tx *sql.Tx,
	startDate, endDate string,
) ([]UsageModelTotal, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT model, SUM(total_tokens), SUM(request_count)
		FROM usage_daily_ledger
		WHERE date >= ? AND date < ? AND model != ''
		GROUP BY model
		HAVING SUM(request_count) > 0
		ORDER BY SUM(total_tokens) DESC, SUM(request_count) DESC, model
	`, startDate, endDate)
	if err != nil {
		return nil, fmt.Errorf("read usage models: %w", err)
	}
	defer rows.Close()

	var models []UsageModelTotal
	for rows.Next() {
		var item UsageModelTotal
		if err := rows.Scan(&item.Model, &item.TokenCount, &item.RequestCount); err != nil {
			return nil, fmt.Errorf("scan usage model: %w", err)
		}
		models = append(models, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate usage models: %w", err)
	}
	return models, nil
}

func loadUsageActiveDates(
	ctx context.Context,
	tx *sql.Tx,
	today string,
) (map[string]struct{}, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT date
		FROM (
			SELECT date
			FROM usage_message_daily_ledger
			WHERE date <= ? AND message_count > 0
			UNION
			SELECT date
			FROM usage_daily_ledger
			WHERE date <= ?
			GROUP BY date
			HAVING SUM(total_tokens) > 0
		)
	`, today, today)
	if err != nil {
		return nil, fmt.Errorf("read usage active dates: %w", err)
	}
	defer rows.Close()

	active := make(map[string]struct{})
	for rows.Next() {
		var date string
		if err := rows.Scan(&date); err != nil {
			return nil, fmt.Errorf("scan usage active date: %w", err)
		}
		active[date] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate usage active dates: %w", err)
	}
	return active, nil
}

func usageStreak(today string, activeDates map[string]struct{}) int {
	day, err := time.ParseInLocation(time.DateOnly, today, time.Local)
	if err != nil {
		return 0
	}
	streak := 0
	for {
		if _, ok := activeDates[day.Format(time.DateOnly)]; !ok {
			return streak
		}
		streak++
		day = day.AddDate(0, 0, -1)
	}
}
