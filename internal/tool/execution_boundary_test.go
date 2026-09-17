package tool

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	conversation "github.com/freesoulcode/foya/internal/conversation"
	interaction "github.com/freesoulcode/foya/internal/interaction"
	"github.com/freesoulcode/foya/internal/sandbox"
)

type runnerCall struct {
	request sandbox.ExecRequest
	profile sandbox.Profile
}

type recordingRunner struct {
	calls  []runnerCall
	result sandbox.ExecResult
	err    error
}

func (r *recordingRunner) Run(
	_ context.Context,
	request sandbox.ExecRequest,
	profile sandbox.Profile,
) (sandbox.ExecResult, error) {
	r.calls = append(r.calls, runnerCall{request: request, profile: profile})
	return r.result, r.err
}

func (*recordingRunner) Kind() sandbox.Kind { return sandbox.KindNone }

type recordingArtifactStore struct {
	sessionID string
	name      string
	mediaType string
	data      []byte
	err       error
	deletedID string
}

func (s *recordingArtifactStore) PutFile(
	_ context.Context,
	sessionID, name, mediaType string,
	src io.Reader,
) (conversation.AttachmentRef, error) {
	data, err := io.ReadAll(src)
	if err != nil {
		return conversation.AttachmentRef{}, err
	}
	if s.err != nil {
		return conversation.AttachmentRef{}, s.err
	}
	s.sessionID = sessionID
	s.name = name
	s.mediaType = mediaType
	s.data = data
	return conversation.AttachmentRef{
		ID:        "artifact-1",
		Name:      name,
		Kind:      "file",
		MediaType: "text/html; charset=utf-8",
		Bytes:     int64(len(data)),
	}, nil
}

func (s *recordingArtifactStore) Delete(_ context.Context, sessionID, artifactID string) error {
	s.sessionID = sessionID
	s.deletedID = artifactID
	return nil
}

func TestBashUsesExecutionBoundary(t *testing.T) {
	project := t.TempDir()
	runner := &recordingRunner{result: sandbox.ExecResult{Stdout: []byte("ok\n")}}
	instance := NewBashTool(allowGateway{}, runner)
	ctx := interaction.WithMode(WithCWD(context.Background(), project), interaction.ModeManual)

	result, err := instance.Run(ctx, Call{Input: []byte(`{"command":"printf ok"}`)})
	if err != nil || result.IsError {
		t.Fatalf("result = %#v, err = %v", result, err)
	}
	call := onlyRunnerCall(t, runner)
	if call.request.Dir != project || call.request.Argv[len(call.request.Argv)-1] != "printf ok" {
		t.Fatalf("boundary request = %#v", call.request)
	}
	assertWorkspaceProfile(t, call.profile, project)
}

func TestWriteUsesExecutionBoundary(t *testing.T) {
	project := t.TempDir()
	runner := &recordingRunner{}
	instance := NewWriteTool(allowGateway{}, runner)
	ctx := interaction.WithMode(WithCWD(context.Background(), project), interaction.ModeManual)

	result, err := instance.Run(ctx, Call{
		Input: []byte(`{"path":"nested/file.txt","content":"new content"}`),
	})
	if err != nil || result.IsError {
		t.Fatalf("result = %#v, err = %v", result, err)
	}
	call := onlyRunnerCall(t, runner)
	if string(call.request.Stdin) != "new content" {
		t.Fatalf("boundary stdin = %q", call.request.Stdin)
	}
	if got := call.request.Argv[len(call.request.Argv)-1]; got != filepath.Join(project, "nested/file.txt") {
		t.Fatalf("boundary target = %q", got)
	}
	if result.FileChange == nil ||
		result.FileChange.Path != filepath.Join(project, "nested/file.txt") ||
		result.FileChange.BeforeExists {
		t.Fatalf("file change = %#v", result.FileChange)
	}
	assertWorkspaceProfile(t, call.profile, project)
}

