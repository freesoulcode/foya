package tool

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/freesoulcode/foya/internal/broker"
	"github.com/freesoulcode/foya/internal/event"
	"github.com/freesoulcode/foya/internal/question"
	"github.com/freesoulcode/foya/internal/state"
)

func TestAskUserToolReturnsCompleteAnswers(t *testing.T) {
	bus := broker.New[event.Event]()
	gateway := question.NewGateway(bus, state.NewMemLog())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events := bus.Subscribe(ctx, "session:session-1")
	ask := NewAskUserTool(gateway)
	resultCh := make(chan Result, 1)
	go func() {
		result, _ := ask.Run(
			WithSessionID(context.Background(), "session-1"),
			Call{ID: "call-1", Input: []byte(`{"questions":[{"id":"choice","question":"Choose","options":[{"label":"A"}]}]}`)},
		)
		resultCh <- result
	}()

	var batch question.Batch
	select {
	case ev := <-events:
		if ev.Kind != event.KindQuestionRequested {
			t.Fatalf("event kind = %s", ev.Kind)
		}
		var ok bool
		batch, ok = ev.Payload.(question.Batch)
		if !ok {
			t.Fatalf("payload = %T", ev.Payload)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for question request")
	}
	if err := gateway.Answer("session-1", batch.ID, []question.Answer{{QuestionID: "choice", Value: "A"}}); err != nil {
		t.Fatal(err)
	}
	result := <-resultCh
	if result.IsError || len(result.Content) != 1 || !strings.Contains(result.Content[0].Text, `"value":"A"`) {
		t.Fatalf("tool result = %#v", result)
	}
}
