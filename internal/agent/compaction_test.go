package agent

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/freesoulcode/foya/internal/approval"
	"github.com/freesoulcode/foya/internal/broker"
	"github.com/freesoulcode/foya/internal/compaction"
	"github.com/freesoulcode/foya/internal/event"
	"github.com/freesoulcode/foya/internal/message"
	"github.com/freesoulcode/foya/internal/provider"
	"github.com/freesoulcode/foya/internal/session"
	"github.com/freesoulcode/foya/internal/state"
	"github.com/freesoulcode/foya/internal/tool"
)

type compactionProvider struct {
	mu            sync.Mutex
	contextWindow int64
	overflowFirst bool
	streamCalls   int
	captured      [][]provider.InputMessage
	efforts       []string
	outputLimits  []int64
}

func (p *compactionProvider) Name() string { return "test" }

func (p *compactionProvider) ListModels(context.Context) ([]provider.ModelInfo, error) {
	return []provider.ModelInfo{{ID: "test-model", ContextWindow: p.contextWindow}}, nil
}

func (p *compactionProvider) Complete(
	context.Context,
	provider.Request,
) (string, error) {
	return `## Goal
Continue the requested work.
## Progress
Earlier investigation completed.
## Key Decisions
Keep canonical history unchanged.
## Next Steps
Continue from the latest user request.
## Critical Context
Use the current project and tools.`, nil
}

func (p *compactionProvider) Stream(
	_ context.Context,
	req provider.Request,
) (<-chan provider.StreamEvent, error) {
	p.mu.Lock()
	p.streamCalls++
	call := p.streamCalls
	p.captured = append(p.captured, append([]provider.InputMessage(nil), req.Messages...))
	p.efforts = append(p.efforts, req.ReasoningEffort)
	p.outputLimits = append(p.outputLimits, req.MaxOutputTokens)
	p.mu.Unlock()

	ch := make(chan provider.StreamEvent, 2)
	if p.overflowFirst && call == 1 {
		ch <- provider.StreamEvent{Type: "error", Text: "maximum context length exceeded"}
		close(ch)
		return ch, nil
	}
	ch <- provider.StreamEvent{
		Type: "usage",
		Usage: &provider.Usage{
			Model:       "test-model",
			InputTokens: 100,
			TotalTokens: 110,
		},
	}
	ch <- provider.StreamEvent{Type: "done", FinishReason: "stop"}
	close(ch)
	return ch, nil
}

func TestRunTurnCompactsBeforeOversizedRequest(t *testing.T) {
	engine, log, sessionID, prov := newCompactionTestEngine(t, 4_096)
	appendMessage(t, log, sessionID, message.RoleUser, "old question")
	appendMessage(t, log, sessionID, message.RoleAssistant, strings.Repeat("old detail ", 2_000))

	if err := engine.RunTurn(context.Background(), sessionID, "continue"); err != nil {
		t.Fatal(err)
	}
	if _, ok := log.Checkpoint(sessionID); !ok {
		t.Fatal("expected an automatic checkpoint")
	}
	prov.mu.Lock()
	defer prov.mu.Unlock()
	if len(prov.captured) != 1 {
		t.Fatalf("stream calls = %d", len(prov.captured))
	}
	wire := messagesText(prov.captured[0])
	if !strings.Contains(wire, "<context_checkpoint>") {
		t.Fatalf("request did not use checkpoint: %s", wire)
	}
	if strings.Contains(wire, "old detail old detail") {
		t.Fatal("covered raw history leaked into compacted request")
	}
	if !strings.Contains(wire, "continue") {
		t.Fatal("latest user turn was not preserved")
	}
}

func TestRunTurnUsesConfiguredInputLimitForCompaction(t *testing.T) {
	engine, log, sessionID, _ := newCompactionTestEngine(t, 100_000)
	engine.SetModelTokenLimitsResolver(func(string, string) *ModelTokenLimits {
		return &ModelTokenLimits{MaxInputTokens: 4_096}
	})
	appendMessage(t, log, sessionID, message.RoleUser, "old question")
	appendMessage(t, log, sessionID, message.RoleAssistant, strings.Repeat("old detail ", 2_000))

	if err := engine.RunTurn(context.Background(), sessionID, "continue"); err != nil {
		t.Fatal(err)
	}
	if _, ok := log.Checkpoint(sessionID); !ok {
		t.Fatal("expected configured max input tokens to trigger compaction")
	}
}

func TestConfiguredInputLimitDoesNotExpandContextBudget(t *testing.T) {
	limit := inputTokenLimit(
		compaction.DeriveBudget(131_072),
		ModelTokenLimits{
			MaxInputTokens:  130_048,
			MaxOutputTokens: 16_384,
		},
	)
	if limit != 114_688 {
		t.Fatalf("input limit = %d, want 114688", limit)
	}
}

