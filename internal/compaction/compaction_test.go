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

func TestDeriveBudgetForOutput(t *testing.T) {
	if got := DeriveBudgetForOutput(128_000, 2_000); got.ReserveTokens != 4_000 {
		t.Fatalf("adaptive reserve = %d, want 4000", got.ReserveTokens)
	}
	if got := DeriveBudgetForOutput(128_000, 10_000); got.ReserveTokens != 8_192 {
		t.Fatalf("capped adaptive reserve = %d, want 8192", got.ReserveTokens)
	}
	if got := DeriveBudgetForOutput(0, 2_000); !got.Estimated ||
		got.ReserveTokens != UnknownReserveTokens {
		t.Fatalf("unknown budget = %#v", got)
	}
}

func TestPlanAndProjectPreserveLatestTurn(t *testing.T) {
	events := []event.Event{
		messageEventForTest(1, message.RoleUser, "old question"),
		messageEventForTest(2, message.RoleAssistant, "old answer"),
		messageEventForTest(3, message.RoleUser, "current question"),
	}
	plan, ok := BuildPlanForPhase(events, nil, PhasePreTurn)
	if !ok {
		t.Fatal("expected a compaction plan")
	}
	if plan.ThroughSeq != 2 || len(plan.SourceMessages) != 2 {
		t.Fatalf("plan = %#v", plan)
	}
	checkpoint := checkpointForPlan(plan, "old work summarized")
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

	if _, ok := BuildPlanForPhase(events, &checkpoint, PhasePreTurn); ok {
		t.Fatal("unchanged covered prefix must not be summarized again")
	}
}

func TestInvalidCheckpointFallsBackToRawHistory(t *testing.T) {
	events := []event.Event{
		messageEventForTest(1, message.RoleUser, "question"),
		messageEventForTest(2, message.RoleAssistant, "answer"),
	}
	plan, ok := BuildPlanForPhase(events, nil, PhaseStandalone)
	if !ok {
		t.Fatal("expected plan")
	}
	checkpoint := checkpointForPlan(plan, "untrusted summary")
	checkpoint.SourceDigest = "wrong"
	checkpoint.CheckpointID = ComputeCheckpointID(checkpoint)
	projected := Project(events, &checkpoint)
	if len(projected) != 2 || projected[0].Content != "question" {
		t.Fatalf("invalid checkpoint should fall back to raw history: %#v", projected)
	}
}

func TestCheckpointRejectsObsoleteWireFormats(t *testing.T) {
	events := []event.Event{
		messageEventForTest(1, message.RoleUser, "question"),
		messageEventForTest(2, message.RoleAssistant, "answer"),
	}
	plan, ok := BuildPlanForPhase(events, nil, PhaseStandalone)
	if !ok {
		t.Fatal("expected plan")
	}
	base := checkpointForPlan(plan, validSummary)

	for name, mutate := range map[string]func(*Checkpoint){
		"zero schema": func(checkpoint *Checkpoint) {
			checkpoint.SchemaVersion = 0
		},
		"previous schema": func(checkpoint *Checkpoint) {
			checkpoint.SchemaVersion = CheckpointSchemaVersion - 1
		},
		"previous summary format": func(checkpoint *Checkpoint) {
			checkpoint.SummaryFormatVersion = "continuation-v1"
		},
		"missing projection kind": func(checkpoint *Checkpoint) {
			checkpoint.ProjectionKind = ""
		},
		"missing checkpoint id": func(checkpoint *Checkpoint) {
			checkpoint.CheckpointID = ""
		},
	} {
		t.Run(name, func(t *testing.T) {
			checkpoint := base
			mutate(&checkpoint)
			if checkpoint.CheckpointID != "" {
				checkpoint.CheckpointID = ComputeCheckpointID(checkpoint)
			}
			if ValidateCheckpoint(events, checkpoint) {
				t.Fatalf("obsolete checkpoint accepted: %#v", checkpoint)
			}
		})
	}
}

