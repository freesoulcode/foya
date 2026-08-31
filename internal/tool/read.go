package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/freesoulcode/foya/internal/approval"
	"github.com/freesoulcode/foya/internal/artifact"
)

const maxReadLen = 50000

// ReadParams 是 read 工具的参数。
type ReadParams struct {
	Path   string `json:"path"`
	Offset int    `json:"offset,omitempty"`
	Limit  int    `json:"limit,omitempty"`
}

type readTool struct {
	gw approval.Gateway
}

// NewReadTool 创建 read 工具(只读,explore 模式自动放行)。
func NewReadTool(gw approval.Gateway) Tool {
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

	// read 动作在 explore 模式下自动放行。
	decision, err := t.gw.Request(ctx, approval.Request{
		ToolName: "read",
		Action:   "read",
		Detail:   params.Path,
	})
	if err != nil {
		return errResult("审批中断: " + err.Error()), nil
	}
	if decision == approval.DecisionDenied {
		return errResult("用户拒绝读取文件"), nil
	}

	path := params.Path
	if !filepath.IsAbs(path) {
		if wd := CWDFromContext(ctx); wd != "" {
			path = filepath.Join(wd, path)
		}
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return errResult(fmt.Sprintf("读取失败: %v", err)), nil
	}
	mediaType := strings.Split(http.DetectContentType(data), ";")[0]
	if strings.HasPrefix(mediaType, "image/") {
		if int64(len(data)) > artifact.MaxImageBytes {
			return errResult(fmt.Sprintf("图片超过 %d 字节限制", artifact.MaxImageBytes)), nil
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
