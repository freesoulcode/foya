package server

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/freesoulcode/foya/internal/agent"
	"github.com/freesoulcode/foya/internal/broker"
	"github.com/freesoulcode/foya/internal/config"
	conversation "github.com/freesoulcode/foya/internal/conversation"
	interaction "github.com/freesoulcode/foya/internal/interaction"
	kernel "github.com/freesoulcode/foya/internal/kernel"

	model "github.com/freesoulcode/foya/internal/model"

	"github.com/freesoulcode/foya/internal/terminal"
	"github.com/freesoulcode/foya/internal/tool"
)

type compactingProvider struct{}

func (compactingProvider) Name() string { return "test" }

func (compactingProvider) Stream(
	context.Context,
	model.Request,
) (<-chan model.StreamEvent, error) {
	ch := make(chan model.StreamEvent, 1)
	ch <- model.StreamEvent{Type: "done", FinishReason: "stop"}
	close(ch)
	return ch, nil
}

func (compactingProvider) CompleteDetailed(
	context.Context,
	model.Request,
) (model.Completion, error) {
	return model.Completion{Text: `## Goal
Continue the task.
## Progress
History was summarized.
## Key Decisions
Preserve canonical events.
## Next Steps
Continue with the next request.
## Critical Context
No unresolved detail.`, FinishReason: "stop"}, nil
}

func TestCompactSessionRoute(t *testing.T) {
	sessions := newTestSessionManager(t)
	log := newTestStore(t)
	bus := broker.New[conversation.Event]()
	gateway := interaction.NewGateway(bus, log)
	engine := agent.NewEngine(
		log,
		bus,
		sessions,
		compactingProvider{},
		"test-model",
		tool.NewRegistry(),
		gateway,
	)
	be := kernel.NewService(
		sessions,
		log,
		bus,
		engine,
		gateway,
		terminal.NewManager(),
		nil,
		config.Provider{},
		t.TempDir(),
	)
	sess, err := be.CreateSession(conversation.CreateOptions{Model: "test-model"})
	if err != nil {
		t.Fatal(err)
	}
	for _, msg := range []conversation.Message{
		{Role: conversation.RoleUser, Content: "question"},
		{Role: conversation.RoleAssistant, Content: strings.Repeat("a sufficiently detailed completed answer ", 100)},
	} {
		if _, err := log.Append(context.Background(), conversation.Event{
			Kind: conversation.KindMessageEnd, Session: sess.ID, Payload: msg,
		}); err != nil {
			t.Fatal(err)
		}
	}

	var result CompactSessionResponse
	code := requestJSON(
		t,
		New(config.Config{}, be).Handler(),
		http.MethodPost,
		"/sessions/"+sess.ID+"/compact",
		nil,
		&result,
	)
	if code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	if result.ThroughSeq == 0 {
		t.Fatal("response has no checkpoint boundary")
	}
}
