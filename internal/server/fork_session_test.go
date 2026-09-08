package server

import (
	"net/http"
	"testing"

	conversation "github.com/freesoulcode/foya/internal/conversation"
)

func TestForkSessionRoute(t *testing.T) {
	handler, sourceID := newQueueTestServer(t)

	var forked conversation.Session
	status := requestJSON(t, handler, http.MethodPost, "/sessions/"+sourceID+"/fork", nil, &forked)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want %d", status, http.StatusOK)
	}
	if forked.ID == "" || forked.ID == sourceID {
		t.Fatalf("forked session = %#v", forked)
	}
	if forked.Title != "New chat copy" {
		t.Fatalf("forked title = %q", forked.Title)
	}
}

func TestForkSessionRouteRejectsUnknownThroughSeq(t *testing.T) {
	handler, sourceID := newQueueTestServer(t)

	status := requestJSON(
		t,
		handler,
		http.MethodPost,
		"/sessions/"+sourceID+"/fork",
		map[string]uint64{"through_seq": 999},
		nil,
	)
	if status != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", status, http.StatusNotFound)
	}
}
