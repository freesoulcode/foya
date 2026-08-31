//go:build windows

package sandbox

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestWindowsWSL2WorkspaceWritePolicy(t *testing.T) {
	backend := newPlatformSandbox().(*wslSandbox)
	if err := backend.available(); err != nil {
		t.Skip(err)
	}
	runner := NewRunnerWithBackend(backend)

	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	project, err := os.MkdirTemp(home, ".foya-wsl-project-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(project)
	outside, err := os.MkdirTemp(home, ".foya-wsl-outside-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(outside)
	protected := filepath.Join(project, ".git")
	if err := os.Mkdir(protected, 0o755); err != nil {
		t.Fatal(err)
	}

	profile := WorkspaceWriteProfile(project)
	t.Run("project write", func(t *testing.T) {
		target := filepath.Join(project, "allowed.txt")
		result, err := runner.Run(context.Background(), wslShellWrite(target), profile)
		if err != nil {
			t.Fatalf("write failed: %v: %s", err, result.Stderr)
		}
	})
	t.Run("outside write", func(t *testing.T) {
		assertWSLWriteDenied(t, runner, profile, filepath.Join(outside, "denied.txt"))
	})
	t.Run("protected metadata", func(t *testing.T) {
		assertWSLWriteDenied(t, runner, profile, filepath.Join(protected, "denied.txt"))
	})
	t.Run("network and host interop", func(t *testing.T) {
		result, err := runner.Run(context.Background(), ExecRequest{
			Argv: []string{
				"/bin/sh",
				"-c",
				`test -z "$WSL_INTEROP" && test ! -x /init && ! grep -q 'eth0:' /proc/net/dev && test -z "$(find /run -type s -print -quit)"`,
			},
		}, profile)
		if err != nil {
			t.Fatalf("WSL isolation check failed: %v: %s", err, result.Stderr)
		}
	})
}

func TestWindowsFullAccessDoesNotEnterWSL(t *testing.T) {
	backend := newPlatformSandbox()
	request := ExecRequest{Argv: []string{"cmd.exe", "/c", "echo ok"}}
	wrapped, err := backend.Wrap(request, Profile{FileSystem: FSFull, Network: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(wrapped.Argv) == 0 || wrapped.Argv[0] != "cmd.exe" {
		t.Fatalf("full access request was wrapped: %#v", wrapped.Argv)
	}
}

func wslShellWrite(path string) ExecRequest {
	return ExecRequest{
		Argv:     []string{"/bin/sh", "-c", `printf content > "$1"`, "sh", path},
		PathArgs: []int{4},
	}
}

func assertWSLWriteDenied(t *testing.T, runner Runner, profile Profile, path string) {
	t.Helper()
	result, err := runner.Run(context.Background(), wslShellWrite(path), profile)
	if err == nil {
		t.Fatalf("write unexpectedly succeeded: %s", result.Stdout)
	}
	if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("denied path exists or cannot be checked: %v", statErr)
	}
}
