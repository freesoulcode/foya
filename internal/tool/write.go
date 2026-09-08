package tool

import (
	"context"
	"encoding/json"
	"fmt"
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
	gw     interaction.Gateway
	runner sandbox.Runner
}

// NewWriteTool creates a tool that replaces a complete file.
func NewWriteTool(gw interaction.Gateway, runner sandbox.Runner) Tool {
	return &writeTool{gw: gw, runner: runner}
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

	path := absoluteToolPath(ctx, params.Path)
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
		Content:    []ContentPart{{Type: "text", Text: fmt.Sprintf("Wrote %d bytes to %s", len(params.Content), path)}},
		Diff:       UnifiedDiff(path, string(oldData), params.Content),
		FileChange: change,
	}, nil
}

func absoluteToolPath(ctx context.Context, path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	if wd := CWDFromContext(ctx); wd != "" {
		return filepath.Clean(filepath.Join(wd, path))
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	return absolute
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
