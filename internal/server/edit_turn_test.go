package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/freesoulcode/foya/internal/agent"
	"github.com/freesoulcode/foya/internal/approval"
	"github.com/freesoulcode/foya/internal/backend"
	"github.com/freesoulcode/foya/internal/broker"
	"github.com/freesoulcode/foya/internal/config"
	"github.com/freesoulcode/foya/internal/event"
	"github.com/freesoulcode/foya/internal/message"
	"github.com/freesoulcode/foya/internal/protocol"
	"github.com/freesoulcode/foya/internal/session"
	"github.com/freesoulcode/foya/internal/state"
	"github.com/freesoulcode/foya/internal/tool"
)

func TestEditTurnRouteRequiresAndAcceptsEffectConfirmation(t *testing.T) {
	sessions := session.NewMemManager()
	log := state.NewMemLog()
	bus := broker.New[event.Event]()
	gateway := approval.NewGateway(bus, log)
	engine := agent.NewEngine(
		log,
		bus,
		sessions,
		idleProvider{},
		"test-model",
		tool.NewRegistry(),
		gateway,
	)
	be := backend.New(sessions, log, bus, engine, gateway, nil, config.Provider{}, t.TempDir())
	sess, err := be.CreateSession(session.CreateOptions{Model: "test-model"})
	if err != nil {
		t.Fatal(err)
	}

	target := appendServerHistoryMessage(t, log, sess.ID, message.Message{
		Role: message.RoleUser, Content: "old request",
	})
	appendServerHistoryMessage(t, log, sess.ID, message.Message{
		Role: message.RoleAssistant,
		ToolCalls: []message.ToolCall{{
			ID:    "write-1",
			Name:  "write",
			Input: json.RawMessage(`{"path":"main.go","content":"new"}`),
		}},
	})
	appendServerHistoryMessage(t, log, sess.ID, message.Message{
		Role:       message.RoleTool,
		ToolCallID: "write-1",
		Content:    "written",
		Diff:       "@@ -0,0 +1 @@\n+new",
	})

	handler := New(config.Config{}, be).Handler()
	path := "/sessions/" + sess.ID + "/turns/" +
		strconv.FormatUint(uint64(target), 10) + "/edit"
	var preview protocol.EditTurnResponse
	code := requestJSON(
		t,
		handler,
		http.MethodPost,
		path,
		protocol.EditTurnRequest{Message: "new request"},
		&preview,
	)
	if code != http.StatusOK ||
		preview.Status != backend.EditConfirmationRequired ||
		len(preview.Effects) != 1 {
		t.Fatalf("preview status=%d body=%#v", code, preview)
	}

	var started protocol.EditTurnResponse
	code = requestJSON(
		t,
		handler,
		http.MethodPost,
		path,
		protocol.EditTurnRequest{
			Message:         "new request",
			ConfirmEffects:  true,
			ExpectedHeadSeq: preview.HeadSeq,
		},
		&started,
	)
	if code != http.StatusOK || started.Status != backend.EditStarted {
		t.Fatalf("confirmed status=%d body=%#v", code, started)
	}

	deadline := time.Now().Add(time.Second)
	for {
		history, historyErr := be.History(context.Background(), sess.ID)
		if historyErr != nil {
			t.Fatal(historyErr)
		}
		if len(history) > 0 && history[0].Content == "new request" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("edited history not projected: %#v", history)
		}
		time.Sleep(time.Millisecond)
	}
}

func appendServerHistoryMessage(
	t *testing.T,
	log *state.MemLog,
	sessionID string,
	msg message.Message,
) event.Seq {
	t.Helper()
	seq, err := log.Append(context.Background(), event.Event{
		Kind:    event.KindMessageEnd,
		Session: sessionID,
		Payload: msg,
		Time:    time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return seq
}
