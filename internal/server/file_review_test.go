package server

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/freesoulcode/foya/internal/agent"
	"github.com/freesoulcode/foya/internal/broker"
	"github.com/freesoulcode/foya/internal/config"
	conversation "github.com/freesoulcode/foya/internal/conversation"
	interaction "github.com/freesoulcode/foya/internal/interaction"
	kernel "github.com/freesoulcode/foya/internal/kernel"

	"github.com/freesoulcode/foya/internal/terminal"
	"github.com/freesoulcode/foya/internal/tool"
)

func TestFileReviewRoutesListAndKeepChanges(t *testing.T) {
	sessions := newTestSessionManager(t)
	log := newTestStore(t)
	bus := broker.New[conversation.Event]()
	gateway := interaction.NewGateway(bus, log)
	engine := agent.NewEngine(
		log,
		bus,
		sessions,
		idleProvider{},
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
	current, err := be.CreateSession(conversation.CreateOptions{Model: "test-model"})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "main.go")
	before := []byte("before\n")
	after := []byte("after\n")
	if err := os.WriteFile(path, after, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := log.Append(context.Background(), conversation.Event{
		Kind:    conversation.KindMessageEnd,
		Session: current.ID,
		Time:    time.Now(),
		Payload: conversation.Message{
			Role:       conversation.RoleTool,
			ToolCallID: "write-1",
			Content:    "written",
			Diff:       "--- main.go\n+++ main.go\n@@ -1 +1,2 @@\n-before\n+after\n+more\n",
			FileChange: &conversation.FileChange{
				Path:            path,
				BeforeExists:    true,
				BeforeMode:      0o644,
				AfterMode:       0o644,
				BeforeBlob:      rewindTestHash(before),
				AfterBlob:       rewindTestHash(after),
				BeforeContent:   before,
				AfterContent:    after,
				ContentCaptured: true,
			},
		},
	}); err != nil {
		t.Fatal(err)
	}

	handler := New(config.Config{}, be).Handler()
	endpoint := "/sessions/" + current.ID + "/file-review"
	var review FileReviewResponse
	if code := requestJSON(
		t,
		handler,
		http.MethodGet,
		endpoint,
		nil,
		&review,
	); code != http.StatusOK {
		t.Fatalf("file review status = %d", code)
	}
	if len(review.Files) != 1 ||
		review.Files[0].Status != kernel.RewindFileReady ||
		review.Files[0].Additions != 1 ||
		review.Files[0].Deletions != 1 ||
		review.Files[0].Diff == "" ||
		review.ThroughSeq == 0 ||
		review.FileStateToken == "" {
		t.Fatalf("file review = %#v", review)
	}

	var result map[string]string
	if code := requestJSON(
		t,
		handler,
		http.MethodPost,
		endpoint,
		ResolveFileReviewRequest{
			Action:             "keep",
			ExpectedThroughSeq: review.ThroughSeq,
			ExpectedFileState:  review.FileStateToken,
		},
		&result,
	); code != http.StatusOK || result["status"] != "resolved" {
		t.Fatalf("resolve status=%d body=%#v", code, result)
	}

	review = FileReviewResponse{}
	if code := requestJSON(
		t,
		handler,
		http.MethodGet,
		endpoint,
		nil,
		&review,
	); code != http.StatusOK || len(review.Files) != 0 {
		t.Fatalf("resolved review status=%d body=%#v", code, review)
	}
}

func TestFileReviewRouteRejectsUnknownAction(t *testing.T) {
	handler, sessionID := newQueueTestServer(t)
	var response map[string]any
	code := requestJSON(
		t,
		handler,
		http.MethodPost,
		"/sessions/"+sessionID+"/file-review",
		json.RawMessage(`{"action":"discard","expected_through_seq":1,"expected_file_state":""}`),
		&response,
	)
	if code != http.StatusBadRequest {
		t.Fatalf("unknown action status=%d body=%#v", code, response)
	}
}
