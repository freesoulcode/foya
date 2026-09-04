package backend

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/freesoulcode/foya/internal/event"
	"github.com/freesoulcode/foya/internal/message"
)

func TestFileReviewCanKeepOrUndoPendingChanges(t *testing.T) {
	t.Run("keep", func(t *testing.T) {
		be, sessionID, _ := newQueueTestBackend(t)
		path := filepath.Join(t.TempDir(), "kept.txt")
		if err := os.WriteFile(path, []byte("after\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		appendHistoryMessage(t, be, sessionID, message.Message{
			Role:       message.RoleTool,
			ToolCallID: "write-1",
			Diff:       "--- kept.txt\n+++ kept.txt\n@@ -1 +1 @@\n-before\n+after\n",
			FileChange: capturedFileChange(path, true, "before\n", "after\n"),
		})

		review, err := be.PendingFileReview(context.Background(), sessionID)
		if err != nil {
			t.Fatal(err)
		}
		if len(review.Files) != 1 ||
			review.Files[0].Status != RewindFileReady ||
			review.Files[0].Additions != 1 ||
			review.Files[0].Deletions != 1 ||
			review.Files[0].Diff == "" ||
			review.ThroughSeq == 0 {
			t.Fatalf("pending review = %#v", review)
		}
		if err := be.KeepFileChanges(
			context.Background(),
			sessionID,
			review.ThroughSeq,
		); err != nil {
			t.Fatal(err)
		}
		assertFileContent(t, path, "after\n")
		assertNoPendingFileReview(t, be, sessionID)
		assertFileReviewEvent(t, be, sessionID, event.FileReviewKept)
	})

	t.Run("undo", func(t *testing.T) {
		be, sessionID, _ := newQueueTestBackend(t)
		path := filepath.Join(t.TempDir(), "undone.txt")
		if err := os.WriteFile(path, []byte("after\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		appendHistoryMessage(t, be, sessionID, message.Message{
			Role:       message.RoleTool,
			ToolCallID: "write-1",
			Diff:       "--- undone.txt\n+++ undone.txt\n@@ -1 +1 @@\n-before\n+after\n",
			FileChange: capturedFileChange(path, true, "before\n", "after\n"),
		})

		review, err := be.PendingFileReview(context.Background(), sessionID)
		if err != nil {
			t.Fatal(err)
		}
		if err := be.UndoFileChanges(
			context.Background(),
			sessionID,
			review.ThroughSeq,
			review.FileStateToken,
			nil,
		); err != nil {
			t.Fatal(err)
		}
		assertFileContent(t, path, "before\n")
		assertNoPendingFileReview(t, be, sessionID)
		assertFileReviewEvent(t, be, sessionID, event.FileReviewUndone)
	})
}

func TestFileReviewReversesMultipleChangesAroundUserEdit(t *testing.T) {
	be, sessionID, _ := newQueueTestBackend(t)
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "main.go")
	beforeFirst := "name\nold implementation\nfooter\n"
	afterFirst := "name\nagent one\nfooter\n"
	beforeSecond := "custom name\nagent one\nfooter\n"
	afterSecond := "custom name\nagent two\nfooter\n"
	if err := os.WriteFile(path, []byte(afterSecond), 0o644); err != nil {
		t.Fatal(err)
	}
	appendHistoryMessage(t, be, sessionID, message.Message{
		Role:       message.RoleTool,
		ToolCallID: "edit-1",
		Diff:       "--- main.go\n+++ main.go\n@@ -2 +2 @@\n-old implementation\n+agent one\n",
		FileChange: capturedFileChange(path, true, beforeFirst, afterFirst),
	})
	appendHistoryMessage(t, be, sessionID, message.Message{
		Role:       message.RoleTool,
		ToolCallID: "edit-2",
		Diff:       "--- main.go\n+++ main.go\n@@ -2 +2 @@\n-agent one\n+agent two\n",
		FileChange: capturedFileChange(path, true, beforeSecond, afterSecond),
	})

	review, err := be.PendingFileReview(ctx, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(review.Files) != 1 ||
		review.Files[0].Status != RewindFileMergeable ||
		review.Files[0].Additions != 2 ||
		review.Files[0].Deletions != 2 ||
		review.Files[0].Diff == "" {
		t.Fatalf("pending review = %#v", review)
	}
	if err := be.UndoFileChanges(
		ctx,
		sessionID,
		review.ThroughSeq,
		review.FileStateToken,
		nil,
	); err != nil {
		t.Fatal(err)
	}
	assertFileContent(t, path, "custom name\nold implementation\nfooter\n")
}

func assertNoPendingFileReview(t *testing.T, be *Backend, sessionID string) {
	t.Helper()
	review, err := be.PendingFileReview(context.Background(), sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(review.Files) != 0 || review.ThroughSeq != 0 {
		t.Fatalf("review remained pending: %#v", review)
	}
}

func assertFileReviewEvent(
	t *testing.T,
	be *Backend,
	sessionID string,
	action string,
) {
	t.Helper()
	events, err := be.log.Read(context.Background(), sessionID, 0)
	if err != nil {
		t.Fatal(err)
	}
	resolved := lastEvent(events, event.KindFileReviewResolved)
	if resolved == nil {
		t.Fatalf("missing file_review_resolved event: %#v", eventKinds(events))
	}
	payload, ok := resolved.Payload.(event.FileReviewResolved)
	if !ok || payload.Action != action {
		t.Fatalf("file review payload = %#v", resolved.Payload)
	}
}
