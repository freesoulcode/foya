package sandbox

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"
)

type recordingSandbox struct {
	request ExecRequest
	profile Profile
	err     error
}

func (s *recordingSandbox) Wrap(req ExecRequest, profile Profile) (ExecRequest, error) {
	s.request = req
	s.profile = profile
	return req, s.err
}

func (*recordingSandbox) Kind() Kind { return KindNone }

func TestRunnerUsesSandboxBoundary(t *testing.T) {
	backend := &recordingSandbox{}
	runner := NewRunnerWithBackend(backend)
	profile := Profile{FileSystem: FSReadOnly}

	result, err := runner.Run(context.Background(), ExecRequest{
		Argv: []string{"/bin/echo", "hello"},
	}, profile)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(result.Stdout); got != "hello\n" {
		t.Fatalf("stdout = %q", got)
	}
	if !reflect.DeepEqual(backend.request.Argv, []string{"/bin/echo", "hello"}) {
		t.Fatalf("boundary received argv %#v", backend.request.Argv)
	}
	if !reflect.DeepEqual(backend.profile, profile) {
		t.Fatalf("boundary received profile %#v", backend.profile)
	}
}

func TestRunnerFailsClosedWhenBoundaryFails(t *testing.T) {
	boundaryErr := errors.New("boundary failed")
	runner := NewRunnerWithBackend(&recordingSandbox{err: boundaryErr})

	_, err := runner.Run(context.Background(), ExecRequest{
		Argv: []string{"/bin/echo", "must not run"},
	}, Profile{FileSystem: FSReadOnly})
	if !errors.Is(err, boundaryErr) {
		t.Fatalf("error = %v, want %v", err, boundaryErr)
	}
}

func TestWorkspaceWriteProfileProtectsAgentMetadata(t *testing.T) {
	profile := WorkspaceWriteProfile("/tmp/project")
	if profile.FileSystem != FSProjectWrite || profile.Network {
		t.Fatalf("unexpected profile: %#v", profile)
	}
	for _, suffix := range []string{".git", ".agents", ".foya"} {
		want := "/tmp/project/" + suffix
		if !containsRoot(profile.ReadOnlyRoots, want) {
			t.Fatalf("read-only roots %#v do not contain %q", profile.ReadOnlyRoots, want)
		}
	}
}

func TestBubblewrapRequest(t *testing.T) {
	request := ExecRequest{
		Argv:     []string{"/bin/sh", "-c", "true"},
		Dir:      "/workspace",
		Env:      []string{"KEY=value"},
		Stdin:    []byte("input"),
		PathArgs: []int{1},
	}
	profile := Profile{
		FileSystem:    FSProjectWrite,
		WritableRoots: []string{"/workspace", "/tmp"},
		ReadOnlyRoots: []string{"/workspace/.git"},
	}
	wrapped, err := bubblewrapRequest(
		[]string{"launcher", "--exec", "/usr/bin/bwrap"},
		nil,
		request,
		profile,
	)
	if err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(wrapped.Argv[:3], []string{"launcher", "--exec", "/usr/bin/bwrap"}) {
		t.Fatalf("launcher prefix = %#v", wrapped.Argv[:3])
	}
	for _, sequence := range [][]string{
		{"--ro-bind", "/", "/"},
		{"--bind", "/workspace", "/workspace"},
		{"--ro-bind", "/workspace/.git", "/workspace/.git"},
		{"--unshare-net"},
		{"--tmpfs", "/run"},
		{"--chdir", "/workspace"},
		{"--", "/bin/sh", "-c", "true"},
	} {
		if !containsSequence(wrapped.Argv, sequence) {
			t.Fatalf("argv %#v does not contain %#v", wrapped.Argv, sequence)
		}
	}
	if wrapped.Dir != "" || wrapped.PathArgs != nil {
		t.Fatalf("host-only request fields were retained: %#v", wrapped)
	}
	if !reflect.DeepEqual(wrapped.Env, request.Env) || !reflect.DeepEqual(wrapped.Stdin, request.Stdin) {
		t.Fatalf("environment or stdin changed: %#v", wrapped)
	}
}

func TestBubblewrapRequestAllowsNetworkOnlyWhenRequested(t *testing.T) {
	wrapped, err := bubblewrapRequest(
		[]string{"/usr/bin/bwrap"},
		nil,
		ExecRequest{Argv: []string{"/bin/true"}},
		Profile{FileSystem: FSReadOnly, Network: true},
	)
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(wrapped.Argv, "--unshare-net") {
		t.Fatalf("network namespace unexpectedly isolated: %#v", wrapped.Argv)
	}
	if containsSequence(wrapped.Argv, []string{"--tmpfs", "/run"}) {
		t.Fatalf("runtime sockets unexpectedly masked: %#v", wrapped.Argv)
	}
}

func TestWSLBubblewrapLauncherDisablesHostInterop(t *testing.T) {
	launcher := wslBubblewrapLauncher(`C:\Windows\System32\wsl.exe`)
	hardening := wslBubblewrapHardening()
	for _, sequence := range [][]string{
		{`C:\Windows\System32\wsl.exe`, "--exec", "/usr/bin/bwrap"},
	} {
		if !containsSequence(launcher, sequence) {
			t.Fatalf("launcher %#v does not contain %#v", launcher, sequence)
		}
	}
	for _, sequence := range [][]string{
		{"--unsetenv", "WSL_INTEROP"},
		{"--unsetenv", "WSLENV"},
		{"--ro-bind", "/dev/null", "/init"},
	} {
		if !containsSequence(hardening, sequence) {
			t.Fatalf("hardening %#v does not contain %#v", hardening, sequence)
		}
	}
	wrapped, err := bubblewrapRequest(
		launcher,
		hardening,
		ExecRequest{Argv: []string{"/bin/true"}},
		Profile{FileSystem: FSReadOnly},
	)
	if err != nil {
		t.Fatal(err)
	}
	rootMount := sequenceIndex(wrapped.Argv, []string{"--ro-bind", "/", "/"})
	initMask := sequenceIndex(wrapped.Argv, []string{"--ro-bind", "/dev/null", "/init"})
	if rootMount < 0 || initMask <= rootMount {
		t.Fatalf("WSL hardening must follow the root mount: %#v", wrapped.Argv)
	}
}

func containsSequence(values, sequence []string) bool {
	return sequenceIndex(values, sequence) >= 0
}

func sequenceIndex(values, sequence []string) int {
	for i := 0; i+len(sequence) <= len(values); i++ {
		if slices.Equal(values[i:i+len(sequence)], sequence) {
			return i
		}
	}
	return -1
}

func containsRoot(roots []string, want string) bool {
	for _, root := range roots {
		if root == want {
			return true
		}
	}
	return false
}
