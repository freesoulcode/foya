package compaction

import (
	"strings"
	"testing"
	"time"

	"github.com/freesoulcode/foya/internal/event"
	"github.com/freesoulcode/foya/internal/message"
	"github.com/freesoulcode/foya/internal/provider"
)

func TestDeriveBudget(t *testing.T) {
	t.Run("known small window", func(t *testing.T) {
		got := DeriveBudget(8_192)
		if got.ReserveTokens != 2_048 || got.HighWater != 6_144 || got.Estimated {
			t.Fatalf("budget = %#v", got)
		}
	})
	t.Run("known large window caps reserve", func(t *testing.T) {
		got := DeriveBudget(256_000)
		if got.ReserveTokens != 16_384 || got.HighWater != 239_616 {
			t.Fatalf("budget = %#v", got)
		}
	})
	t.Run("unknown window uses fallback", func(t *testing.T) {
		got := DeriveBudget(0)
		if got.ContextWindow != 48_384 || got.HighWater != 32_000 || !got.Estimated {
			t.Fatalf("budget = %#v", got)
		}
	})
}

func TestPlanAndProjectPreserveLatestTurn(t *testing.T) {
	events := []event.Event{
		messageEventForTest(1, message.RoleUser, "old question"),
		messageEventForTest(2, message.RoleAssistant, "old answer"),
		messageEventForTest(3, message.RoleUser, "current question"),
	}
	plan, ok := BuildPlan(events, nil, true)
	if !ok {
		t.Fatal("expected a compaction plan")
	}
	if plan.ThroughSeq != 2 || len(plan.SourceMessages) != 2 {
		t.Fatalf("plan = %#v", plan)
	}
	checkpoint := Checkpoint{
		SessionID:    "session-1",
		ThroughSeq:   plan.ThroughSeq,
		SourceDigest: plan.SourceDigest,
		Summary:      "old work summarized",
		CreatedAt:    time.Now(),
	}
	projected := Project(events, &checkpoint)
	if len(projected) != 2 {
		t.Fatalf("projected messages = %#v", projected)
	}
	if projected[0].Role != message.RoleSystem ||
		!strings.Contains(projected[0].Content, "old work summarized") {
		t.Fatalf("checkpoint message = %#v", projected[0])
	}
	if projected[1].Role != message.RoleUser || projected[1].Content != "current question" {
		t.Fatalf("tail message = %#v", projected[1])
	}

	if _, ok := BuildPlan(events, &checkpoint, true); ok {
		t.Fatal("unchanged covered prefix must not be summarized again")
	}
}

func TestInvalidCheckpointFallsBackToRawHistory(t *testing.T) {
	events := []event.Event{
		messageEventForTest(1, message.RoleUser, "question"),
		messageEventForTest(2, message.RoleAssistant, "answer"),
	}
	checkpoint := Checkpoint{
		SessionID:    "session-1",
		ThroughSeq:   2,
		SourceDigest: "wrong",
		Summary:      "untrusted summary",
	}
	projected := Project(events, &checkpoint)
	if len(projected) != 2 || projected[0].Content != "question" {
		t.Fatalf("invalid checkpoint should fall back to raw history: %#v", projected)
	}
}

func TestBoundToolResultsDoesNotMutateCanonicalMessages(t *testing.T) {
	original := []message.Message{{
		Role:       message.RoleTool,
		ToolCallID: "call-1",
		Content:    strings.Repeat("x", 20_000),
	}}
	bounded, rewritten := BoundToolResults(original, 256)
	if rewritten != 1 {
		t.Fatalf("rewritten = %d", rewritten)
	}
	if len(bounded[0].Content) >= len(original[0].Content) {
		t.Fatalf("bounded result was not reduced")
	}
	if len(original[0].Content) != 20_000 {
		t.Fatal("canonical message was mutated")
	}
}

func TestEstimateNextRequestTokensUsesSignedDelta(t *testing.T) {
	messages := []message.Message{{Role: message.RoleUser, Content: "hello"}}
	tools := []provider.ToolDef{}
	units := RequestUnits(messages, tools)
	if got := EstimateNextRequestTokens(1_000, units+400, units); got != 900 {
		t.Fatalf("estimate = %d, want 900", got)
	}
}

func TestRequestEstimateIgnoresDisplayOnlyFields(t *testing.T) {
	base := []message.Message{{Role: message.RoleAssistant, Content: "answer"}}
	decorated := []message.Message{{
		Role:      message.RoleAssistant,
		Content:   "answer",
		Reasoning: strings.Repeat("hidden reasoning", 100),
		Diff:      strings.Repeat("display diff", 100),
	}}
	if EstimateMessagesTokens(base) != EstimateMessagesTokens(decorated) {
		t.Fatal("display-only fields changed provider input estimate")
	}
}

func messageEventForTest(seq event.Seq, role message.Role, content string) event.Event {
	return event.Event{
		Seq:     seq,
		Kind:    event.KindMessageEnd,
		Session: "session-1",
		Time:    time.Now(),
		Payload: message.Message{Role: role, Content: content},
	}
}