func TestWriteWithoutWorkspaceCreatesArtifact(t *testing.T) {
	runner := &recordingRunner{}
	artifacts := &recordingArtifactStore{}
	instance := NewWriteTool(allowGateway{}, runner, artifacts)
	ctx := interaction.WithMode(WithSessionID(context.Background(), "session-1"), interaction.ModeManual)

	result, err := instance.Run(ctx, Call{
		Input: []byte(`{"path":"reports/index.html","content":"<h1>Report</h1>"}`),
	})
	if err != nil || result.IsError {
		t.Fatalf("result = %#v, err = %v", result, err)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("runner calls = %d, want 0", len(runner.calls))
	}
	if artifacts.sessionID != "session-1" || artifacts.name != "index.html" ||
		string(artifacts.data) != "<h1>Report</h1>" {
		t.Fatalf("artifact write = %#v", artifacts)
	}
	if result.FileChange != nil || result.Diff != "" {
		t.Fatalf("artifact write should not create file diff/change: %#v diff=%q", result.FileChange, result.Diff)
	}
	if len(result.Content) != 2 || result.Content[1].Type != "artifact_ref" ||
		result.Content[1].Attachment == nil ||
		result.Content[1].Attachment.ID != "artifact-1" {
		t.Fatalf("artifact result content = %#v", result.Content)
	}
}

func TestWriteManagedWorkspaceUsesFixedDirectoryAndAttachesArtifact(t *testing.T) {
	workspace := t.TempDir()
	runner := &recordingRunner{}
	artifacts := &recordingArtifactStore{}
	instance := NewWriteTool(allowGateway{}, runner, artifacts)
	ctx := context.Background()
	ctx = WithCWD(ctx, workspace)
	ctx = WithManagedWorkspace(ctx, true)
	ctx = WithSessionID(ctx, "session-1")
	ctx = interaction.WithMode(ctx, interaction.ModeManual)

	result, err := instance.Run(ctx, Call{
		Input: []byte(`{"path":"reports/index.html","content":"<h1>Report</h1>"}`),
	})
	if err != nil || result.IsError {
		t.Fatalf("result = %#v, err = %v", result, err)
	}
	call := onlyRunnerCall(t, runner)
	if got := call.request.Argv[len(call.request.Argv)-1]; got != filepath.Join(workspace, "reports", "index.html") {
		t.Fatalf("boundary target = %q", got)
	}
	if artifacts.sessionID != "session-1" || artifacts.name != "index.html" ||
		string(artifacts.data) != "<h1>Report</h1>" {
		t.Fatalf("artifact write = %#v", artifacts)
	}
	if result.FileChange != nil || result.Diff != "" {
		t.Fatalf("managed workspace write should not create project diff/change: %#v diff=%q", result.FileChange, result.Diff)
	}
	if !strings.Contains(result.Content[0].Text, "reports/index.html") {
		t.Fatalf("managed write result text = %q", result.Content[0].Text)
	}
	if len(result.Content) != 2 || result.Content[1].Type != "artifact_ref" {
		t.Fatalf("managed write result content = %#v", result.Content)
	}
}

