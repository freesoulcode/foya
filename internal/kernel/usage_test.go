package kernel

import (
	"context"
	"testing"
	"time"

	conversation "github.com/freesoulcode/foya/internal/conversation"

	model "github.com/freesoulcode/foya/internal/model"

	"github.com/freesoulcode/foya/internal/testkit"
)

func TestUsageStatisticsAggregatesRecentActivity(t *testing.T) {
	ctx := context.Background()
	sessions, log := newUsageTestRuntime(t)
	root, err := sessions.Create(conversation.CreateOptions{Model: "model-a"})
	if err != nil {
		t.Fatal(err)
	}
	child, err := sessions.Create(conversation.CreateOptions{
		ParentID: root.ID,
		Model:    "model-b",
	})
	if err != nil {
		t.Fatal(err)
	}
	older, err := sessions.Create(conversation.CreateOptions{Model: "model-c"})
	if err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, time.September, 4, 15, 0, 0, 0, time.Local)
	appendUsageEvent(t, log, root.ID, now, conversation.KindMessageEnd, conversation.Message{
		Role: conversation.RoleUser, Content: "today",
	})
	appendUsageEvent(t, log, root.ID, now, conversation.KindUsageUpdated, model.Usage{
		Model: "model-a", InputTokens: 60, OutputTokens: 40, TotalTokens: 100,
	})
	appendUsageEvent(t, log, child.ID, now.AddDate(0, 0, -1), conversation.KindMessageEnd, conversation.Message{
		Role: conversation.RoleAssistant, Content: "yesterday",
	})
	appendUsageEvent(t, log, child.ID, now.AddDate(0, 0, -1), conversation.KindUsageUpdated, model.Usage{
		Model: "model-b", InputTokens: 150, OutputTokens: 50, TotalTokens: 200,
	})
	appendUsageEvent(t, log, root.ID, now.AddDate(0, 0, -2), conversation.KindMessageEnd, conversation.Message{
		Role: conversation.RoleTool, Content: "not a chat message",
	})
	appendUsageEvent(t, log, root.ID, now.AddDate(0, 0, -2), conversation.KindUsageUpdated, model.Usage{
		Model: "model-b", InputTokens: 50, OutputTokens: 50, TotalTokens: 100,
	})
	appendUsageEvent(t, log, older.ID, now.AddDate(0, 0, -10), conversation.KindMessageEnd, conversation.Message{
		Role: conversation.RoleUser, Content: "older",
	})
	appendUsageEvent(t, log, older.ID, now.AddDate(0, 0, -10), conversation.KindUsageUpdated, model.Usage{
		Model: "model-c", InputTokens: 500, OutputTokens: 500, TotalTokens: 1_000,
	})

	be := &Service{sessions: sessions, log: log}
	recent, err := be.usageStatisticsAt(ctx, 7, now)
	if err != nil {
		t.Fatal(err)
	}
	if recent.TotalTokens != 400 ||
		recent.InputTokens != 260 ||
		recent.OutputTokens != 140 {
		t.Fatalf("recent tokens = %#v", recent)
	}
	if recent.SessionCount != 1 || recent.MessageCount != 2 || recent.ActiveDays != 3 {
		t.Fatalf("recent counts = %#v", recent)
	}
	if recent.CurrentStreak != 3 {
		t.Fatalf("current streak = %d, want 3", recent.CurrentStreak)
	}
	if recent.MostUsedModel != "model-b" || recent.MostUsedModelShare != 75 {
		t.Fatalf("most used model = %q (%d%%)", recent.MostUsedModel, recent.MostUsedModelShare)
	}
	if len(recent.ModelUsage) != 2 ||
		recent.ModelUsage[0].Model != "model-b" ||
		recent.ModelUsage[0].TokenCount != 300 ||
		recent.ModelUsage[0].RequestCount != 2 ||
		recent.ModelUsage[1].Model != "model-a" {
		t.Fatalf("model usage = %#v", recent.ModelUsage)
	}
	if len(recent.Activity) != usageActivityDays {
		t.Fatalf("activity length = %d, want %d", len(recent.Activity), usageActivityDays)
	}
	last := recent.Activity[len(recent.Activity)-1]
	if last.Date != "2026-09-04" || last.MessageCount != 1 || last.TokenCount != 100 {
		t.Fatalf("today activity = %#v", last)
	}

	monthly, err := be.usageStatisticsAt(ctx, 30, now)
	if err != nil {
		t.Fatal(err)
	}
	if monthly.TotalTokens != 1_400 ||
		monthly.SessionCount != 2 ||
		monthly.MessageCount != 3 ||
		monthly.ActiveDays != 4 {
		t.Fatalf("monthly statistics = %#v", monthly)
	}
}

