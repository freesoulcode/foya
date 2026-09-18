package kernel

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	conversation "github.com/freesoulcode/foya/internal/conversation"
	"github.com/freesoulcode/foya/internal/project"
	"github.com/freesoulcode/foya/internal/terminal"
)

type workspaceTerminalRecorder struct {
	cwd string
}

func (r *workspaceTerminalRecorder) Start(
	_ context.Context,
	sessionID, cwd string,
	_, _ uint16,
) (terminal.Snapshot, error) {
	r.cwd = cwd
	return terminal.Snapshot{Ref: "terminal-1", SessionID: sessionID, Running: true}, nil
}

func (*workspaceTerminalRecorder) Attach(string, string) (terminal.Snapshot, error) {
	return terminal.Snapshot{}, terminal.ErrNotFound
}

func (*workspaceTerminalRecorder) Write(string, string, string) error {
	return terminal.ErrNotFound
}

func (*workspaceTerminalRecorder) Resize(string, string, uint16, uint16) error {
	return terminal.ErrNotFound
}

func (*workspaceTerminalRecorder) Stop(string, string) error {
	return terminal.ErrNotFound
}

func (*workspaceTerminalRecorder) Subscribe(
	context.Context,
	string,
	string,
	uint64,
) (<-chan terminal.DataEvent, error) {
	return nil, terminal.ErrNotFound
}

func (*workspaceTerminalRecorder) CloseSession(string) {}

func TestManagedSessionWorkspaceBacksFilesAndTerminal(t *testing.T) {
	ctx := context.Background()
	service, sessionID, _ := newQueueTestBackend(t)

	workspace, err := service.SessionWorkspace(ctx, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if workspace.Kind != WorkspaceKindManaged || workspace.ProjectID != "" {
		t.Fatalf("managed workspace = %#v", workspace)
	}
	if workspace.Name != "Workspace" {
		t.Fatalf("managed workspace name = %q", workspace.Name)
	}
	if info, err := os.Stat(workspace.Path); err != nil || !info.IsDir() {
		t.Fatalf("managed workspace path = %q, info=%#v err=%v", workspace.Path, info, err)
	}

	recorder := &workspaceTerminalRecorder{}
	service.terminal = recorder
	if _, err := service.StartTerminal(ctx, sessionID, 80, 24); err != nil {
		t.Fatal(err)
	}
	if recorder.cwd != workspace.Path {
		t.Fatalf("terminal cwd = %q, want %q", recorder.cwd, workspace.Path)
	}
}

func TestProjectSessionUsesProjectAsWorkspace(t *testing.T) {
	ctx := context.Background()
	service, _, _ := newQueueTestBackend(t)
	manager, err := project.NewManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	service.SetProjectManager(manager)

	root := filepath.Join(t.TempDir(), "example")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	item, err := service.RegisterProject(root, "Example")
	if err != nil {
		t.Fatal(err)
	}
	session, err := service.CreateSession(conversation.CreateOptions{
		ConnectionID: "test-connection",
		Model:        "test-model",
		ProjectID:    item.ID,
		ApprovalMode: "manual",
	})
	if err != nil {
		t.Fatal(err)
	}

	workspace, err := service.SessionWorkspace(ctx, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if workspace.Kind != WorkspaceKindProject ||
		workspace.ProjectID != item.ID ||
		workspace.Name != item.Name ||
		workspace.Path != item.Path {
		t.Fatalf("project workspace = %#v, project = %#v", workspace, item)
	}
	created, err := service.CreateWorkspaceEntry(ctx, session.ID, "project.txt", false)
	if err != nil {
		t.Fatal(err)
	}
	if created != "project.txt" {
		t.Fatalf("created project workspace file = %q", created)
	}
	if _, err := os.Stat(filepath.Join(root, created)); err != nil {
		t.Fatalf("project workspace file missing: %v", err)
	}
}

func TestManagedWorkspaceFileLifecycle(t *testing.T) {
	ctx := context.Background()
	service, sessionID, _ := newQueueTestBackend(t)

	directory, err := service.CreateWorkspaceEntry(ctx, sessionID, "notes", true)
	if err != nil {
		t.Fatal(err)
	}
	if directory != "notes" {
		t.Fatalf("created directory = %q", directory)
	}
	file, err := service.CreateWorkspaceEntry(ctx, sessionID, "notes/todo.md", false)
	if err != nil {
		t.Fatal(err)
	}
	if file != "notes/todo.md" {
		t.Fatalf("created file = %q", file)
	}
	path, err := service.ResolveWorkspacePath(ctx, sessionID, file)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("# Todo"), 0o600); err != nil {
		t.Fatal(err)
	}

	entries, err := service.ListWorkspaceFiles(ctx, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 ||
		entries[0] != (WorkspaceEntry{Path: "notes", Name: "notes", IsDirectory: true}) ||
		entries[1] != (WorkspaceEntry{Path: "notes/todo.md", Name: "todo.md"}) {
		t.Fatalf("workspace entries = %#v", entries)
	}
	content, err := service.ReadWorkspaceFile(ctx, sessionID, file)
	if err != nil {
		t.Fatal(err)
	}
	if content != "# Todo" {
		t.Fatalf("workspace file content = %q", content)
	}

	renamed, err := service.RenameWorkspaceEntry(ctx, sessionID, file, "done.md")
	if err != nil {
		t.Fatal(err)
	}
	if renamed != "notes/done.md" {
		t.Fatalf("renamed workspace file = %q", renamed)
	}
	renamedPath, err := service.ResolveWorkspacePath(ctx, sessionID, renamed)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.DeleteWorkspaceEntry(ctx, sessionID, "notes"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(renamedPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("deleted workspace file stat error = %v", err)
	}
}

func TestReadWorkspaceMediaFile(t *testing.T) {
	ctx := context.Background()
	service, sessionID, _ := newQueueTestBackend(t)
	imagePath, err := service.CreateWorkspaceEntry(ctx, sessionID, "preview.png", false)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := service.ResolveWorkspacePath(ctx, sessionID, imagePath)
	if err != nil {
		t.Fatal(err)
	}
	imageData := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}
	if err := os.WriteFile(resolved, imageData, 0o600); err != nil {
		t.Fatal(err)
	}

	data, mediaType, err := service.ReadWorkspaceMediaFile(ctx, sessionID, imagePath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, imageData) || mediaType != "image/png" {
		t.Fatalf("media preview = %q, %q", data, mediaType)
	}

	textPath, err := service.CreateWorkspaceEntry(ctx, sessionID, "notes.txt", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.ReadWorkspaceMediaFile(ctx, sessionID, textPath); !errors.Is(err, ErrUnsupportedMediaFile) {
		t.Fatalf("text media preview error = %v", err)
	}
}

func TestWorkspaceFilesRejectPathEscape(t *testing.T) {
	ctx := context.Background()
	service, sessionID, _ := newQueueTestBackend(t)

	if _, err := service.CreateWorkspaceEntry(ctx, sessionID, "../escape.txt", false); !errors.Is(err, ErrInvalidWorkspacePath) {
		t.Fatalf("create path escape error = %v", err)
	}
	if _, err := service.ResolveWorkspacePath(ctx, sessionID, "../escape.txt"); !errors.Is(err, ErrInvalidWorkspacePath) {
		t.Fatalf("resolve path escape error = %v", err)
	}
}