func TestWriteManagedWorkspaceFailsBeforeWritingWhenArtifactSnapshotFails(t *testing.T) {
	workspace := t.TempDir()
	runner := &recordingRunner{}
	artifacts := &recordingArtifactStore{err: errors.New("store unavailable")}
	instance := NewWriteTool(allowGateway{}, runner, artifacts)
	ctx := context.Background()
	ctx = WithCWD(ctx, workspace)
	ctx = WithManagedWorkspace(ctx, true)
	ctx = WithSessionID(ctx, "session-1")
	ctx = interaction.WithMode(ctx, interaction.ModeManual)

	result, err := instance.Run(ctx, Call{
		Input: []byte(`{"path":"report.html","content":"<h1>Report</h1>"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError || !strings.Contains(result.Content[0].Text, "artifact snapshot failed") {
		t.Fatalf("result = %#v", result)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("runner calls = %d, want 0", len(runner.calls))
	}
	if _, err := os.Stat(filepath.Join(workspace, "report.html")); !os.IsNotExist(err) {
		t.Fatalf("workspace file exists or cannot be checked: %v", err)
	}
}

func TestEditWithoutWorkspaceRejectsRelativePath(t *testing.T) {
	runner := &recordingRunner{}
	instance := NewEditTool(allowGateway{}, runner)

	result, err := instance.Run(context.Background(), Call{
		Input: []byte(`{"path":"file.txt","edits":[{"old_text":"a","new_text":"b"}]}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError || !strings.Contains(result.Content[0].Text, "requires a workspace") {
		t.Fatalf("result = %#v", result)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("runner calls = %d, want 0", len(runner.calls))
	}
}

func TestEditUsesExecutionBoundary(t *testing.T) {
	project := t.TempDir()
	path := filepath.Join(project, "file.txt")
	if err := os.WriteFile(path, []byte("before"), 0o644); err != nil {
		t.Fatal(err)
	}
	runner := &recordingRunner{}
	instance := NewEditTool(allowGateway{}, runner)
	ctx := interaction.WithMode(WithCWD(context.Background(), project), interaction.ModeManual)

	result, err := instance.Run(ctx, Call{
		Input: []byte(`{"path":"file.txt","edits":[{"old_text":"before","new_text":"after"}]}`),
	})
	if err != nil || result.IsError {
		t.Fatalf("result = %#v, err = %v", result, err)
	}
	call := onlyRunnerCall(t, runner)
	if string(call.request.Stdin) != "after" {
		t.Fatalf("boundary stdin = %q", call.request.Stdin)
	}
	if got := call.request.Argv[len(call.request.Argv)-1]; got != path {
		t.Fatalf("boundary target = %q", got)
	}
	if result.FileChange == nil ||
		result.FileChange.Path != path ||
		!result.FileChange.BeforeExists {
		t.Fatalf("file change = %#v", result.FileChange)
	}
	assertWorkspaceProfile(t, call.profile, project)
}

func TestEditManagedWorkspaceAttachesArtifact(t *testing.T) {
	workspace := t.TempDir()
	path := filepath.Join(workspace, "file.txt")
	if err := os.WriteFile(path, []byte("before"), 0o644); err != nil {
		t.Fatal(err)
	}
	runner := &recordingRunner{}
	artifacts := &recordingArtifactStore{}
	instance := NewEditTool(allowGateway{}, runner, artifacts)
	ctx := context.Background()
	ctx = WithCWD(ctx, workspace)
	ctx = WithManagedWorkspace(ctx, true)
	ctx = WithSessionID(ctx, "session-1")
	ctx = interaction.WithMode(ctx, interaction.ModeManual)

	result, err := instance.Run(ctx, Call{
		Input: []byte(`{"path":"file.txt","edits":[{"old_text":"before","new_text":"after"}]}`),
	})
	if err != nil || result.IsError {
		t.Fatalf("result = %#v, err = %v", result, err)
	}
	if artifacts.name != "file.txt" || string(artifacts.data) != "after" {
		t.Fatalf("artifact write = %#v", artifacts)
	}
	if len(result.Content) != 2 || result.Content[1].Type != "artifact_ref" {
		t.Fatalf("managed edit result content = %#v", result.Content)
	}
	if result.FileChange != nil || result.Diff != "" {
		t.Fatalf("managed workspace edit should not create project diff/change: %#v diff=%q", result.FileChange, result.Diff)
	}
}

func TestEditManagedWorkspaceFailsBeforeWritingWhenArtifactSnapshotFails(t *testing.T) {
	workspace := t.TempDir()
	path := filepath.Join(workspace, "file.txt")
	if err := os.WriteFile(path, []byte("before"), 0o644); err != nil {
		t.Fatal(err)
	}
	runner := &recordingRunner{}
	artifacts := &recordingArtifactStore{err: errors.New("store unavailable")}
	instance := NewEditTool(allowGateway{}, runner, artifacts)
	ctx := context.Background()
	ctx = WithCWD(ctx, workspace)
	ctx = WithManagedWorkspace(ctx, true)
	ctx = WithSessionID(ctx, "session-1")
	ctx = interaction.WithMode(ctx, interaction.ModeManual)

	result, err := instance.Run(ctx, Call{
		Input: []byte(`{"path":"file.txt","edits":[{"old_text":"before","new_text":"after"}]}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError || !strings.Contains(result.Content[0].Text, "artifact snapshot failed") {
		t.Fatalf("result = %#v", result)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("runner calls = %d, want 0", len(runner.calls))
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "before" {
		t.Fatalf("workspace file = %q, err = %v", data, err)
	}
}

func TestBypassUsesFullAccessProfile(t *testing.T) {
	runner := &recordingRunner{}
	instance := NewBashTool(allowGateway{}, runner)
	ctx := interaction.WithMode(context.Background(), interaction.ModeFullAccess)

	_, err := instance.Run(ctx, Call{Input: []byte(`{"command":"true"}`)})
	if err != nil {
		t.Fatal(err)
	}
	call := onlyRunnerCall(t, runner)
	if call.profile.FileSystem != sandbox.FSFull || !call.profile.Network {
		t.Fatalf("bypass profile = %#v", call.profile)
	}
}

func TestBashFailsWhenExecutionBoundaryIsUnavailable(t *testing.T) {
	instance := NewBashTool(allowGateway{}, nil)
	ctx := interaction.WithMode(context.Background(), interaction.ModeFullAccess)

	result, err := instance.Run(ctx, Call{Input: []byte(`{"command":"true"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError || !strings.Contains(result.Content[0].Text, "boundary is unavailable") {
		t.Fatalf("result = %#v", result)
	}
}

func TestBashRejectsPermanentDeletionCommands(t *testing.T) {
	for _, command := range []string{
		"rm -rf build",
		"/bin/rm file.txt",
		"find . -name '*.tmp' -delete",
		"git clean -fd",
		"Remove-Item -Recurse build",
	} {
		t.Run(command, func(t *testing.T) {
			runner := &recordingRunner{}
			instance := NewBashTool(allowGateway{}, runner)
			result, err := instance.Run(context.Background(), Call{
				Input: []byte(`{"command":` + quotedJSON(command) + `}`),
			})
			if err != nil || !result.IsError ||
				!strings.Contains(result.Content[0].Text, "delete tool") {
				t.Fatalf("result = %#v, err = %v", result, err)
			}
			if len(runner.calls) != 0 {
				t.Fatalf("runner received permanent deletion command: %#v", runner.calls)
			}
		})
	}
}

func TestBashAllowsPermanentDeletionInFullAccess(t *testing.T) {
	runner := &recordingRunner{}
	instance := NewBashTool(allowGateway{}, runner)
	ctx := interaction.WithMode(context.Background(), interaction.ModeFullAccess)

	result, err := instance.Run(ctx, Call{
		Input: []byte(`{"command":"rm -rf build"}`),
	})
	if err != nil || result.IsError {
		t.Fatalf("result = %#v, err = %v", result, err)
	}
	if len(runner.calls) != 1 {
		t.Fatalf("runner calls = %d, want 1", len(runner.calls))
	}
}

func onlyRunnerCall(t *testing.T, runner *recordingRunner) runnerCall {
	t.Helper()
	if len(runner.calls) != 1 {
		t.Fatalf("runner calls = %d, want 1", len(runner.calls))
	}
	return runner.calls[0]
}

func assertWorkspaceProfile(t *testing.T, profile sandbox.Profile, project string) {
	t.Helper()
	if profile.FileSystem != sandbox.FSProjectWrite || profile.Network {
		t.Fatalf("profile = %#v", profile)
	}
	for _, root := range profile.WritableRoots {
		if root == project {
			return
		}
	}
	t.Fatalf("writable roots %#v do not contain project %q", profile.WritableRoots, project)
}
