package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/freesoulcode/foya/internal/approval"
)

func TestDeleteMovesWorkspaceFileToTrashAndReturnsArtifact(t *testing.T) {
	workspace := t.TempDir()
	path := filepath.Join(workspace, "obsolete.txt")
	if err := os.WriteFile(path, []byte("obsolete\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "obsolete.txt")
	gateway := &recordingGateway{decision: approval.DecisionApproved}
	instance := &deleteTool{
		gw: gateway,
		trash: func(source string) error {
			return os.Rename(source, destination)
		},
	}

	result, err := instance.Run(
		WithCWD(context.Background(), workspace),
		Call{Input: []byte(`{"path":"obsolete.txt"}`)},
	)
	if err != nil || result.IsError {
		t.Fatalf("delete result = %#v, err = %v", result, err)
	}
	if gateway.request.Action != "delete" || gateway.request.Resource != path {
		t.Fatalf("approval request = %#v", gateway.request)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("source still exists: %v", err)
	}
	if _, err := os.Stat(destination); err != nil {
		t.Fatalf("trashed file missing: %v", err)
	}
	if !strings.Contains(result.Diff, "-obsolete") || result.FileChange != nil {
		t.Fatalf("delete artifact = %#v", result)
	}
	var output struct {
		Path string `json:"path"`
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal([]byte(result.Content[0].Text), &output); err != nil {
		t.Fatal(err)
	}
	if output.Path != path || output.Kind != "file" {
		t.Fatalf("delete output = %#v", output)
	}
}

func TestDeleteReportsDirectoryKind(t *testing.T) {
	workspace := t.TempDir()
	path := filepath.Join(workspace, "obsolete")
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}
	instance := &deleteTool{
		gw: allowGateway{},
		trash: func(string) error {
			return nil
		},
	}
	encoded, _ := json.Marshal(DeleteParams{Path: path})
	result, err := instance.Run(
		WithCWD(context.Background(), workspace),
		Call{Input: encoded},
	)
	if err != nil || result.IsError {
		t.Fatalf("delete directory result = %#v, err = %v", result, err)
	}
	var output struct {
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal([]byte(result.Content[0].Text), &output); err != nil {
		t.Fatal(err)
	}
	if output.Kind != "directory" {
		t.Fatalf("delete directory kind = %q", output.Kind)
	}
}

func TestDeleteRejectsUnsafePaths(t *testing.T) {
	workspace := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	protected := filepath.Join(workspace, ".git")
	if err := os.MkdirAll(protected, 0o755); err != nil {
		t.Fatal(err)
	}
	calls := 0
	instance := &deleteTool{
		gw: allowGateway{},
		trash: func(string) error {
			calls++
			return nil
		},
	}
	ctx := WithCWD(context.Background(), workspace)

	for _, path := range []string{workspace, outside, protected} {
		encoded, _ := json.Marshal(DeleteParams{Path: path})
		result, err := instance.Run(ctx, Call{Input: encoded})
		if err != nil || !result.IsError {
			t.Fatalf("unsafe delete %q = %#v, %v", path, result, err)
		}
	}
	if calls != 0 {
		t.Fatalf("trash called %d times for unsafe paths", calls)
	}
}

func TestDeleteFullAccessAllowsProtectedAndOutsidePaths(t *testing.T) {
	workspace := t.TempDir()
	protected := filepath.Join(workspace, ".git")
	if err := os.MkdirAll(protected, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	var trashed []string
	instance := &deleteTool{
		gw: allowGateway{},
		trash: func(path string) error {
			trashed = append(trashed, path)
			return nil
		},
	}
	ctx := approval.WithMode(
		WithCWD(context.Background(), workspace),
		approval.ModeFullAccess,
	)

	for _, path := range []string{protected, outside} {
		encoded, _ := json.Marshal(DeleteParams{Path: path})
		result, err := instance.Run(ctx, Call{Input: encoded})
		if err != nil || result.IsError {
			t.Fatalf("full-access delete %q = %#v, %v", path, result, err)
		}
	}
	if len(trashed) != 2 {
		t.Fatalf("trashed paths = %#v", trashed)
	}
}

func TestDeleteRejectsSymlinkedParentOutsideWorkspace(t *testing.T) {
	workspace := t.TempDir()
	outside := t.TempDir()
	target := filepath.Join(outside, "outside.txt")
	if err := os.WriteFile(target, []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(workspace, "linked")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	instance := &deleteTool{
		gw: allowGateway{},
		trash: func(string) error {
			t.Fatal("trash called for path through symlinked parent")
			return nil
		},
	}
	encoded, _ := json.Marshal(DeleteParams{Path: filepath.Join(link, "outside.txt")})
	result, err := instance.Run(
		WithCWD(context.Background(), workspace),
		Call{Input: encoded},
	)
	if err != nil || !result.IsError {
		t.Fatalf("symlinked delete result = %#v, %v", result, err)
	}
}

func quotedJSON(value string) string {
	data, _ := json.Marshal(value)
	return string(data)
}
