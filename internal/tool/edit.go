package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/freesoulcode/foya/internal/approval"
)

// EditOp 是一处精确替换。
type EditOp struct {
	OldText string `json:"old_text"`
	NewText string `json:"new_text"`
}

// EditParams 是 edit 工具的参数。
type EditParams struct {
	Path  string   `json:"path"`
	Edits []EditOp `json:"edits"`
}

type editTool struct {
	gw approval.Gateway
}

// NewEditTool 创建 edit 工具:对已有文件做精确文本替换。
// 每处 old_text 必须在文件中唯一匹配(出现且仅出现一次),否则拒绝并提示。
func NewEditTool(gw approval.Gateway) Tool {
	return &editTool{gw: gw}
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

	decision, err := t.gw.Request(ctx, approval.Request{
		ToolName: "edit",
		Action:   "write",
		Detail:   fmt.Sprintf("编辑文件: %s (%d 处替换)", params.Path, len(params.Edits)),
	})
	if err != nil {
		return errResult("审批中断: " + err.Error()), nil
	}
	if decision == approval.DecisionDenied {
		return errResult("用户拒绝编辑文件"), nil
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
	content := string(data)
	original := content

	var applied int
	for i, op := range params.Edits {
		if op.OldText == "" {
			return errResult(fmt.Sprintf("第 %d 处编辑: old_text 不能为空", i+1)), nil
		}
		count := strings.Count(content, op.OldText)
		if count == 0 {
			return errResult(fmt.Sprintf("第 %d 处编辑: old_text 在文件中未找到", i+1)), nil
		}
		if count > 1 {
			return errResult(fmt.Sprintf("第 %d 处编辑: old_text 在文件中出现 %d 次,必须唯一;请补充更多上下文", i+1, count)), nil
		}
		content = strings.Replace(content, op.OldText, op.NewText, 1)
		applied++
	}

	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return errResult(fmt.Sprintf("写入失败: %v", err)), nil
	}

	return Result{
		Content: []ContentPart{{Type: "text", Text: fmt.Sprintf("已对 %s 应用 %d 处替换", path, applied)}},
		Diff:    unifiedDiff(path, original, content),
	}, nil
}
