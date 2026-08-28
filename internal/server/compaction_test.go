package server

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/freesoulcode/foya/internal/agent"
	"github.com/freesoulcode/foya/internal/approval"
	"github.com/freesoulcode/foya/internal/backend"
	"github.com/freesoulcode/foya/internal/broker"
	"github.com/freesoulcode/foya/internal/config"
	"github.com/freesoulcode/foya/internal/event"
	"github.com/freesoulcode/foya/internal/message"
	"github.com/freesoulcode/foya/internal/protocol"
	"github.com/freesoulcode/foya/internal/provider"
	"github.com/freesoulcode/foya/internal/session"
	"github.com/freesoulcode/foya/internal/state"
	"github.com/freesoulcode/foya/internal/tool"
)

type compactingProvider struct{}

func (compactingProvider) Name() string { return "test" }

func (compactingProvider) Stream(
	context.Context,
	provider.Request,
) (<-chan provider.StreamEvent, error) {
	ch := make(chan provider.StreamEvent, 1)
	ch <- provider.StreamEvent{Type: "done", FinishReason: "stop"}
	close(ch)
	return ch, nil
}

func (compactingProvider) Complete(context.Context, provider.Request) (string, error) {
	return `## Goal
Continue the task.
## Progress
History was summarized.
## Key Decisions
Preserve canonical events.
## Next Steps
Continue with the next request.
## Critical Context
No unresolved detail.`, nil
}

func TestCompactSessionRoute(t *testing.T) {
	sessions := session.NewMemManager()
	log := state.NewMemLog()
	bus := broker.New[event.Event]()
	gateway := approval.NewGateway(bus, log)
	engine := agent.NewEngine(
		log,
		bus,
		sessions,
		compactingProvider{},
		"test-model",
		tool.NewRegistry(),
		gateway,
	)
	be := backend.New(sessions, log, bus, engine, gateway, nil, config.Provider{}, t.TempDir())
	sess, err := be.CreateSession(session.CreateOptions{Model: "test-model"})
	if err != nil {
		t.Fatal(err)
	}
	for _, msg := range []message.Message{
		{Role: message.RoleUser, Content: "question"},
		{Role: message.RoleAssistant, Content: strings.Repeat("a sufficiently detailed completed answer ", 100)},
	} {
		if _, err := log.Append(context.Background(), event.Event{
			Kind: event.KindMessageEnd, Session: sess.ID, Payload: msg,
		}); err != nil {
			t.Fatal(err)
		}
	}

	var result protocol.CompactSessionResponse
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
