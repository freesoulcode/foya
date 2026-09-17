package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	conversation "github.com/freesoulcode/foya/internal/conversation"
	interaction "github.com/freesoulcode/foya/internal/interaction"
	"github.com/freesoulcode/foya/internal/sandbox"
)

// WriteParams contains arguments for the write tool.
type WriteParams struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

type writeTool struct {
	gw        interaction.Gateway
	runner    sandbox.Runner
	artifacts fileArtifactStore
}

type fileArtifactStore interface {
	PutFile(ctx context.Context, sessionID, name, mediaType string, src io.Reader) (conversation.AttachmentRef, error)
	Delete(ctx context.Context, sessionID, artifactID string) error
}

// NewWriteTool creates a tool that replaces a complete file.
func NewWriteTool(gw interaction.Gateway, runner sandbox.Runner, artifacts ...fileArtifactStore) Tool {
	var store fileArtifactStore
	if len(artifacts) > 0 {
		store = artifacts[0]
	}
	return &writeTool{gw: gw, runner: runner, artifacts: store}
}

func (t *writeTool) Name() string       { return "write" }
func (t *writeTool) Exposure() Exposure { return ExposureDirect }
func (t *writeTool) Description() string {
	return "Write content to a file (creates or overwrites). Use for creating new files."
}

func (t *writeTool) Spec() []byte {
	return []byte(`{
		"type": "object",
		"properties": {
			"path": {"type": "string", "description": "Path to the file to write"},
			"content": {"type": "string", "description": "Full content to write to the file"}
		},
		"required": ["path", "content"]
	}`)
}

func (t *writeTool) Run(ctx context.Context, call Call) (Result, error) {
	var params WriteParams
	if err := json.Unmarshal(call.Input, &params); err != nil {
		return errResult("invalid arguments: " + err.Error()), nil
	}
	if strings.TrimSpace(params.Path) == "" {
		return errResult("path is required"), nil
	}
	requestedPath := strings.TrimSpace(params.Path)
	if CWDFromContext(ctx) == "" && !filepath.IsAbs(requestedPath) {
		return t.writeArtifact(ctx, requestedPath, params.Content)
	}

	path := absoluteToolPath(ctx, requestedPath)
	decision, err := t.gw.Request(ctx, interaction.Request{
		ToolName: "write",
		Action:   "write",
		Detail:   fmt.Sprintf("Write file: %s", path),
		Resource: path,
		Scope:    approvalPathScope(ctx, path),
	})
	if err != nil {
		return errResult("Approval interrupted: " + err.Error()), nil
	}
	if decision == interaction.DecisionDenied {
		return errResult("User denied file write"), nil
	}

	// Read previous content for the diff and reversible change record.
	oldData, readErr := os.ReadFile(path)
	beforeExists := readErr == nil
	if readErr != nil && !os.IsNotExist(readErr) {
		return errResult(fmt.Sprintf("Failed to read original file: %v", readErr)), nil
	}
	var beforeMode os.FileMode
	if beforeExists {
		info, err := os.Stat(path)
		if err != nil {
			return errResult(fmt.Sprintf("Failed to read original file metadata: %v", err)), nil
		}
		beforeMode = info.Mode()
	}

	displayPath := path
	if managedWorkspacePath(ctx, path) {
		relativePath, _ := managedWorkspaceRelativePath(ctx, path)
		if relativePath != "" {
			displayPath = relativePath
		}
		ref, err := putArtifact(ctx, t.artifacts, requestedPath, params.Content)
		if err != nil {
			return errResult(fmt.Sprintf("Write failed: artifact snapshot failed: %v", err)), nil
		}
		if err := writeFileAtBoundary(ctx, t.runner, path, []byte(params.Content)); err != nil {
			deleteArtifactSnapshot(context.WithoutCancel(ctx), t.artifacts, ref.ID)
			return errResult(fmt.Sprintf("Write failed: %v", err)), nil
		}
		return Result{Content: []ContentPart{
			{Type: "text", Text: fmt.Sprintf("Wrote %d bytes to %s", len(params.Content), displayPath)},
			{Type: "artifact_ref", Attachment: &ref},
		}}, nil
	}

	if err := writeFileAtBoundary(ctx, t.runner, path, []byte(params.Content)); err != nil {
		return errResult(fmt.Sprintf("Write failed: %v", err)), nil
	}
	afterMode := beforeMode
	if !beforeExists {
		afterMode = 0o644
	}

	var change *conversation.FileChange
	if !beforeExists || string(oldData) != params.Content {
		change = trackedFileChange(
			path,
			oldData,
			beforeExists,
			beforeMode,
			[]byte(params.Content),
			afterMode,
		)
	}
	return Result{
		Content:    []ContentPart{{Type: "text", Text: fmt.Sprintf("Wrote %d bytes to %s", len(params.Content), displayPath)}},
		Diff:       UnifiedDiff(displayPath, string(oldData), params.Content),
		FileChange: change,
	}, nil
}