func TestRunTurnForwardsSessionReasoningEffort(t *testing.T) {
	sessions := session.NewMemManager()
	sess, err := sessions.Create(session.CreateOptions{
		Model:           "test-model",
		ReasoningEffort: session.ReasoningEffortHigh,
	})
	if err != nil {
		t.Fatal(err)
	}
	log := state.NewMemLog()
	bus := broker.New[event.Event]()
	gateway := approval.NewGateway(bus, log)
	prov := &compactionProvider{contextWindow: 100_000}
	engine := NewEngine(log, bus, sessions, prov, "test-model", tool.NewRegistry(), gateway)

	if err := engine.RunTurn(context.Background(), sess.ID, "continue"); err != nil {
		t.Fatal(err)
	}
	prov.mu.Lock()
	defer prov.mu.Unlock()
	if len(prov.efforts) != 1 || prov.efforts[0] != "high" {
		t.Fatalf("reasoning efforts = %#v, want one high value", prov.efforts)
	}
}

func TestRunTurnForwardsConfiguredOutputLimit(t *testing.T) {
	engine, _, sessionID, prov := newCompactionTestEngine(t, 100_000)
	engine.SetModelTokenLimitsResolver(func(string, string) *ModelTokenLimits {
		return &ModelTokenLimits{MaxOutputTokens: 1_024}
	})

	if err := engine.RunTurn(context.Background(), sessionID, "continue"); err != nil {
		t.Fatal(err)
	}
	prov.mu.Lock()
	defer prov.mu.Unlock()
	if len(prov.outputLimits) != 1 || prov.outputLimits[0] != 1_024 {
		t.Fatalf("output limits = %#v, want one 1024 value", prov.outputLimits)
	}
}

func TestRunTurnCompactsAndRetriesOneProviderOverflow(t *testing.T) {
	engine, log, sessionID, prov := newCompactionTestEngine(t, 100_000)
	prov.overflowFirst = true
	appendMessage(t, log, sessionID, message.RoleUser, "old question")
	appendMessage(t, log, sessionID, message.RoleAssistant, strings.Repeat("old answer ", 500))

	if err := engine.RunTurn(context.Background(), sessionID, "continue"); err != nil {
		t.Fatal(err)
	}
	prov.mu.Lock()
	defer prov.mu.Unlock()
	if prov.streamCalls != 2 {
		t.Fatalf("stream calls = %d, want 2", prov.streamCalls)
	}
	if !strings.Contains(messagesText(prov.captured[1]), "<context_checkpoint>") {
		t.Fatal("overflow retry did not use compacted history")
	}
}

func TestCompactSessionCreatesStandaloneCheckpoint(t *testing.T) {
	engine, log, sessionID, _ := newCompactionTestEngine(t, 100_000)
	appendMessage(t, log, sessionID, message.RoleUser, "completed question")
	appendMessage(t, log, sessionID, message.RoleAssistant, strings.Repeat("completed answer ", 200))

	checkpoint, err := engine.CompactSession(context.Background(), sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if checkpoint.ThroughSeq == 0 {
		t.Fatal("checkpoint has no coverage boundary")
	}
	projected, err := log.ModelHistory(context.Background(), sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(projected) != 1 || projected[0].Role != message.RoleSystem {
		t.Fatalf("projected history = %#v", projected)
	}
}

func TestContextOverflowClassification(t *testing.T) {
	for _, text := range []string{
		"maximum context length exceeded",
		"prompt is too long",
		"request_too_large",
	} {
		if !isContextOverflow(text) {
			t.Fatalf("expected context overflow for %q", text)
		}
	}
	if isContextOverflow("rate limit exceeded for input tokens") {
		t.Fatal("rate limit must not be classified as context overflow")
	}
}

func newCompactionTestEngine(
	t *testing.T,
	contextWindow int64,
) (*Engine, *state.MemLog, string, *compactionProvider) {
	t.Helper()
	sessions := session.NewMemManager()
	sess, err := sessions.Create(session.CreateOptions{Model: "test-model"})
	if err != nil {
		t.Fatal(err)
	}
	log := state.NewMemLog()
	bus := broker.New[event.Event]()
	gateway := approval.NewGateway(bus, log)
	prov := &compactionProvider{contextWindow: contextWindow}
	engine := NewEngine(
		log,
		bus,
		sessions,
		prov,
		"test-model",
		tool.NewRegistry(),
		gateway,
	)
	return engine, log, sess.ID, prov
}

func appendMessage(
	t *testing.T,
	log *state.MemLog,
	sessionID string,
	role message.Role,
	content string,
) {
	t.Helper()
	_, err := log.Append(context.Background(), event.Event{
		Kind:    event.KindMessageEnd,
		Session: sessionID,
		Payload: message.Message{Role: role, Content: content},
	})
	if err != nil {
		t.Fatal(err)
	}
}

func messagesText(messages []provider.InputMessage) string {
	var parts []string
	for _, msg := range messages {
		for _, part := range msg.Parts {
			if part.Type == "text" {
				parts = append(parts, part.Text)
			}
		}
	}
	return strings.Join(parts, "\n")
}
