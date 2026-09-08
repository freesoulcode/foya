package tool

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/freesoulcode/foya/internal/broker"
	conversation "github.com/freesoulcode/foya/internal/conversation"
	interaction "github.com/freesoulcode/foya/internal/interaction"
	"github.com/freesoulcode/foya/internal/testkit"
)

func TestAskUserToolReturnsCompleteAnswers(t *testing.T) {
	bus := broker.New[conversation.Event]()
	gateway := interaction.NewQuestionGateway(bus, testkit.NewLog())
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

	var batch interaction.Batch
	select {
	case ev := <-events:
		if ev.Kind != conversation.KindQuestionRequested {
			t.Fatalf("event kind = %s", ev.Kind)
		}
		var ok bool
		batch, ok = ev.Payload.(interaction.Batch)
		if !ok {
			t.Fatalf("payload = %T", ev.Payload)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for question request")
	}
	if err := gateway.Answer("session-1", batch.ID, []interaction.Answer{{QuestionID: "choice", Value: "A"}}); err != nil {
		t.Fatal(err)
	}
	result := <-resultCh
	if result.IsError || len(result.Content) != 1 || !strings.Contains(result.Content[0].Text, `"value":"A"`) {
		t.Fatalf("tool result = %#v", result)
	}
}
