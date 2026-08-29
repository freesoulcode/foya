package server

import (
	"net/http"
	"testing"

	"github.com/freesoulcode/foya/internal/session"
)

func TestSessionReasoningEffortRoutes(t *testing.T) {
	handler, sessionID := newQueueTestServer(t)

	var updated session.Session
	if code := requestJSON(
		t,
		handler,
		http.MethodPatch,
		"/sessions/"+sessionID,
		map[string]string{"reasoning_effort": "high"},
		&updated,
	); code != http.StatusOK {
		t.Fatalf("update status = %d", code)
	}
	if updated.ReasoningEffort != session.ReasoningEffortHigh {
		t.Fatalf("reasoning effort = %q, want %q", updated.ReasoningEffort, session.ReasoningEffortHigh)
	}

	if code := requestJSON(
		t,
		handler,
		http.MethodPatch,
		"/sessions/"+sessionID,
		map[string]string{"reasoning_effort": "maximum"},
		nil,
	); code != http.StatusBadRequest {
		t.Fatalf("invalid update status = %d, want %d", code, http.StatusBadRequest)
	}
}