func TestBoundToolResultsDoesNotMutateCanonicalMessages(t *testing.T) {
	content := "BEGIN-" + strings.Repeat("x", 20_000) + "-END"
	original := []message.Message{{
		Role:       message.RoleTool,
		ToolCallID: "call-1",
		Content:    content,
		EventSeq:   42,
	}}
	bounded, rewritten := BoundToolResults(original, 256)
	if rewritten != 1 {
		t.Fatalf("rewritten = %d", rewritten)
	}
	if len(bounded[0].Content) >= len(original[0].Content) {
		t.Fatalf("bounded result was not reduced")
	}
	if got := EstimateTextTokens(bounded[0].Content); got > 256 {
		t.Fatalf("bounded result tokens = %d, want <= 256", got)
	}
	if !strings.Contains(bounded[0].Content, "BEGIN-") ||
		!strings.Contains(bounded[0].Content, "-END") {
		t.Fatalf("bounded result did not preserve head and tail: %q", bounded[0].Content)
	}
	if !strings.Contains(bounded[0].Content, "history_read_tool_result") ||
		!strings.Contains(bounded[0].Content, `"event_seq":42`) {
		t.Fatalf("bounded result does not contain a recovery reference: %q", bounded[0].Content)
	}
	if original[0].Content != content {
		t.Fatal("canonical message was mutated")
	}
}

func TestToolResultPolicyKeepsNewestWorkingResult(t *testing.T) {
	messages := []message.Message{
		{Role: message.RoleTool, ToolCallID: "old", Content: strings.Repeat("old ", 2_000)},
		{Role: message.RoleTool, ToolCallID: "new", Content: strings.Repeat("new ", 2_000)},
	}
	projected, rewritten := BoundToolResultsWithPolicy(messages, ToolResultPolicy{
		MaxTokens:  128,
		KeepNewest: 1,
	})
	if rewritten != 1 {
		t.Fatalf("rewritten = %d, want 1", rewritten)
	}
	if projected[1].Content != messages[1].Content {
		t.Fatal("newest tool result was not preserved")
	}
	if projected[0].Content == messages[0].Content {
		t.Fatal("older tool result was not projected")
	}
}

func TestRollingPlanMeasuresCurrentProjection(t *testing.T) {
	events := []event.Event{
		messageEventForTest(1, message.RoleUser, strings.Repeat("old question ", 500)),
		messageEventForTest(2, message.RoleAssistant, strings.Repeat("old answer ", 500)),
		messageEventForTest(3, message.RoleUser, "follow-up"),
		messageEventForTest(4, message.RoleAssistant, strings.Repeat("new evidence ", 50)),
		messageEventForTest(5, message.RoleUser, "current"),
	}
	first, ok := BuildPlanForPhase(events[:3], nil, PhasePreTurn)
	if !ok {
		t.Fatal("expected initial plan")
	}
	checkpoint := checkpointForPlan(first, "short prior checkpoint")
	rolling, ok := BuildPlanForPhase(events, &checkpoint, PhasePreTurn)
	if !ok {
		t.Fatal("expected rolling plan")
	}
	want := EstimateMessagesTokens([]message.Message{
		CheckpointMessage(checkpoint.Summary),
		{Role: message.RoleUser, Content: "follow-up"},
		{Role: message.RoleAssistant, Content: strings.Repeat("new evidence ", 50)},
	})
	if rolling.EstimatedTokens != want {
		t.Fatalf("effective tokens = %d, want %d", rolling.EstimatedTokens, want)
	}
	if rolling.EstimatedRawTokens <= rolling.EstimatedTokens {
		t.Fatalf(
			"raw estimate %d should exceed effective estimate %d",
			rolling.EstimatedRawTokens,
			rolling.EstimatedTokens,
		)
	}
}