func (t *writeTool) writeArtifact(ctx context.Context, requestedPath, content string) (Result, error) {
	sessionID := SessionIDFromContext(ctx)
	if sessionID == "" {
		return errResult("relative write path requires a workspace or session"), nil
	}
	if t.artifacts == nil {
		return errResult("artifact store is unavailable"), nil
	}
	name := artifactFileName(requestedPath)
	decision, err := t.gw.Request(ctx, interaction.Request{
		ToolName: "write",
		Action:   "write",
		Detail:   fmt.Sprintf("Create generated file: %s", name),
		Resource: "artifact:" + requestedPath,
		Scope:    "session:" + sessionID,
	})
	if err != nil {
		return errResult("Approval interrupted: " + err.Error()), nil
	}
	if decision == interaction.DecisionDenied {
		return errResult("User denied file write"), nil
	}
	ref, err := putArtifact(ctx, t.artifacts, requestedPath, content)
	if err != nil {
		return errResult(fmt.Sprintf("Write failed: %v", err)), nil
	}
	output, _ := json.Marshal(struct {
		Operation  string `json:"operation"`
		Path       string `json:"path"`
		ArtifactID string `json:"artifact_id"`
		Name       string `json:"name"`
		MediaType  string `json:"media_type"`
		Bytes      int64  `json:"bytes"`
	}{
		Operation:  "artifact_write",
		Path:       requestedPath,
		ArtifactID: ref.ID,
		Name:       ref.Name,
		MediaType:  ref.MediaType,
		Bytes:      ref.Bytes,
	})
	return Result{
		Content: []ContentPart{
			{Type: "text", Text: string(output)},
			{Type: "artifact_ref", Attachment: &ref},
		},
	}, nil
}

func putArtifact(ctx context.Context, artifacts fileArtifactStore, requestedPath, content string) (conversation.AttachmentRef, error) {
	if artifacts == nil {
		return conversation.AttachmentRef{}, fmt.Errorf("artifact store is unavailable")
	}
	sessionID := SessionIDFromContext(ctx)
	if sessionID == "" {
		return conversation.AttachmentRef{}, fmt.Errorf("session is unavailable")
	}
	name := artifactFileName(requestedPath)
	return artifacts.PutFile(ctx, sessionID, name, "", strings.NewReader(content))
}

func deleteArtifactSnapshot(ctx context.Context, artifacts fileArtifactStore, artifactID string) {
	sessionID := SessionIDFromContext(ctx)
	if artifacts == nil || sessionID == "" || artifactID == "" {
		return
	}
	_ = artifacts.Delete(ctx, sessionID, artifactID)
}

func managedWorkspacePath(ctx context.Context, target string) bool {
	_, ok := managedWorkspaceRelativePath(ctx, target)
	return ok
}

func managedWorkspaceRelativePath(ctx context.Context, target string) (string, bool) {
	if !ManagedWorkspaceFromContext(ctx) {
		return "", false
	}
	workspace := CWDFromContext(ctx)
	if workspace == "" {
		return "", false
	}
	relative, err := filepath.Rel(filepath.Clean(workspace), filepath.Clean(target))
	if err != nil || relative == "." || relative == ".." ||
		strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", false
	}
	return filepath.Clean(relative), true
}

func absoluteToolPath(ctx context.Context, path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	if wd := CWDFromContext(ctx); wd != "" {
		return filepath.Clean(filepath.Join(wd, path))
	}
	return filepath.Clean(path)
}

func approvalPathScope(ctx context.Context, path string) string {
	workspace := CWDFromContext(ctx)
	if workspace == "" {
		return path
	}
	workspace = filepath.Clean(workspace)
	relative, err := filepath.Rel(workspace, path)
	if err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return workspace
	}
	return path
}

func artifactFileName(value string) string {
	value = strings.TrimSpace(strings.ReplaceAll(value, "\\", "/"))
	name := filepath.Base(filepath.Clean(value))
	if name == "." || name == string(filepath.Separator) || name == "" {
		return "generated"
	}
	return name
}
