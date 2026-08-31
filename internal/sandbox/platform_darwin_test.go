//go:build darwin

package sandbox

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestSeatbeltWorkspaceWritePolicy(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	project, err := os.MkdirTemp(home, ".foya-sandbox-project-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(project)
	outside, err := os.MkdirTemp(home, ".foya-sandbox-outside-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(outside)

	protected := filepath.Join(project, ".git")
	if err := os.Mkdir(protected, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(project, "escape")); err != nil {
		t.Fatal(err)
	}

	runner := NewRunner()
	profile := WorkspaceWriteProfile(project)
	t.Run("project write", func(t *testing.T) {
		target := filepath.Join(project, "nested", "allowed.txt")
		result, err := runner.Run(context.Background(), shellWrite(target), profile)
		if err != nil {
			t.Fatalf("write failed: %v: %s", err, result.Stderr)
		}
		data, err := os.ReadFile(target)
		if err != nil || string(data) != "content" {
			t.Fatalf("file = %q, err = %v", data, err)
		}
	})
	t.Run("outside write", func(t *testing.T) {
		assertWriteDenied(t, runner, profile, filepath.Join(outside, "denied.txt"))
	})
	t.Run("protected metadata", func(t *testing.T) {
		assertWriteDenied(t, runner, profile, filepath.Join(protected, "denied.txt"))
	})
	t.Run("missing protected metadata", func(t *testing.T) {
		assertWriteDenied(t, runner, profile, filepath.Join(project, ".agents", "denied.txt"))
	})
	t.Run("protected root mutation", func(t *testing.T) {
		result, err := runner.Run(context.Background(), ExecRequest{
			Argv: []string{"/bin/rmdir", protected},
		}, profile)
		if err == nil {
			t.Fatalf("protected root removal unexpectedly succeeded: %s", result.Stdout)
		}
		if info, statErr := os.Stat(protected); statErr != nil || !info.IsDir() {
			t.Fatalf("protected root was modified: info = %#v, err = %v", info, statErr)
		}
	})
	t.Run("symlink escape", func(t *testing.T) {
		assertWriteDenied(t, runner, profile, filepath.Join(project, "escape", "denied.txt"))
	})
}

func TestSeatbeltReadOnlyPolicy(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	root, err := os.MkdirTemp(home, ".foya-sandbox-readonly-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)

	assertWriteDenied(
		t,
		NewRunner(),
		Profile{FileSystem: FSReadOnly},
		filepath.Join(root, "denied.txt"),
	)
}

func TestSeatbeltBlocksNetwork(t *testing.T) {
	if os.Getenv("FOYA_SANDBOX_NETWORK_HELPER") == "1" {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if listener != nil {
			_ = listener.Close()
		}
		if !errors.Is(err, syscall.EPERM) && !errors.Is(err, syscall.EACCES) {
			t.Fatalf("listen error = %v, want permission denied", err)
		}
		return
	}

	result, err := NewRunner().Run(context.Background(), ExecRequest{
		Argv: []string{os.Args[0], "-test.run=^TestSeatbeltBlocksNetwork$"},
		Env: append(
			os.Environ(),
			"FOYA_SANDBOX_NETWORK_HELPER=1",
		),
	}, Profile{FileSystem: FSReadOnly})
	if err != nil {
		t.Fatalf("network helper failed: %v: %s", err, result.Stderr)
	}
}

func TestSeatbeltFullAccessDoesNotWrap(t *testing.T) {
	request := ExecRequest{Argv: []string{"/bin/echo", "ok"}}
	wrapped, err := (seatbeltSandbox{}).Wrap(request, Profile{FileSystem: FSFull, Network: true})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(wrapped.Argv, "\x00") != strings.Join(request.Argv, "\x00") {
		t.Fatalf("full access argv = %#v", wrapped.Argv)
	}
}

func shellWrite(path string) ExecRequest {
	return ExecRequest{
		Argv: []string{"/bin/sh", "-c", `/bin/mkdir -p -- "$(dirname "$1")"; printf content > "$1"`, "sh", path},
	}
}

func assertWriteDenied(t *testing.T, runner Runner, profile Profile, path string) {
	t.Helper()
	result, err := runner.Run(context.Background(), shellWrite(path), profile)
	if err == nil {
		t.Fatalf("write unexpectedly succeeded: %s", result.Stdout)
	}
	if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("denied path exists or cannot be checked: %v", statErr)
	}
}