func TestMidTurnPlanPreservesUserAnchorAndToolPairs(t *testing.T) {
	events := []event.Event{
		messageEventForTest(1, message.RoleUser, "old question"),
		messageEventForTest(2, message.RoleAssistant, "old answer"),
		messageEventForTest(3, message.RoleUser, "current request"),
		messageValueEventForTest(4, message.Message{
			Role: message.RoleAssistant,
			ToolCalls: []message.ToolCall{{
				ID: "call-1", Name: "read",
			}},
		}),
		messageValueEventForTest(5, message.Message{
			Role: message.RoleTool, ToolCallID: "call-1", Content: "first result",
		}),
		messageValueEventForTest(6, message.Message{
			Role: message.RoleAssistant,
			ToolCalls: []message.ToolCall{{
				ID: "call-2", Name: "test",
			}},
		}),
		messageValueEventForTest(7, message.Message{
			Role: message.RoleTool, ToolCallID: "call-2", Content: "second result",
		}),
		messageEventForTest(8, message.RoleAssistant, "latest working note"),
	}
	plan, ok := BuildPlanForPhase(events, nil, PhaseMidTurn)
	if !ok {
		t.Fatal("expected mid-turn plan")
	}
	if plan.ThroughSeq != 7 || plan.HeadAnchorSeq != 3 {
		t.Fatalf("plan boundary = through %d anchor %d", plan.ThroughSeq, plan.HeadAnchorSeq)
	}
	checkpoint := checkpointForPlan(plan, "completed work summarized")
	projected := Project(events, &checkpoint)
	if len(projected) != 3 {
		t.Fatalf("projected messages = %#v", projected)
	}
	if projected[1].Role != message.RoleUser ||
		projected[1].Content != "current request" {
		t.Fatalf("head anchor = %#v", projected[1])
	}
	if projected[2].Content != "latest working note" {
		t.Fatalf("tail = %#v", projected[2])
	}
}

func TestMidTurnPlanRejectsIncompleteToolPair(t *testing.T) {
	events := []event.Event{
		messageEventForTest(1, message.RoleUser, "current request"),
		messageValueEventForTest(2, message.Message{
			Role: message.RoleAssistant,
			ToolCalls: []message.ToolCall{{
				ID: "call-1", Name: "read",
			}},
		}),
		messageEventForTest(3, message.RoleAssistant, "later note"),
	}
	if _, ok := BuildPlanForPhase(events, nil, PhaseMidTurn); ok {
		t.Fatal("incomplete tool pair must not be compacted")
	}
}

func TestAutoPlanPrefersFurtherMidTurnCoverage(t *testing.T) {
	events := []event.Event{
		messageEventForTest(1, message.RoleUser, "old question"),
		messageEventForTest(2, message.RoleAssistant, "old answer"),
		messageEventForTest(3, message.RoleUser, "current request"),
		messageEventForTest(4, message.RoleAssistant, "completed step one"),
		messageEventForTest(5, message.RoleAssistant, "completed step two"),
		messageEventForTest(6, message.RoleAssistant, "latest working note"),
	}
	plan, ok := BuildPlanForPhase(events, nil, PhaseAuto)
	if !ok {
		t.Fatal("expected automatic plan")
	}
	if plan.Phase != PhaseMidTurn || plan.ThroughSeq != 5 {
		t.Fatalf("automatic plan = %#v", plan)
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

func checkpointForPlan(plan Plan, summary string) Checkpoint {
	checkpoint := Checkpoint{
		SchemaVersion:        CheckpointSchemaVersion,
		SourcePolicyVersion:  SourcePolicyVersion,
		SummaryFormatVersion: SummaryFormatVersion,
		PromptVersion:        PromptVersion,
		SessionID:            plan.SessionID,
		Phase:                plan.Phase,
		HeadAnchorSeq:        plan.HeadAnchorSeq,
		ThroughSeq:           plan.ThroughSeq,
		SourceDigest:         plan.SourceDigest,
		ProjectionKind:       ProjectionText,
		Level:                CheckpointLevelSegmented,
		Segments:             ConsolidatedSegment(plan, summary),
		Summary:              summary,
		Model:                "model",
		CreatedAt:            time.Now(),
	}
	checkpoint.CheckpointID = ComputeCheckpointID(checkpoint)
	return checkpoint
}

func messageEventForTest(seq event.Seq, role message.Role, content string) event.Event {
	return messageValueEventForTest(seq, message.Message{Role: role, Content: content})
}

func messageValueEventForTest(seq event.Seq, value message.Message) event.Event {
	return event.Event{
		Seq:     seq,
		Kind:    event.KindMessageEnd,
		Session: "session-1",
		Time:    time.Now(),
		Payload: value,
	}
}
