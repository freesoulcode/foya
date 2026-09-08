package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/freesoulcode/foya/internal/broker"
	conversation "github.com/freesoulcode/foya/internal/conversation"
	interaction "github.com/freesoulcode/foya/internal/interaction"

	modelapi "github.com/freesoulcode/foya/internal/model"

	"github.com/freesoulcode/foya/internal/tool"
)

type compactionProvider struct {
	mu             sync.Mutex
	contextWindow  int64
	overflowFirst  bool
	streamCalls    int
	captured       [][]modelapi.InputMessage
	efforts        []string
	outputLimits   []int64
	completeCalls  int
	completeLimits []int64
	completeInputs [][]modelapi.InputMessage
	completions    []modelapi.Completion
	completionErrs []error
}

type nativeCompactionProvider struct {
	state          modelapi.ContextState
	previousStates []*modelapi.ContextState
}

type unsupportedNativeProvider struct {
	*compactionProvider
}

func (*unsupportedNativeProvider) CompactContext(
	context.Context,
	modelapi.Request,
) (modelapi.NativeCompactionResult, error) {
	return modelapi.NativeCompactionResult{}, modelapi.ErrNativeCompactionUnsupported
}

func (p *nativeCompactionProvider) Name() string { return "native-test" }
func (p *nativeCompactionProvider) Stream(
	context.Context,
	modelapi.Request,
) (<-chan modelapi.StreamEvent, error) {
	ch := make(chan modelapi.StreamEvent)
	close(ch)
	return ch, nil
}
func (p *nativeCompactionProvider) CompactContext(
	_ context.Context,
	request modelapi.Request,
) (modelapi.NativeCompactionResult, error) {
	p.previousStates = append(p.previousStates, request.ContextState)
	return modelapi.NativeCompactionResult{State: p.state}, nil
}

func (p *compactionProvider) Name() string { return "test" }

func (p *compactionProvider) ListModels(context.Context) ([]modelapi.ModelInfo, error) {
	return []modelapi.ModelInfo{{ID: "test-model", ContextWindow: p.contextWindow}}, nil
}

func (p *compactionProvider) Complete(
	context.Context,
	modelapi.Request,
) (string, error) {
	return defaultCompactionSummary, nil
}

func (p *compactionProvider) CompleteDetailed(
	_ context.Context,
	req modelapi.Request,
) (modelapi.Completion, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.completeCalls++
	p.completeLimits = append(p.completeLimits, req.MaxOutputTokens)
	p.completeInputs = append(
		p.completeInputs,
		append([]modelapi.InputMessage(nil), req.Messages...),
	)
	if len(p.completionErrs) > 0 {
		err := p.completionErrs[0]
		p.completionErrs = p.completionErrs[1:]
		if err != nil {
			return modelapi.Completion{}, err
		}
	}
	if len(p.completions) > 0 {
		result := p.completions[0]
		p.completions = p.completions[1:]
		return result, nil
	}
	return modelapi.Completion{
		Text:         defaultCompactionSummary,
		FinishReason: "stop",
	}, nil
}

const defaultCompactionSummary = `## Goal
Continue the requested work.
## Progress
Earlier investigation completed.
## Key Decisions
Keep canonical history unchanged.
## Next Steps
Continue from the latest user request.
## Critical Context
Use the current project and tools.`

func (p *compactionProvider) Stream(
	_ context.Context,
	req modelapi.Request,
) (<-chan modelapi.StreamEvent, error) {
	p.mu.Lock()
	p.streamCalls++
	call := p.streamCalls
	p.captured = append(p.captured, append([]modelapi.InputMessage(nil), req.Messages...))
	p.efforts = append(p.efforts, req.ReasoningEffort)
	p.outputLimits = append(p.outputLimits, req.MaxOutputTokens)
	p.mu.Unlock()

	ch := make(chan modelapi.StreamEvent, 2)
	if p.overflowFirst && call == 1 {
		ch <- modelapi.StreamEvent{Type: "error", Text: "maximum context length exceeded"}
		close(ch)
		return ch, nil
	}
	ch <- modelapi.StreamEvent{
		Type: "usage",
		Usage: &modelapi.Usage{
			Model:       "test-model",
			InputTokens: 100,
			TotalTokens: 110,
		},
	}
	ch <- modelapi.StreamEvent{Type: "done", FinishReason: "stop"}
	close(ch)
	return ch, nil
}

