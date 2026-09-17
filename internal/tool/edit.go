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

// EditOp describes one exact replacement.
type EditOp struct {
	OldText string `json:"old_text"`
	NewText string `json:"new_text"`
}

// EditParams contains arguments for the edit tool.
type EditParams struct {
	Path  string   `json:"path"`
	Edits []EditOp `json:"edits"`
}

type editTool struct {
	gw        interaction.Gateway
	runner    sandbox.Runner
	artifacts fileArtifactStore
}

// NewEditTool creates a tool for exact replacements in existing files.
// Each old_text must match exactly once or the edit is rejected.
func NewEditTool(gw interaction.Gateway, runner sandbox.Runner, artifacts ...fileArtifactStore) Tool {
	var store fileArtifactStore
	if len(artifacts) > 0 {
		store = artifacts[0]
	}
	return &editTool{gw: gw, runner: runner, artifacts: store}
}

func (t *editTool) Name() string       { return "edit" }
func (t *editTool) Exposure() Exposure { return ExposureDirect }
func (t *editTool) Description() string {
	return "Edit an existing file by replacing exact text. Each old_text must uniquely match."
}

func (t *editTool) Spec() []byte {
	return []byte(`{
		"type": "object",
		"properties": {
			"path": {"type": "string", "description": "Path to the file to edit"},
			"edits": {
				"type": "array",
				"description": "List of replacements to apply",
				"items": {
					"type": "object",
					"properties": {
						"old_text": {"type": "string", "description": "Exact text to find (must be unique)"},
						"new_text": {"type": "string", "description": "Replacement text"}
					},
					"required": ["old_text", "new_text"]
				}
			}
		},
		"required": ["path", "edits"]
	}`)
}

func (t *editTool) Run(ctx context.Context, call Call) (Result, error) {
	var params EditParams
	if err := json.Unmarshal(call.Input, &params); err != nil {
		return errResult("invalid arguments: " + err.Error()), nil
	}
	if strings.TrimSpace(params.Path) == "" {
		return errResult("path is required"), nil
	}
	if len(params.Edits) == 0 {
		return errResult("edits must not be empty"), nil
	}
	if CWDFromContext(ctx) == "" && !filepath.IsAbs(strings.TrimSpace(params.Path)) {
		return errResult("relative edit path requires a workspace"), nil
	}

	path := absoluteToolPath(ctx, strings.TrimSpace(params.Path))
	decision, err := t.gw.Request(ctx, interaction.Request{
		ToolName: "edit",
		Action:   "write",
		Detail:   fmt.Sprintf("Edit file: %s (%d replacements)", path, len(params.Edits)),
		Resource: path,
		Scope:    approvalPathScope(ctx, path),
	})
	if err != nil {
		return errResult("Approval interrupted: " + err.Error()), nil
	}
	if decision == interaction.DecisionDenied {
		return errResult("User denied file edit"), nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return errResult(fmt.Sprintf("Read failed: %v", err)), nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return errResult(fmt.Sprintf("Failed to read file metadata: %v", err)), nil
	}
	content := string(data)
	original := content

	var applied int
	for i, op := range params.Edits {
		if op.OldText == "" {
			return errResult(fmt.Sprintf("Edit %d: old_text cannot be empty", i+1)), nil
		}
		count := strings.Count(content, op.OldText)
		if count == 0 {
			return errResult(fmt.Sprintf("Edit %d: old_text was not found", i+1)), nil
		}
		if count > 1 {
			return errResult(fmt.Sprintf("Edit %d: old_text occurs %d times and must be unique; include more context", i+1, count)), nil
		}
		content = strings.Replace(content, op.OldText, op.NewText, 1)
		applied++
	}

	displayPath := path
	if managedWorkspacePath(ctx, path) {
		relativePath, _ := managedWorkspaceRelativePath(ctx, path)
		if relativePath != "" {
			displayPath = relativePath
		}
		ref, err := putArtifact(ctx, t.artifacts, params.Path, content)
		if err != nil {
			return errResult(fmt.Sprintf("Write failed: artifact snapshot failed: %v", err)), nil
		}
		if err := writeFileAtBoundary(ctx, t.runner, path, []byte(content)); err != nil {
			deleteArtifactSnapshot(context.WithoutCancel(ctx), t.artifacts, ref.ID)
			return errResult(fmt.Sprintf("Write failed: %v", err)), nil
		}
		return Result{Content: []ContentPart{
			{Type: "text", Text: fmt.Sprintf("Applied %d replacements to %s", applied, displayPath)},
			{Type: "artifact_ref", Attachment: &ref},
		}}, nil
	}

	if err := writeFileAtBoundary(ctx, t.runner, path, []byte(content)); err != nil {
		return errResult(fmt.Sprintf("Write failed: %v", err)), nil
	}

	var change *conversation.FileChange
	if original != content {
		change = trackedFileChange(path, data, true, info.Mode(), []byte(content), info.Mode())
	}
	return Result{
		Content:    []ContentPart{{Type: "text", Text: fmt.Sprintf("Applied %d replacements to %s", applied, displayPath)}},
		Diff:       UnifiedDiff(displayPath, original, content),
		FileChange: change,
	}, nil
}
