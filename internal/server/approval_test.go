package server

import (
	"net/http"
	"testing"
)

func TestApprovalAPIRejectsLegacyMode(t *testing.T) {
	handler, _ := newQueueTestServer(t)
	code := requestJSON(
		t,
		handler,
		http.MethodPost,
		"/sessions",
		map[string]string{"approval_mode": "ask"},
		nil,
	)
	if code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", code, http.StatusBadRequest)
	}
}

func TestApprovalAPIRejectsInvalidDecision(t *testing.T) {
	handler, sessionID := newQueueTestServer(t)
	code := requestJSON(
		t,
		handler,
		http.MethodPost,
		"/sessions/"+sessionID+"/approvals/request-1",
		map[string]string{"decision": "allow"},
		nil,
	)
	if code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", code, http.StatusBadRequest)
	}
}
