package tool

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/freesoulcode/foya/internal/approval"
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

func TestBashUsesExecutionBoundary(t *testing.T) {
	project := t.TempDir()
	runner := &recordingRunner{result: sandbox.ExecResult{Stdout: []byte("ok\n")}}
	instance := NewBashTool(allowGateway{}, runner)
	ctx := approval.WithMode(WithCWD(context.Background(), project), approval.ModeManual)

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
	ctx := approval.WithMode(WithCWD(context.Background(), project), approval.ModeManual)

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
	assertWorkspaceProfile(t, call.profile, project)
}

func TestEditUsesExecutionBoundary(t *testing.T) {
	project := t.TempDir()
	path := filepath.Join(project, "file.txt")
	if err := os.WriteFile(path, []byte("before"), 0o644); err != nil {
		t.Fatal(err)
	}
	runner := &recordingRunner{}
	instance := NewEditTool(allowGateway{}, runner)
	ctx := approval.WithMode(WithCWD(context.Background(), project), approval.ModeManual)

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
	assertWorkspaceProfile(t, call.profile, project)
}

func TestBypassUsesFullAccessProfile(t *testing.T) {
	runner := &recordingRunner{}
	instance := NewBashTool(allowGateway{}, runner)
	ctx := approval.WithMode(context.Background(), approval.ModeFullAccess)

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
	ctx := approval.WithMode(context.Background(), approval.ModeFullAccess)

	result, err := instance.Run(ctx, Call{Input: []byte(`{"command":"true"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError || !strings.Contains(result.Content[0].Text, "boundary is unavailable") {
		t.Fatalf("result = %#v", result)
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
