//go:build darwin

package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	interaction "github.com/freesoulcode/foya/internal/interaction"
	"github.com/freesoulcode/foya/internal/sandbox"
)

func TestToolsUseMacOSSandboxBoundary(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	project, err := os.MkdirTemp(home, ".foya-tool-project-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(project)
	outside, err := os.MkdirTemp(home, ".foya-tool-outside-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(outside)
	if err := os.Mkdir(filepath.Join(project, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	ctx := interaction.WithMode(WithCWD(context.Background(), project), interaction.ModeManual)
	runner := sandbox.NewRunner()

	t.Run("bash writes project", func(t *testing.T) {
		target := filepath.Join(project, "bash.txt")
		result, err := NewBashTool(allowGateway{}, runner).Run(ctx, Call{
			Input: bashCallInput(t, "printf content > "+strconv.Quote(target)),
		})
		if err != nil || result.IsError {
			t.Fatalf("result = %#v, err = %v", result, err)
		}
	})
	t.Run("bash rejects outside", func(t *testing.T) {
		target := filepath.Join(outside, "bash.txt")
		result, err := NewBashTool(allowGateway{}, runner).Run(ctx, Call{
			Input: bashCallInput(t, "printf content > "+strconv.Quote(target)),
		})
		if err != nil {
			t.Fatal(err)
		}
		if !result.IsError {
			t.Fatalf("sandbox violation reported success: %#v", result)
		}
		assertMissing(t, target)
	})
	t.Run("write rejects protected metadata", func(t *testing.T) {
		target := filepath.Join(project, ".git", "config")
		result, err := NewWriteTool(allowGateway{}, runner).Run(ctx, Call{
			Input: []byte(`{"path":".git/config","content":"denied"}`),
		})
		if err != nil {
			t.Fatal(err)
		}
		if !result.IsError {
			t.Fatalf("sandbox violation reported success: %#v", result)
		}
		assertMissing(t, target)
	})
	t.Run("edit persists through boundary", func(t *testing.T) {
		target := filepath.Join(project, "edit.txt")
		if err := os.WriteFile(target, []byte("before"), 0o644); err != nil {
			t.Fatal(err)
		}
		result, err := NewEditTool(allowGateway{}, runner).Run(ctx, Call{
			Input: []byte(`{"path":"edit.txt","edits":[{"old_text":"before","new_text":"after"}]}`),
		})
		if err != nil || result.IsError {
			t.Fatalf("result = %#v, err = %v", result, err)
		}
		data, err := os.ReadFile(target)
		if err != nil || string(data) != "after" {
			t.Fatalf("file = %q, err = %v", data, err)
		}
	})
}

func assertMissing(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("path %q exists or cannot be checked: %v", path, err)
	}
}

func bashCallInput(t *testing.T, command string) []byte {
	t.Helper()
	input, err := json.Marshal(BashParams{Command: command})
	if err != nil {
		t.Fatal(err)
	}
	return input
}
