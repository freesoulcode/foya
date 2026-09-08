package kernel

import (
	"context"
	"time"

	conversation "github.com/freesoulcode/foya/internal/conversation"
)

const usageActivityDays = 365

// DailyUsage is one local calendar day's activity in the usage heatmap.
type DailyUsage struct {
	Date         string `json:"date"`
	MessageCount int    `json:"message_count"`
	TokenCount   int64  `json:"token_count"`
}

// ModelUsage is one model's usage within the selected statistics range.
type ModelUsage struct {
	Model        string `json:"model"`
	TokenCount   int64  `json:"token_count"`
	RequestCount int    `json:"request_count"`
	Share        int    `json:"share"`
}

// UsageStatistics summarizes application usage for a recent time range.
type UsageStatistics struct {
	RangeDays          int          `json:"range_days"`
	TotalTokens        int64        `json:"total_tokens"`
	InputTokens        int64        `json:"input_tokens"`
	OutputTokens       int64        `json:"output_tokens"`
	CachedTokens       int64        `json:"cached_tokens"`
	SessionCount       int          `json:"session_count"`
	MessageCount       int          `json:"message_count"`
	ActiveDays         int          `json:"active_days"`
	CurrentStreak      int          `json:"current_streak"`
	MostUsedModel      string       `json:"most_used_model,omitempty"`
	MostUsedModelShare int          `json:"most_used_model_share"`
	Activity           []DailyUsage `json:"activity"`
	ModelUsage         []ModelUsage `json:"model_usage"`
}

// UsageStatistics returns recent usage totals and a year of daily activity.
func (b *Service) UsageStatistics(ctx context.Context, days int) (UsageStatistics, error) {
	return b.usageStatisticsAt(ctx, days, time.Now())
}

func (b *Service) usageStatisticsAt(
	ctx context.Context,
	days int,
	now time.Time,
) (UsageStatistics, error) {
	location := now.Location()
	today := startOfDay(now.In(location))
	rangeStart := today.AddDate(0, 0, -(days - 1))
	rangeEnd := today.AddDate(0, 0, 1)
	activityStart := today.AddDate(0, 0, -(usageActivityDays - 1))

	summary, err := b.log.UsageSummary(ctx, conversation.UsageQuery{
		RangeStart:    rangeStart.Format(time.DateOnly),
		RangeEnd:      rangeEnd.Format(time.DateOnly),
		ActivityStart: activityStart.Format(time.DateOnly),
		Today:         today.Format(time.DateOnly),
	})
	if err != nil {
		return UsageStatistics{}, err
	}

	result := UsageStatistics{
		RangeDays:     days,
		TotalTokens:   summary.TotalTokens,
		InputTokens:   summary.InputTokens,
		OutputTokens:  summary.OutputTokens,
		CachedTokens:  summary.CachedTokens,
		SessionCount:  summary.SessionCount,
		MessageCount:  summary.MessageCount,
		ActiveDays:    summary.ActiveDays,
		CurrentStreak: summary.CurrentStreak,
		ModelUsage:    modelUsageRanking(summary.TotalTokens, summary.ModelUsage),
	}
	if len(result.ModelUsage) > 0 {
		result.MostUsedModel = result.ModelUsage[0].Model
		result.MostUsedModelShare = result.ModelUsage[0].Share
	}

	daily := make(map[string]DailyUsage, len(summary.Activity))
	for _, day := range summary.Activity {
		daily[day.Date] = DailyUsage{
			Date:         day.Date,
			MessageCount: day.MessageCount,
			TokenCount:   day.TokenCount,
		}
	}
	result.Activity = make([]DailyUsage, 0, usageActivityDays)
	for offset := 0; offset < usageActivityDays; offset++ {
		date := activityStart.AddDate(0, 0, offset).Format(time.DateOnly)
		day, ok := daily[date]
		if !ok {
			day = DailyUsage{Date: date}
		}
		result.Activity = append(result.Activity, day)
	}
	return result, nil
}

func startOfDay(value time.Time) time.Time {
	year, month, day := value.Date()
	return time.Date(year, month, day, 0, 0, 0, 0, value.Location())
}

func modelUsageRanking(
	totalTokens int64,
	models []conversation.UsageModelTotal,
) []ModelUsage {
	ranking := make([]ModelUsage, 0, len(models))
	for _, model := range models {
		share := 0
		if totalTokens > 0 {
			share = int((model.TokenCount*100 + totalTokens/2) / totalTokens)
		}
		ranking = append(ranking, ModelUsage{
			Model:        model.Model,
			TokenCount:   model.TokenCount,
			RequestCount: model.RequestCount,
			Share:        share,
		})
	}
	return ranking
}