func TestRunTurnCompactsBeforeOversizedRequest(t *testing.T) {
	engine, log, sessionID, prov := newCompactionTestEngine(t, 4_096)
	appendMessage(t, log, sessionID, conversation.RoleUser, "old question")
	appendMessage(t, log, sessionID, conversation.RoleAssistant, strings.Repeat("old detail ", 2_000))

	if err := engine.RunTurn(context.Background(), sessionID, "continue"); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := log.Checkpoint(context.Background(), sessionID); err != nil || !ok {
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
	appendMessage(t, log, sessionID, conversation.RoleUser, "old question")
	appendMessage(t, log, sessionID, conversation.RoleAssistant, strings.Repeat("old detail ", 2_000))

	if err := engine.RunTurn(context.Background(), sessionID, "continue"); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := log.Checkpoint(context.Background(), sessionID); err != nil || !ok {
		t.Fatal("expected configured max input tokens to trigger compaction")
	}
}

func TestEstimatedBudgetDoesNotRejectProviderRequest(t *testing.T) {
	engine, _, sessionID, prov := newCompactionTestEngine(t, 64)

	if err := engine.RunTurn(
		context.Background(),
		sessionID,
		strings.Repeat("large current request ", 500),
	); err != nil {
		t.Fatal(err)
	}
	prov.mu.Lock()
	defer prov.mu.Unlock()
	if prov.streamCalls != 1 {
		t.Fatalf("stream calls = %d, want 1", prov.streamCalls)
	}
}

func TestRunTurnPersistsAcceptedProviderBoundary(t *testing.T) {
	engine, log, sessionID, _ := newCompactionTestEngine(t, 100_000)
	if err := engine.RunTurn(context.Background(), sessionID, "continue"); err != nil {
		t.Fatal(err)
	}
	route := engine.modelRouteKey(sessionID, "test-model")
	boundary, ok, err := log.AcceptedBoundary(
		context.Background(),
		sessionID,
		route,
	)
	if err != nil || !ok {
		t.Fatalf("accepted boundary: ok=%v err=%v", ok, err)
	}
	if boundary.ThroughSeq == 0 || boundary.Route != route {
		t.Fatalf("accepted boundary = %#v", boundary)
	}
	if boundary.InputTokens != 100 || boundary.PayloadUnits == 0 {
		t.Fatalf("accepted boundary usage = %#v", boundary)
	}
}

func TestPrepareModelRequestCompactsCompletedMidTurnSteps(t *testing.T) {
	engine, log, sessionID, _ := newCompactionTestEngine(t, 512)
	appendMessage(t, log, sessionID, conversation.RoleUser, "current request")
	appendMessageValue(t, log, sessionID, conversation.Message{
		Role: conversation.RoleAssistant,
		ToolCalls: []conversation.ToolCall{{
			ID: "call-1", Name: "read",
		}},
	})
	appendMessageValue(t, log, sessionID, conversation.Message{
		Role:       conversation.RoleTool,
		ToolCallID: "call-1",
		Content:    strings.Repeat("first evidence ", 1_000),
	})
	appendMessageValue(t, log, sessionID, conversation.Message{
		Role: conversation.RoleAssistant,
		ToolCalls: []conversation.ToolCall{{
			ID: "call-2", Name: "test",
		}},
	})
	appendMessageValue(t, log, sessionID, conversation.Message{
		Role:       conversation.RoleTool,
		ToolCallID: "call-2",
		Content:    strings.Repeat("second evidence ", 1_000),
	})
	appendMessage(t, log, sessionID, conversation.RoleAssistant, "latest working note")

	messages, _, _, _, _, err := engine.prepareModelRequest(
		context.Background(),
		sessionID,
		"test-model",
		"system",
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	checkpoint, ok, err := log.Checkpoint(context.Background(), sessionID)
	if err != nil || !ok {
		t.Fatalf("checkpoint: ok=%v err=%v", ok, err)
	}
	if checkpoint.Phase != conversation.PhaseMidTurn || checkpoint.HeadAnchorSeq == 0 {
		t.Fatalf("checkpoint = %#v", checkpoint)
	}
	text := messagesText(modelMessages(messages))
	if !strings.Contains(text, "<context_checkpoint>") ||
		!strings.Contains(text, "current request") ||
		!strings.Contains(text, "latest working note") {
		t.Fatalf("mid-turn projection = %s", text)
	}
	if strings.Contains(text, "first evidence first evidence") {
		t.Fatal("covered mid-turn tool result leaked into projection")
	}
}

func TestPrepareModelRequestExposesHistoryReaderOnlyForReferences(t *testing.T) {
	engine, log, sessionID, _ := newCompactionTestEngine(t, 128)
	engine.tools.Register(tool.NewHistoryReadToolResult(log))
	appendMessage(t, log, sessionID, conversation.RoleUser, "current request")
	appendMessageValue(t, log, sessionID, conversation.Message{
		Role: conversation.RoleAssistant,
		ToolCalls: []conversation.ToolCall{{
			ID: "call-1", Name: "bash",
		}},
	})
	appendMessageValue(t, log, sessionID, conversation.Message{
		Role:       conversation.RoleTool,
		ToolCallID: "call-1",
		Content:    strings.Repeat("large output ", 2_000),
	})

	messages, toolDefs, _, _, _, err := engine.prepareModelRequest(
		context.Background(),
		sessionID,
		"test-model",
		"system",
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !messagesContainToolResultReference(messages) {
		t.Fatal("bounded history did not contain a tool-result reference")
	}
	if !activeToolDefNames(toolDefs)[tool.HistoryReadToolResultName] {
		t.Fatal("history reader was not exposed for a referenced tool result")
	}
}

func TestConfiguredInputLimitDoesNotExpandContextBudget(t *testing.T) {
	compiled := (conversation.ContextCompiler{}).Compile(conversation.CompileInput{
		ContextWindow:  131_072,
		MaxInputTokens: 130_048,
	})
	if compiled.InputLimit != 114_688 {
		t.Fatalf("input limit = %d, want 114688", compiled.InputLimit)
	}
}

func TestRunTurnForwardsSessionReasoningEffort(t *testing.T) {
	sessions := newTestSessionManager(t)
	sess, err := sessions.Create(conversation.CreateOptions{
		Model:           "test-model",
		ReasoningEffort: conversation.ReasoningEffortHigh,
	})
	if err != nil {
		t.Fatal(err)
	}
	log := newTestStore(t)
	bus := broker.New[conversation.Event]()
	gateway := interaction.NewGateway(bus, log)
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
	appendMessage(t, log, sessionID, conversation.RoleUser, "old question")
	appendMessage(t, log, sessionID, conversation.RoleAssistant, strings.Repeat("old answer ", 500))

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
	appendMessage(t, log, sessionID, conversation.RoleUser, "completed question")
	appendMessage(t, log, sessionID, conversation.RoleAssistant, strings.Repeat("completed answer ", 200))

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
	if len(projected) != 1 || projected[0].Role != conversation.RoleSystem {
		t.Fatalf("projected history = %#v", projected)
	}
	if checkpoint.Level != conversation.CheckpointLevelSegmented ||
		len(checkpoint.Segments) != 1 {
		t.Fatalf("checkpoint hierarchy = %#v", checkpoint)
	}
}

func TestCompactSessionAppendsSummarySegment(t *testing.T) {
	engine, log, sessionID, _ := newCompactionTestEngine(t, 100_000)
	appendMessage(t, log, sessionID, conversation.RoleUser, "first question")
	appendMessage(
		t,
		log,
		sessionID,
		conversation.RoleAssistant,
		strings.Repeat("first answer ", 200),
	)
	first, err := engine.CompactSession(context.Background(), sessionID)
	if err != nil {
		t.Fatal(err)
	}
	appendMessage(t, log, sessionID, conversation.RoleUser, "second question")
	appendMessage(
		t,
		log,
		sessionID,
		conversation.RoleAssistant,
		strings.Repeat("second answer ", 200),
	)
	second, err := engine.CompactSession(context.Background(), sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if second.PreviousCheckpointID != first.CheckpointID ||
		len(second.Segments) != 2 ||
		second.Level != conversation.CheckpointLevelSegmented {
		t.Fatalf("rolling checkpoint = %#v", second)
	}
	if !strings.Contains(second.Summary, "<checkpoint_segments>") {
		t.Fatalf("segmented summary = %q", second.Summary)
	}
}

func TestCompactSessionConsolidatesBoundedSegments(t *testing.T) {
	engine, log, sessionID, _ := newCompactionTestEngine(t, 100_000)
	var checkpoint *conversation.Checkpoint
	for i := 0; i < conversation.MaxCheckpointSegments+1; i++ {
		appendMessage(
			t,
			log,
			sessionID,
			conversation.RoleUser,
			fmt.Sprintf("question %d", i),
		)
		appendMessage(
			t,
			log,
			sessionID,
			conversation.RoleAssistant,
			strings.Repeat(fmt.Sprintf("answer %d ", i), 200),
		)
		var err error
		checkpoint, err = engine.CompactSession(context.Background(), sessionID)
		if err != nil {
			t.Fatal(err)
		}
	}
	if checkpoint.Level != conversation.CheckpointLevelSession ||
		len(checkpoint.Segments) != 1 {
		t.Fatalf("consolidated checkpoint = %#v", checkpoint)
	}
}

func TestCompactionUsesDedicatedOutputLimitAndRepairsLength(t *testing.T) {
	engine, log, sessionID, prov := newCompactionTestEngine(t, 100_000)
	engine.SetModelTokenLimitsResolver(func(string, string) *ModelTokenLimits {
		return &ModelTokenLimits{MaxOutputTokens: 32_000}
	})
	prov.completions = []modelapi.Completion{
		{Text: "truncated", FinishReason: "length"},
		{Text: defaultCompactionSummary, FinishReason: "stop"},
	}
	appendMessage(t, log, sessionID, conversation.RoleUser, "completed question")
	appendMessage(
		t,
		log,
		sessionID,
		conversation.RoleAssistant,
		strings.Repeat("completed answer ", 200),
	)

	if _, err := engine.CompactSession(context.Background(), sessionID); err != nil {
		t.Fatal(err)
	}
	prov.mu.Lock()
	defer prov.mu.Unlock()
	if prov.completeCalls != 2 {
		t.Fatalf("completion calls = %d, want 2", prov.completeCalls)
	}
	for _, limit := range prov.completeLimits {
		if limit != conversation.MaxOutputTokens {
			t.Fatalf("compaction output limit = %d", limit)
		}
	}
}

func TestCompactionRetreatsToAcceptedProviderBoundary(t *testing.T) {
	engine, log, sessionID, prov := newCompactionTestEngine(t, 100_000)
	firstSeq := appendMessage(
		t,
		log,
		sessionID,
		conversation.RoleUser,
		strings.Repeat("old question ", 200),
	)
	acceptedSeq := appendMessage(
		t,
		log,
		sessionID,
		conversation.RoleAssistant,
		strings.Repeat("old answer ", 200),
	)
	if firstSeq == 0 {
		t.Fatal("first event sequence was not assigned")
	}
	appendMessage(t, log, sessionID, conversation.RoleUser, "current request")
	appendMessage(t, log, sessionID, conversation.RoleAssistant, "completed step one")
	appendMessage(t, log, sessionID, conversation.RoleAssistant, "completed step two")
	appendMessage(t, log, sessionID, conversation.RoleAssistant, "latest working note")

	route := engine.modelRouteKey(sessionID, "test-model")
	boundary := conversation.AcceptedBoundary{
		SessionID: sessionID,
		Route:     route, ThroughSeq: acceptedSeq,
		CreatedAt: time.Now(),
	}
	if _, err := log.Append(context.Background(), conversation.Event{
		Kind: conversation.KindContextRequestAccepted, Session: sessionID,
		Payload: boundary, Time: boundary.CreatedAt,
	}); err != nil {
		t.Fatal(err)
	}
	prov.completionErrs = []error{
		fmt.Errorf("maximum context length exceeded"),
		nil,
	}

	checkpoint, err := engine.compactHistory(
		context.Background(),
		sessionID,
		"test-model",
		conversation.PhaseAuto,
		"test",
	)
	if err != nil {
		t.Fatal(err)
	}
	if checkpoint.ThroughSeq != acceptedSeq {
		t.Fatalf(
			"checkpoint through = %d, want accepted boundary %d",
			checkpoint.ThroughSeq,
			acceptedSeq,
		)
	}
	prov.mu.Lock()
	defer prov.mu.Unlock()
	if prov.completeCalls != 2 {
		t.Fatalf("completion calls = %d, want 2", prov.completeCalls)
	}
}

func TestCompactionSuppressesRepeatedDeterministicFailure(t *testing.T) {
	engine, log, sessionID, prov := newCompactionTestEngine(t, 100_000)
	appendMessage(t, log, sessionID, conversation.RoleUser, "completed question")
	appendMessage(
		t,
		log,
		sessionID,
		conversation.RoleAssistant,
		strings.Repeat("completed answer ", 200),
	)
	prov.completions = []modelapi.Completion{
		{Text: "invalid", FinishReason: "stop"},
		{Text: "still invalid", FinishReason: "stop"},
	}

	_, firstErr := engine.compactHistory(
		context.Background(),
		sessionID,
		"test-model",
		conversation.PhaseStandalone,
		"test",
	)
	if !errors.Is(firstErr, errCompactionInvalidSummary) {
		t.Fatalf("first error = %v", firstErr)
	}
	_, secondErr := engine.compactHistory(
		context.Background(),
		sessionID,
		"test-model",
		conversation.PhaseStandalone,
		"test",
	)
	if !errors.Is(secondErr, errCompactionSuppressed) {
		t.Fatalf("second error = %v", secondErr)
	}
	prov.mu.Lock()
	defer prov.mu.Unlock()
	if prov.completeCalls != 2 {
		t.Fatalf("completion calls = %d, want 2", prov.completeCalls)
	}
}

func TestCompactionFailureCircuitIsScopedToProviderRoute(t *testing.T) {
	engine, log, sessionID, prov := newCompactionTestEngine(t, 100_000)
	route := "route-a"
	engine.SetModelRouteResolver(func(string, string) string { return route })
	appendMessage(t, log, sessionID, conversation.RoleUser, "completed question")
	appendMessage(
		t,
		log,
		sessionID,
		conversation.RoleAssistant,
		strings.Repeat("completed answer ", 200),
	)
	prov.completions = []modelapi.Completion{
		{Text: "invalid", FinishReason: "stop"},
		{Text: "still invalid", FinishReason: "stop"},
		{Text: defaultCompactionSummary, FinishReason: "stop"},
	}

	if _, err := engine.compactHistory(
		context.Background(),
		sessionID,
		"test-model",
		conversation.PhaseStandalone,
		"test",
	); !errors.Is(err, errCompactionInvalidSummary) {
		t.Fatalf("first error = %v", err)
	}
	route = "route-b"
	if _, err := engine.compactHistory(
		context.Background(),
		sessionID,
		"test-model",
		conversation.PhaseStandalone,
		"test",
	); err != nil {
		t.Fatal(err)
	}
	prov.mu.Lock()
	defer prov.mu.Unlock()
	if prov.completeCalls != 3 {
		t.Fatalf("completion calls = %d, want 3", prov.completeCalls)
	}
}

func TestProviderNativeCheckpointReplaysOnlyOnMatchingRoute(t *testing.T) {
	sessions := newTestSessionManager(t)
	sess, err := sessions.Create(conversation.CreateOptions{Model: "native-model"})
	if err != nil {
		t.Fatal(err)
	}
	log := newTestStore(t)
	bus := broker.New[conversation.Event]()
	gateway := interaction.NewGateway(bus, log)
	prov := &nativeCompactionProvider{state: modelapi.ContextState{
		Kind: "native.compaction",
		Data: []byte(`{"opaque":"state"}`),
	}}
	engine := NewEngine(
		log,
		bus,
		sessions,
		prov,
		"native-model",
		tool.NewRegistry(),
		gateway,
	)
	appendMessage(t, log, sess.ID, conversation.RoleUser, "old question")
	appendMessage(
		t,
		log,
		sess.ID,
		conversation.RoleAssistant,
		strings.Repeat("old answer ", 200),
	)
	appendMessage(t, log, sess.ID, conversation.RoleUser, "current request")

	checkpoint, err := engine.compactHistory(
		context.Background(),
		sess.ID,
		"native-model",
		conversation.PhasePreTurn,
		"test",
	)
	if err != nil {
		t.Fatal(err)
	}
	if checkpoint.ProjectionKind != conversation.ProjectionProviderNative ||
		checkpoint.ProviderRoute == "" ||
		len(checkpoint.ProviderState) == 0 {
		t.Fatalf("native checkpoint = %#v", checkpoint)
	}
	messages, _, contextState, _, _, err := engine.prepareModelRequest(
		context.Background(),
		sess.ID,
		"native-model",
		"system",
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if contextState == nil ||
		contextState.Kind != "native.compaction" ||
		string(contextState.Data) != `{"opaque":"state"}` {
		t.Fatalf("context state = %#v", contextState)
	}
	if text := messagesText(modelMessages(messages)); strings.Contains(text, "old answer") {
		t.Fatalf("native-covered history leaked into request: %s", text)
	}
	appendMessage(
		t,
		log,
		sess.ID,
		conversation.RoleAssistant,
		strings.Repeat("native continuation ", 200),
	)
	checkpoint, err = engine.CompactSession(context.Background(), sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(prov.previousStates) != 2 ||
		prov.previousStates[1] == nil ||
		prov.previousStates[1].Kind != "native.compaction" {
		t.Fatalf("native rolling states = %#v", prov.previousStates)
	}

	raw := conversation.ProjectForRoute(
		mustEvents(t, log, sess.ID),
		checkpoint,
		"different-route",
	)
	if raw.ContextState != nil || len(raw.Messages) != 4 {
		t.Fatalf("route mismatch projection = %#v", raw)
	}

	textProvider := &compactionProvider{contextWindow: 100_000}
	engine.SwitchProvider(textProvider, "native-model")
	appendMessage(
		t,
		log,
		sess.ID,
		conversation.RoleAssistant,
		strings.Repeat("completed after switch ", 200),
	)
	portable, err := engine.CompactSession(context.Background(), sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if portable.ProjectionKind != conversation.ProjectionText {
		t.Fatalf("portable checkpoint kind = %q", portable.ProjectionKind)
	}
	textProvider.mu.Lock()
	defer textProvider.mu.Unlock()
	if len(textProvider.completeInputs) == 0 ||
		!strings.Contains(
			messagesText(textProvider.completeInputs[0]),
			"old answer old answer",
		) {
		t.Fatal("native route fallback did not rebuild from canonical history")
	}
}

func TestUnsupportedNativeCompactionFallsBackToText(t *testing.T) {
	sessions := newTestSessionManager(t)
	sess, err := sessions.Create(conversation.CreateOptions{Model: "test-model"})
	if err != nil {
		t.Fatal(err)
	}
	log := newTestStore(t)
	bus := broker.New[conversation.Event]()
	gateway := interaction.NewGateway(bus, log)
	prov := &unsupportedNativeProvider{
		compactionProvider: &compactionProvider{contextWindow: 100_000},
	}
	engine := NewEngine(
		log,
		bus,
		sessions,
		prov,
		"test-model",
		tool.NewRegistry(),
		gateway,
	)
	appendMessage(t, log, sess.ID, conversation.RoleUser, "old question")
	appendMessage(
		t,
		log,
		sess.ID,
		conversation.RoleAssistant,
		strings.Repeat("old answer ", 200),
	)

	checkpoint, err := engine.CompactSession(context.Background(), sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if checkpoint.ProjectionKind != conversation.ProjectionText {
		t.Fatalf("checkpoint kind = %q", checkpoint.ProjectionKind)
	}
}

func TestContextOverflowClassification(t *testing.T) {
	for _, text := range []string{
		"maximum context length exceeded",
		"prompt is too long",
		"request_too_large: prompt exceeds context window",
	} {
		if !isContextOverflow(text) {
			t.Fatalf("expected context overflow for %q", text)
		}
	}
	if isContextOverflow("rate limit exceeded for input tokens") {
		t.Fatal("rate limit must not be classified as context overflow")
	}
	if isContextOverflow("request_too_large") {
		t.Fatal("ambiguous request size errors must not be classified as context overflow")
	}
}

func newCompactionTestEngine(
	t *testing.T,
	contextWindow int64,
) (*Engine, conversation.Store, string, *compactionProvider) {
	t.Helper()
	sessions := newTestSessionManager(t)
	sess, err := sessions.Create(conversation.CreateOptions{Model: "test-model"})
	if err != nil {
		t.Fatal(err)
	}
	log := newTestStore(t)
	bus := broker.New[conversation.Event]()
	gateway := interaction.NewGateway(bus, log)
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
	log conversation.Store,
	sessionID string,
	role conversation.Role,
	content string,
) conversation.Seq {
	return appendMessageValue(
		t,
		log,
		sessionID,
		conversation.Message{Role: role, Content: content},
	)
}

func appendMessageValue(
	t *testing.T,
	log conversation.Store,
	sessionID string,
	value conversation.Message,
) conversation.Seq {
	t.Helper()
	seq, err := log.Append(context.Background(), conversation.Event{
		Kind:    conversation.KindMessageEnd,
		Session: sessionID,
		Payload: value,
	})
	if err != nil {
		t.Fatal(err)
	}
	return seq
}

func messagesText(messages []modelapi.InputMessage) string {
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

func mustEvents(t *testing.T, log conversation.Store, sessionID string) []conversation.Event {
	t.Helper()
	events, err := log.Events(context.Background(), sessionID)
	if err != nil {
		t.Fatal(err)
	}
	return events
}
