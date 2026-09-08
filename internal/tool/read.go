package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/freesoulcode/foya/internal/artifact"
	interaction "github.com/freesoulcode/foya/internal/interaction"
)

const maxReadLen = 50000

// ReadParams contains arguments for the read tool.
type ReadParams struct {
	Path   string `json:"path"`
	Offset int    `json:"offset,omitempty"`
	Limit  int    `json:"limit,omitempty"`
}

type readTool struct {
	gw interaction.Gateway
}

// NewReadTool creates a read-only tool allowed automatically in explore mode.
func NewReadTool(gw interaction.Gateway) Tool {
	return &readTool{gw: gw}
}

func (t *readTool) Name() string        { return "read" }
func (t *readTool) Exposure() Exposure  { return ExposureDirect }
func (t *readTool) Parallel() bool      { return true }
func (t *readTool) Description() string { return "Read a file from the filesystem." }

func (t *readTool) Spec() []byte {
	return []byte(`{
		"type": "object",
		"properties": {
			"path": {"type": "string", "description": "Path to the file to read"},
			"offset": {"type": "integer", "description": "Line offset to start from (0-based)", "default": 0},
			"limit": {"type": "integer", "description": "Maximum number of lines to read", "default": 2000}
		},
		"required": ["path"]
	}`)
}

func (t *readTool) Run(ctx context.Context, call Call) (Result, error) {
	var params ReadParams
	if err := json.Unmarshal(call.Input, &params); err != nil {
		return errResult("invalid arguments: " + err.Error()), nil
	}
	if strings.TrimSpace(params.Path) == "" {
		return errResult("path is required"), nil
	}

	// Read operations are allowed automatically in explore mode.
	decision, err := t.gw.Request(ctx, interaction.Request{
		ToolName: "read",
		Action:   "read",
		Detail:   params.Path,
	})
	if err != nil {
		return errResult("Approval interrupted: " + err.Error()), nil
	}
	if decision == interaction.DecisionDenied {
		return errResult("User denied file read"), nil
	}

	path := params.Path
	if !filepath.IsAbs(path) {
		if wd := CWDFromContext(ctx); wd != "" {
			path = filepath.Join(wd, path)
		}
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return errResult(fmt.Sprintf("Read failed: %v", err)), nil
	}
	mediaType := strings.Split(http.DetectContentType(data), ";")[0]
	if strings.HasPrefix(mediaType, "image/") {
		if int64(len(data)) > artifact.MaxImageBytes {
			return errResult(fmt.Sprintf("Image exceeds the %d-byte limit", artifact.MaxImageBytes)), nil
		}
		return Result{Content: []ContentPart{{
			Type:      "image",
			Name:      filepath.Base(path),
			MediaType: mediaType,
			Data:      data,
		}}}, nil
	}

	content := string(data)
	if params.Offset > 0 || params.Limit > 0 {
		lines := strings.Split(content, "\n")
		start := params.Offset
		if start < 0 {
			start = 0
		}
		if start >= len(lines) {
			return errResult("offset beyond file length"), nil
		}
		end := len(lines)
		if params.Limit > 0 && start+params.Limit < end {
			end = start + params.Limit
		}
		content = strings.Join(lines[start:end], "\n")
	}

	content = truncateToolOutput(content, truncationOptions{
		MaxLines:  defaultMaxOutputLines,
		MaxBytes:  maxReadLen,
		Direction: keepOutputHead,
	}).Content
	return Result{
		Content: []ContentPart{{Type: "text", Text: content}},
	}, nil
}
