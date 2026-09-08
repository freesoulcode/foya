package interaction

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/freesoulcode/foya/internal/broker"
	conversation "github.com/freesoulcode/foya/internal/conversation"
	"github.com/freesoulcode/foya/internal/testkit"
)

func testQuestionGateway(t testing.TB) *questionGateway {
	return NewQuestionGateway(
		broker.New[conversation.Event](),
		testkit.NewLog(),
	).(*questionGateway)
}

func testBatch(id string) Batch {
	return Batch{
		ID:        id,
		SessionID: "session-1",
		Questions: []Question{
			{ID: "scope", Question: "Which scope?", Options: []Option{{Label: "small"}, {Label: "large"}}},
			{ID: "notes", Question: "Any notes?", AllowCustom: true},
		},
	}
}

func TestGatewayAnswersOneBatchOnce(t *testing.T) {
	gateway := testQuestionGateway(t)
	result := make(chan struct {
		answers []Answer
		err     error
	}, 1)
	go func() {
		answers, err := gateway.Ask(context.Background(), testBatch("batch-1"))
		result <- struct {
			answers []Answer
			err     error
		}{answers, err}
	}()
	waitForQuestionPending(t, gateway, "batch-1")

	invalid := []Answer{{QuestionID: "scope", Value: "invalid"}, {QuestionID: "notes", Value: "details"}}
	if err := gateway.Answer("session-1", "batch-1", invalid); !errors.Is(err, ErrInvalidBatch) {
		t.Fatalf("invalid answer error = %v", err)
	}
	answers := []Answer{{QuestionID: "scope", Value: "small"}, {QuestionID: "notes", Value: "details"}}
	if err := gateway.Answer("session-1", "batch-1", answers); err != nil {
		t.Fatal(err)
	}
	if err := gateway.Answer("session-1", "batch-1", answers); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second answer error = %v", err)
	}
	got := <-result
	if got.err != nil || len(got.answers) != 2 || got.answers[0].Value != "small" {
		t.Fatalf("ask result = %#v, %v", got.answers, got.err)
	}
}

func TestGatewayClearSessionUnblocksAsk(t *testing.T) {
	gateway := testQuestionGateway(t)
	done := make(chan error, 1)
	go func() {
		_, err := gateway.Ask(context.Background(), testBatch("batch-2"))
		done <- err
	}()
	waitForQuestionPending(t, gateway, "batch-2")
	gateway.ClearSession("session-1")
	if err := <-done; !errors.Is(err, ErrCancelled) {
		t.Fatalf("ask error = %v, want ErrCancelled", err)
	}
}

func waitForQuestionPending(t *testing.T, gateway *questionGateway, id string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		gateway.mu.Lock()
		_, ok := gateway.pending[id]
		gateway.mu.Unlock()
		if ok {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("question batch %q did not become pending", id)
}
