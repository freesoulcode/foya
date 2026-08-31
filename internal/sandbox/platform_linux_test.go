//go:build linux

package sandbox

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestLinuxBubblewrapWorkspaceWritePolicy(t *testing.T) {
	if _, err := os.Stat(bubblewrapExecutable); err != nil {
		t.Skipf("%s is not installed", bubblewrapExecutable)
	}

	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	project, err := os.MkdirTemp(home, ".foya-bwrap-project-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(project)
	outside, err := os.MkdirTemp(home, ".foya-bwrap-outside-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(outside)
	protected := filepath.Join(project, ".git")
	if err := os.Mkdir(protected, 0o755); err != nil {
		t.Fatal(err)
	}

	runner := NewRunner()
	profile := WorkspaceWriteProfile(project)
	probe, err := runner.Run(context.Background(), ExecRequest{
		Argv: []string{"/bin/true"},
	}, profile)
	if err != nil {
		t.Skipf("bubblewrap cannot run in this environment: %v: %s", err, probe.Stderr)
	}

	t.Run("project write", func(t *testing.T) {
		target := filepath.Join(project, "allowed.txt")
		result, err := runner.Run(context.Background(), linuxShellWrite(target), profile)
		if err != nil {
			t.Fatalf("write failed: %v: %s", err, result.Stderr)
		}
	})
	t.Run("outside write", func(t *testing.T) {
		assertLinuxWriteDenied(t, runner, profile, filepath.Join(outside, "denied.txt"))
	})
	t.Run("protected metadata", func(t *testing.T) {
		assertLinuxWriteDenied(t, runner, profile, filepath.Join(protected, "denied.txt"))
	})
	t.Run("network", func(t *testing.T) {
		result, err := runner.Run(context.Background(), ExecRequest{
			Argv: []string{"/bin/sh", "-c", "! grep -q 'eth0:' /proc/net/dev"},
		}, profile)
		if err != nil {
			t.Fatalf("network namespace is not isolated: %v: %s", err, result.Stderr)
		}
	})
}

func linuxShellWrite(path string) ExecRequest {
	return ExecRequest{
		Argv: []string{"/bin/sh", "-c", `printf content > "$1"`, "sh", path},
	}
}

func assertLinuxWriteDenied(t *testing.T, runner Runner, profile Profile, path string) {
	t.Helper()
	result, err := runner.Run(context.Background(), linuxShellWrite(path), profile)
	if err == nil {
		t.Fatalf("write unexpectedly succeeded: %s", result.Stdout)
	}
	if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("denied path exists or cannot be checked: %v", statErr)
	}
}
