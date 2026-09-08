package kernel

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	conversation "github.com/freesoulcode/foya/internal/conversation"
)

func TestApplyFileRewindRestoresSelectedFilesAndCanCompensate(t *testing.T) {
	dir := t.TempDir()
	readyPath := filepath.Join(dir, "ready.txt")
	modifiedPath := filepath.Join(dir, "modified.txt")
	if err := os.WriteFile(readyPath, []byte("after\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(modifiedPath, []byte("user edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	readyCurrent, err := readFileSnapshot(readyPath)
	if err != nil {
		t.Fatal(err)
	}
	modifiedCurrent, err := readFileSnapshot(modifiedPath)
	if err != nil {
		t.Fatal(err)
	}
	candidates := []fileRewindCandidate{
		{
			key:     "ready",
			path:    readyPath,
			status:  RewindFileReady,
			current: readyCurrent,
			safePlan: fileRewindPlan{
				path: readyPath, before: readyCurrent,
				after: fileSnapshot{exists: true, content: []byte("before\n"), mode: 0o640},
			},
		},
		{
			key:     "modified",
			path:    modifiedPath,
			status:  RewindFileModified,
			current: modifiedCurrent,
			forcePlan: fileRewindPlan{
				path: modifiedPath, before: modifiedCurrent,
				after: fileSnapshot{exists: true, content: []byte("original\n"), mode: 0o644},
			},
		},
	}

	results, compensate, err := applyFileRewind(candidates, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 ||
		results[0].Action != conversation.RewindFileRestored ||
		results[1].Action != conversation.RewindFileKept {
		t.Fatalf("results = %#v", results)
	}
	assertFileContent(t, readyPath, "before\n")
	assertFileContent(t, modifiedPath, "user edit\n")

	if err := compensate(); err != nil {
		t.Fatal(err)
	}
	assertFileContent(t, readyPath, "after\n")
	assertFileContent(t, modifiedPath, "user edit\n")
}

func TestApplyFileRewindForceRestoresModifiedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file.txt")
	if err := os.WriteFile(path, []byte("external\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	current, err := readFileSnapshot(path)
	if err != nil {
		t.Fatal(err)
	}
	candidate := fileRewindCandidate{
		key:     "modified",
		path:    path,
		status:  RewindFileModified,
		current: current,
		forcePlan: fileRewindPlan{
			path: path, before: current,
			after: fileSnapshot{exists: true, content: []byte("before\n"), mode: 0o644},
		},
	}

	results, _, err := applyFileRewind(
		[]fileRewindCandidate{candidate},
		[]string{candidate.key},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Action != conversation.RewindFileForceRestored {
		t.Fatalf("results = %#v", results)
	}
	assertFileContent(t, path, "before\n")
}

func TestThreeWayRestorePreservesNonOverlappingUserEdit(t *testing.T) {
	afterAgent := fileSnapshot{
		exists:  true,
		content: []byte("user name\nagent implementation\nfooter\n"),
		mode:    0o644,
	}
	current := fileSnapshot{
		exists:  true,
		content: []byte("custom name\nagent implementation\nfooter\n"),
		mode:    0o644,
	}
	beforeAgent := fileSnapshot{
		exists:  true,
		content: []byte("user name\nold implementation\nfooter\n"),
		mode:    0o644,
	}

	merged, ok := threeWayRestore(afterAgent, current, beforeAgent)
	if !ok {
		t.Fatal("non-overlapping changes did not merge")
	}
	if got := string(merged.content); got != "custom name\nold implementation\nfooter\n" {
		t.Fatalf("merged content = %q", got)
	}
}

func TestThreeWayRestoreRejectsOverlappingEdit(t *testing.T) {
	afterAgent := fileSnapshot{exists: true, content: []byte("agent\n"), mode: 0o644}
	current := fileSnapshot{exists: true, content: []byte("user\n"), mode: 0o644}
	beforeAgent := fileSnapshot{exists: true, content: []byte("before\n"), mode: 0o644}

	if _, ok := threeWayRestore(afterAgent, current, beforeAgent); ok {
		t.Fatal("overlapping changes merged without a conflict")
	}
}

func TestApplyFileRewindRejectsUnknownForceFile(t *testing.T) {
	_, _, err := applyFileRewind(nil, []string{"unknown"})
	if !errors.Is(err, ErrFileRewindConflict) {
		t.Fatalf("force error = %v", err)
	}
}

func TestRecoverPreparedFileRewindRestoresOriginalState(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "file.txt")
	if err := os.WriteFile(path, []byte("current\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	journalID, err := store.BeginFileRewind(
		ctx,
		"session-1",
		1,
		2,
		[]conversation.FileRewindBackup{{
			Path:          path,
			BeforeExists:  true,
			BeforeMode:    0o640,
			BeforeContent: []byte("current\n"),
			AfterExists:   true,
			AfterMode:     0o640,
			AfterContent:  []byte("partially rewound\n"),
		}},
	)
	if err != nil || journalID == "" {
		t.Fatalf("begin journal: id=%q err=%v", journalID, err)
	}
	if err := os.WriteFile(path, []byte("partially rewound\n"), 0o640); err != nil {
		t.Fatal(err)
	}

	if err := RecoverFileRewinds(ctx, store); err != nil {
		t.Fatal(err)
	}
	assertFileContent(t, path, "current\n")
	journals, err := store.PendingFileRewinds(ctx)
	if err != nil || len(journals) != 0 {
		t.Fatalf("pending journals = %#v, err = %v", journals, err)
	}
}

func TestRecoverCommittedFileRewindOnlyCleansJournal(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "file.txt")
	if err := os.WriteFile(path, []byte("current\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	target, err := store.Append(ctx, conversation.Event{
		Kind:    conversation.KindMessageEnd,
		Session: "session-1",
		Time:    time.Now(),
		Payload: conversation.Message{Role: conversation.RoleUser, Content: "request"},
	})
	if err != nil {
		t.Fatal(err)
	}
	preview, err := store.Rewind(ctx, "session-1", target, false, 0, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	journalID, err := store.BeginFileRewind(
		ctx,
		"session-1",
		target,
		preview.HeadSeq,
		[]conversation.FileRewindBackup{{
			Path:          path,
			BeforeExists:  true,
			BeforeMode:    0o644,
			BeforeContent: []byte("current\n"),
			AfterExists:   true,
			AfterMode:     0o644,
			AfterContent:  []byte("rewound\n"),
		}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("rewound\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Rewind(
		ctx,
		"session-1",
		target,
		true,
		preview.HeadSeq,
		nil,
		journalID,
	); err != nil {
		t.Fatal(err)
	}

	if err := RecoverFileRewinds(ctx, store); err != nil {
		t.Fatal(err)
	}
	assertFileContent(t, path, "rewound\n")
	journals, err := store.PendingFileRewinds(ctx)
	if err != nil || len(journals) != 0 {
		t.Fatalf("pending journals = %#v, err = %v", journals, err)
	}
}

func capturedFileChange(
	path string,
	beforeExists bool,
	before string,
	after string,
) *conversation.FileChange {
	change := &conversation.FileChange{
		Path:            path,
		BeforeExists:    beforeExists,
		BeforeMode:      0o644,
		AfterMode:       0o644,
		AfterBlob:       contentHash([]byte(after)),
		BeforeContent:   []byte(before),
		AfterContent:    []byte(after),
		ContentCaptured: true,
	}
	if beforeExists {
		change.BeforeBlob = contentHash([]byte(before))
	}
	return change
}

func assertFileContent(t *testing.T, path, want string) {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != want {
		t.Fatalf("%s = %q, want %q", path, content, want)
	}
}
