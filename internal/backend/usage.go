package backend

import (
	"context"
	"sort"
	"time"

	"github.com/freesoulcode/foya/internal/event"
	"github.com/freesoulcode/foya/internal/message"
	"github.com/freesoulcode/foya/internal/provider"
	"github.com/freesoulcode/foya/internal/session"
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
func (b *Backend) UsageStatistics(ctx context.Context, days int) (UsageStatistics, error) {
	return b.usageStatisticsAt(ctx, days, time.Now())
}

func (b *Backend) usageStatisticsAt(
	ctx context.Context,
	days int,
	now time.Time,
) (UsageStatistics, error) {
	location := now.Location()
	today := startOfDay(now.In(location))
	rangeStart := today.AddDate(0, 0, -(days - 1))
	rangeEnd := today.AddDate(0, 0, 1)
	activityStart := today.AddDate(0, 0, -(usageActivityDays - 1))

	result := UsageStatistics{RangeDays: days}
	daily := make(map[string]*DailyUsage, usageActivityDays)
	for offset := 0; offset < usageActivityDays; offset++ {
		date := activityStart.AddDate(0, 0, offset).Format(time.DateOnly)
		daily[date] = &DailyUsage{Date: date}
	}

	allSessions := b.sessions.List()
	sessionByID := make(map[string]*session.Session, len(allSessions))
	for _, item := range allSessions {
		sessionByID[item.ID] = item
	}

	activeSessions := make(map[string]struct{})
	activeDays := make(map[string]struct{})
	allActiveDays := make(map[string]struct{})
	modelTokens := make(map[string]int64)
	modelRequests := make(map[string]int)

	for _, item := range allSessions {
		events, err := b.log.Read(ctx, item.ID, 0)
		if err != nil {
			return UsageStatistics{}, err
		}
		rootID := rootSessionID(item.ID, sessionByID)
		for _, itemEvent := range events {
			if itemEvent.Time.IsZero() {
				continue
			}
			eventDay := startOfDay(itemEvent.Time.In(location))
			if eventDay.After(today) {
				continue
			}
			date := eventDay.Format(time.DateOnly)
			inRange := !eventDay.Before(rangeStart) && eventDay.Before(rangeEnd)
			inActivity := !eventDay.Before(activityStart)

			if isConversationMessage(itemEvent) {
				allActiveDays[date] = struct{}{}
				if inActivity {
					daily[date].MessageCount++
				}
				if inRange {
					result.MessageCount++
					activeSessions[rootID] = struct{}{}
					activeDays[date] = struct{}{}
				}
				continue
			}

			usage, ok := usageFromEvent(itemEvent)
			if !ok {
				continue
			}
			total := usage.TotalTokens
			if total <= 0 {
				total = usage.InputTokens + usage.OutputTokens
			}
			if total > 0 {
				allActiveDays[date] = struct{}{}
				if inActivity {
					daily[date].TokenCount += total
				}
			}
			if !inRange {
				continue
			}
			result.TotalTokens += total
			result.InputTokens += usage.InputTokens
			result.OutputTokens += usage.OutputTokens
			result.CachedTokens += usage.CachedTokens
			if usage.Model != "" {
				modelRequests[usage.Model]++
			}
			if total > 0 {
				activeSessions[rootID] = struct{}{}
				activeDays[date] = struct{}{}
				if usage.Model != "" {
					modelTokens[usage.Model] += total
				}
			}
		}
	}

	result.SessionCount = len(activeSessions)
	result.ActiveDays = len(activeDays)
	result.CurrentStreak = currentUsageStreak(today, allActiveDays)
	result.ModelUsage = modelUsageRanking(
		result.TotalTokens,
		modelTokens,
		modelRequests,
	)
	if len(result.ModelUsage) > 0 {
		result.MostUsedModel = result.ModelUsage[0].Model
		result.MostUsedModelShare = result.ModelUsage[0].Share
	}
	result.Activity = make([]DailyUsage, 0, usageActivityDays)
	for offset := 0; offset < usageActivityDays; offset++ {
		date := activityStart.AddDate(0, 0, offset).Format(time.DateOnly)
		result.Activity = append(result.Activity, *daily[date])
	}
	return result, nil
}

func startOfDay(value time.Time) time.Time {
	year, month, day := value.Date()
	return time.Date(year, month, day, 0, 0, 0, 0, value.Location())
}

func rootSessionID(id string, sessions map[string]*session.Session) string {
	current := id
	seen := make(map[string]struct{})
	for {
		if _, exists := seen[current]; exists {
			return id
		}
		seen[current] = struct{}{}
		item, exists := sessions[current]
		if !exists || item.ParentID == "" {
			return current
		}
		current = item.ParentID
	}
}

func isConversationMessage(itemEvent event.Event) bool {
	if itemEvent.Kind != event.KindMessageEnd {
		return false
	}
	var item message.Message
	switch value := itemEvent.Payload.(type) {
	case message.Message:
		item = value
	case *message.Message:
		if value == nil {
			return false
		}
		item = *value
	default:
		return false
	}
	return item.Role == message.RoleUser || item.Role == message.RoleAssistant
}

func usageFromEvent(itemEvent event.Event) (provider.Usage, bool) {
	if itemEvent.Kind != event.KindUsageUpdated {
		return provider.Usage{}, false
	}
	switch value := itemEvent.Payload.(type) {
	case provider.Usage:
		return value, true
	case *provider.Usage:
		if value != nil {
			return *value, true
		}
	}
	return provider.Usage{}, false
}

func currentUsageStreak(today time.Time, activeDays map[string]struct{}) int {
	streak := 0
	for day := today; ; day = day.AddDate(0, 0, -1) {
		if _, active := activeDays[day.Format(time.DateOnly)]; !active {
			break
		}
		streak++
	}
	return streak
}

func modelUsageRanking(
	totalTokens int64,
	modelTokens map[string]int64,
	modelRequests map[string]int,
) []ModelUsage {
	models := make([]string, 0, len(modelRequests))
	for model := range modelRequests {
		models = append(models, model)
	}
	sort.Slice(models, func(i, j int) bool {
		if modelTokens[models[i]] != modelTokens[models[j]] {
			return modelTokens[models[i]] > modelTokens[models[j]]
		}
		if modelRequests[models[i]] != modelRequests[models[j]] {
			return modelRequests[models[i]] > modelRequests[models[j]]
		}
		return models[i] < models[j]
	})

	ranking := make([]ModelUsage, 0, len(models))
	for _, model := range models {
		share := 0
		if totalTokens > 0 {
			share = int((modelTokens[model]*100 + totalTokens/2) / totalTokens)
		}
		ranking = append(ranking, ModelUsage{
			Model:        model,
			TokenCount:   modelTokens[model],
			RequestCount: modelRequests[model],
			Share:        share,
		})
	}
	return ranking
}