func TestUsageStatisticsRetainsHistoricalCountsAfterSessionDeletion(t *testing.T) {
	ctx := context.Background()
	sessions, log := newUsageTestRuntime(t)
	item, err := sessions.Create(conversation.CreateOptions{Model: "model-a"})
	if err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, time.September, 4, 15, 0, 0, 0, time.Local)
	appendUsageEvent(t, log, item.ID, now, conversation.KindMessageEnd, conversation.Message{
		Role: conversation.RoleUser, Content: "question",
	})
	appendUsageEvent(t, log, item.ID, now, conversation.KindMessageEnd, conversation.Message{
		Role: conversation.RoleAssistant, Content: "answer",
	})
	appendUsageEvent(t, log, item.ID, now, conversation.KindUsageUpdated, model.Usage{
		Model: "model-a", InputTokens: 90, OutputTokens: 30, TotalTokens: 120,
	})

	be := &Service{sessions: sessions, log: log}
	before, err := be.usageStatisticsAt(ctx, 7, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := sessions.Delete(item.ID); err != nil {
		t.Fatal(err)
	}
	if err := log.Delete(ctx, item.ID); err != nil {
		t.Fatal(err)
	}
	after, err := be.usageStatisticsAt(ctx, 7, now)
	if err != nil {
		t.Fatal(err)
	}
	if after.TotalTokens != before.TotalTokens ||
		after.InputTokens != before.InputTokens ||
		after.OutputTokens != before.OutputTokens ||
		after.SessionCount != before.SessionCount ||
		after.MessageCount != before.MessageCount ||
		after.ActiveDays != before.ActiveDays ||
		after.CurrentStreak != before.CurrentStreak ||
		len(after.ModelUsage) != 1 ||
		after.ModelUsage[0] != before.ModelUsage[0] {
		t.Fatalf("usage changed after deletion: before=%#v after=%#v", before, after)
	}
}

func appendUsageEvent(
	t *testing.T,
	log conversation.Store,
	sessionID string,
	at time.Time,
	kind conversation.Kind,
	payload any,
) {
	t.Helper()
	if _, err := log.Append(context.Background(), conversation.Event{
		Kind: kind, Session: sessionID, Time: at, Payload: payload,
	}); err != nil {
		t.Fatal(err)
	}
}

func newUsageTestRuntime(t testing.TB) (conversation.Manager, conversation.Store) {
	t.Helper()
	db := testkit.OpenDatabase(t)
	manager, err := conversation.NewManager(db)
	if err != nil {
		t.Fatal(err)
	}
	return manager, conversation.NewStore(db)
}

func TestUsageStatisticsReturnsZeroValuesWithoutActivity(t *testing.T) {
	now := time.Date(2026, time.September, 4, 12, 0, 0, 0, time.Local)
	be := &Service{
		sessions: newTestSessionManager(t),
		log:      newTestStore(t),
	}
	stats, err := be.usageStatisticsAt(context.Background(), 30, now)
	if err != nil {
		t.Fatal(err)
	}
	if stats.TotalTokens != 0 ||
		stats.SessionCount != 0 ||
		stats.MessageCount != 0 ||
		stats.MostUsedModel != "" ||
		len(stats.ModelUsage) != 0 ||
		len(stats.Activity) != usageActivityDays {
		t.Fatalf("empty statistics = %#v", stats)
	}
}
