package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/freesoulcode/foya/internal/approval"
)

const maxOutputLen = 30000

// BashParams 是 bash 工具的参数。
type BashParams struct {
	Command string `json:"command"`
}

// bashTool 执行 shell 命令。
type bashTool struct {
	gw        approval.Gateway
	shell     string
	shellFlag string
}

// NewBashTool 创建 bash 工具。
func NewBashTool(gw approval.Gateway) Tool {
	shell, flag := defaultShell()
	return &bashTool{gw: gw, shell: shell, shellFlag: flag}
}

func (t *bashTool) Name() string        { return "bash" }
func (t *bashTool) Exposure() Exposure  { return ExposureDirect }
func (t *bashTool) Description() string { return "Execute a shell command and return its output." }

func (t *bashTool) Spec() []byte {
	return []byte(`{
		"type": "object",
		"properties": {
			"command": {
				"type": "string",
				"description": "The shell command to execute"
			}
		},
		"required": ["command"]
	}`)
}

func (t *bashTool) Run(ctx context.Context, call Call) (Result, error) {
	var params BashParams
	if err := json.Unmarshal(call.Input, &params); err != nil {
		return errResult("invalid arguments: " + err.Error()), nil
	}
	if strings.TrimSpace(params.Command) == "" {
		return errResult("command is empty"), nil
	}

	decision, err := t.gw.Request(ctx, approval.Request{
		ToolName: "bash",
		Action:   "execute",
		Detail:   params.Command,
	})
	if err != nil {
		return errResult("审批中断: " + err.Error()), nil
	}
	if decision == approval.DecisionDenied {
		return errResult("用户拒绝执行命令"), nil
	}

	workDir := WorkspaceFromContext(ctx)
	cmdCtx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()

	cmd := exec.CommandContext(cmdCtx, t.shell, t.shellFlag, params.Command)
	if workDir != "" {
		cmd.Dir = workDir
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	runErr := cmd.Run()
	output := stdout.String()
	if stderr.Len() > 0 {
		if output != "" {
			output += "\n"
		}
		output += stderr.String()
	}
	if runErr != nil {
		if output == "" {
			output = runErr.Error()
		} else {
			output += "\n" + runErr.Error()
		}
	}
	output = truncate(output, maxOutputLen)
	return Result{
		Content: []ContentPart{{Type: "text", Text: output}},
	}, nil
}

func defaultShell() (shell, flag string) {
	if runtime.GOOS == "windows" {
		return "cmd", "/c"
	}
	return "/bin/sh", "-c"
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + fmt.Sprintf("\n... (output truncated, %d bytes total)", len(s))
}

func errResult(msg string) Result {
	return Result{IsError: true, Content: []ContentPart{{Type: "text", Text: msg}}}
}
