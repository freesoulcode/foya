package tool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	interaction "github.com/freesoulcode/foya/internal/interaction"
)

const maxDeleteDiffBytes = 256 << 10

type DeleteParams struct {
	Path string `json:"path"`
}

type deleteTool struct {
	gw    interaction.Gateway
	trash func(string) error
}

func NewDeleteTool(gw interaction.Gateway) Tool {
	return &deleteTool{gw: gw, trash: moveToTrash}
}

func (t *deleteTool) Name() string       { return "delete" }
func (t *deleteTool) Exposure() Exposure { return ExposureDirect }
func (t *deleteTool) Description() string {
	return "Move one file or directory inside the workspace to the operating system Trash or Recycle Bin. Use this instead of rm or other permanent deletion commands."
}

func (t *deleteTool) Spec() []byte {
	return []byte(`{
		"type":"object",
		"properties":{
			"path":{"type":"string","description":"Workspace-relative or absolute path to move to the system Trash or Recycle Bin"}
		},
		"required":["path"],
		"additionalProperties":false
	}`)
}

func (t *deleteTool) Run(ctx context.Context, call Call) (Result, error) {
	var params DeleteParams
	if err := json.Unmarshal(call.Input, &params); err != nil {
		return errResult("invalid arguments: " + err.Error()), nil
	}
	path, info, err := validatedDeletePath(ctx, params.Path)
	if err != nil {
		return errResult(err.Error()), nil
	}
	decision, err := t.gw.Request(ctx, interaction.Request{
		ToolName: "delete",
		Action:   "delete",
		Detail:   "Move to Trash: " + path,
		Resource: path,
		Scope:    approvalPathScope(ctx, path),
	})
	if err != nil {
		return errResult("Approval interrupted: " + err.Error()), nil
	}
	if decision == interaction.DecisionDenied {
		return errResult("User denied moving the item to Trash"), nil
	}

	diff := deletionDiff(path, info)
	if err := t.trash(path); err != nil {
		return errResult("Move to Trash failed: " + err.Error()), nil
	}
	kind := "file"
	if info.IsDir() {
		kind = "directory"
	}
	output, _ := json.Marshal(struct {
		Operation string `json:"operation"`
		Path      string `json:"path"`
		Kind      string `json:"kind"`
	}{
		Operation: "trash",
		Path:      path,
		Kind:      kind,
	})
	return Result{
		Content: []ContentPart{{Type: "text", Text: string(output)}},
		Diff:    diff,
	}, nil
}

func validatedDeletePath(ctx context.Context, value string) (string, os.FileInfo, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil, errors.New("path is required")
	}
	workspace := CWDFromContext(ctx)
	fullAccess := interaction.ModeFromContext(ctx) == interaction.ModeFullAccess
	if workspace == "" && !fullAccess {
		return "", nil, errors.New("delete requires a workspace")
	}
	path := value
	if !filepath.IsAbs(path) {
		if workspace == "" {
			return "", nil, errors.New("relative delete path requires a workspace")
		}
		path = filepath.Join(workspace, path)
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return "", nil, fmt.Errorf("resolve delete path: %w", err)
	}
	path = filepath.Clean(path)
	if path == filepath.VolumeName(path)+string(filepath.Separator) {
		return "", nil, errors.New("cannot delete a filesystem root")
	}
	if !fullAccess {
		workspace, err = filepath.Abs(workspace)
		if err != nil {
			return "", nil, fmt.Errorf("resolve workspace: %w", err)
		}
		if path == workspace {
			return "", nil, errors.New("cannot delete the workspace root")
		}
		parent, err := filepath.EvalSymlinks(filepath.Dir(path))
		if err != nil {
			return "", nil, fmt.Errorf("resolve parent directory: %w", err)
		}
		resolvedWorkspace, err := filepath.EvalSymlinks(workspace)
		if err != nil {
			return "", nil, fmt.Errorf("resolve workspace: %w", err)
		}
		resolvedPath := filepath.Join(parent, filepath.Base(path))
		relative, err := filepath.Rel(resolvedWorkspace, resolvedPath)
		if err != nil || relative == ".." ||
			strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return "", nil, errors.New("delete path must stay inside the workspace")
		}
		for _, part := range strings.Split(relative, string(filepath.Separator)) {
			for _, protected := range []string{".git", ".agents", ".foya"} {
				if strings.EqualFold(part, protected) {
					return "", nil, fmt.Errorf("cannot delete protected agent metadata: %s", protected)
				}
			}
		}
	}
	info, err := os.Lstat(path)
	if err != nil {
		return "", nil, fmt.Errorf("inspect delete path: %w", err)
	}
	return path, info, nil
}

func deletionDiff(path string, info os.FileInfo) string {
	if !info.Mode().IsRegular() || info.Size() > maxDeleteDiffBytes {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil || !utf8.Valid(data) || strings.IndexByte(string(data), 0) >= 0 {
		return ""
	}
	return UnifiedDiff(path, string(data), "")
}
