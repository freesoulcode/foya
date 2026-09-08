package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
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

func TestRewindTurnRoutePreviewsAndConfirms(t *testing.T) {
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
	sess, err := be.CreateSession(conversation.CreateOptions{Model: "test-model"})
	if err != nil {
		t.Fatal(err)
	}
	filePath := filepath.Join(t.TempDir(), "main.go")
	beforeContent := []byte("old\n")
	afterContent := []byte("new\n")
	if err := os.WriteFile(filePath, afterContent, 0o644); err != nil {
		t.Fatal(err)
	}

	target := appendServerHistoryMessage(t, log, sess.ID, conversation.Message{
		Role: conversation.RoleUser, Content: "old request",
	})
	appendServerHistoryMessage(t, log, sess.ID, conversation.Message{
		Role: conversation.RoleAssistant,
		ToolCalls: []conversation.ToolCall{{
			ID:    "write-1",
			Name:  "write",
			Input: json.RawMessage(`{"path":"main.go","content":"new"}`),
		}},
	})
	appendServerHistoryMessage(t, log, sess.ID, conversation.Message{
		Role:       conversation.RoleTool,
		ToolCallID: "write-1",
		Content:    "written",
		Diff:       "--- " + filePath + "\n+++ " + filePath + "\n@@ -1,1 +1,1 @@\n-old\n+new\n",
		FileChange: &conversation.FileChange{
			Path:            filePath,
			BeforeExists:    true,
			BeforeMode:      0o644,
			AfterMode:       0o644,
			BeforeBlob:      rewindTestHash(beforeContent),
			AfterBlob:       rewindTestHash(afterContent),
			BeforeContent:   beforeContent,
			AfterContent:    afterContent,
			ContentCaptured: true,
		},
	})

	handler := New(config.Config{}, be).Handler()
	path := "/sessions/" + sess.ID + "/turns/" +
		strconv.FormatUint(uint64(target), 10) + "/rewind"
	var preview RewindTurnResponse
	code := requestJSON(
		t,
		handler,
		http.MethodPost,
		path,
		RewindTurnRequest{},
		&preview,
	)
	if code != http.StatusOK ||
		preview.Status != kernel.RewindConfirmationNeeded ||
		preview.Message != "old request" ||
		len(preview.Files) != 1 ||
		preview.Files[0].Path != filepath.ToSlash(filePath) ||
		preview.Files[0].Status != kernel.RewindFileReady ||
		preview.FileStateToken == "" {
		t.Fatalf("preview status=%d body=%#v", code, preview)
	}

	var applied RewindTurnResponse
	code = requestJSON(
		t,
		handler,
		http.MethodPost,
		path,
		RewindTurnRequest{
			Confirm:           true,
			ExpectedHeadSeq:   preview.HeadSeq,
			ExpectedFileState: preview.FileStateToken,
		},
		&applied,
	)
	if code != http.StatusOK ||
		applied.Status != kernel.RewindApplied ||
		applied.Message != "old request" {
		t.Fatalf("confirmed status=%d body=%#v", code, applied)
	}

	history, err := be.History(context.Background(), sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 0 {
		t.Fatalf("rewound history = %#v", history)
	}
	restored, err := os.ReadFile(filePath)
	if err != nil || string(restored) != string(beforeContent) {
		t.Fatalf("restored file = %q, err = %v", restored, err)
	}
}

func rewindTestHash(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

func appendServerHistoryMessage(
	t *testing.T,
	log conversation.Store,
	sessionID string,
	msg conversation.Message,
) conversation.Seq {
	t.Helper()
	seq, err := log.Append(context.Background(), conversation.Event{
		Kind:    conversation.KindMessageEnd,
		Session: sessionID,
		Payload: msg,
		Time:    time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return seq
}
