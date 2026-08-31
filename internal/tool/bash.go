package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime"
	"strings"
	"time"

	"github.com/freesoulcode/foya/internal/approval"
	"github.com/freesoulcode/foya/internal/sandbox"
)

const maxOutputLen = 30000

// BashParams 是 bash 工具的参数。
type BashParams struct {
	Command string `json:"command"`
}

// bashTool 执行 shell 命令。
type bashTool struct {
	gw        approval.Gateway
	runner    sandbox.Runner
	shell     string
	shellFlag string
}

// NewBashTool 创建 bash 工具。
func NewBashTool(gw approval.Gateway, runner sandbox.Runner) Tool {
	shell, flag := defaultShell()
	return &bashTool{gw: gw, runner: runner, shell: shell, shellFlag: flag}
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

	workDir := CWDFromContext(ctx)
	decision, err := t.gw.Request(ctx, approval.Request{
		ToolName: "bash",
		Action:   "execute",
		Detail:   params.Command,
		Resource: params.Command,
		Scope:    workDir,
	})
	if err != nil {
		return errResult("审批中断: " + err.Error()), nil
	}
	if decision == approval.DecisionDenied {
		return errResult("用户拒绝执行命令"), nil
	}

	cmdCtx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()

	if t.runner == nil {
		return errResult("execution boundary is unavailable"), nil
	}
	result, runErr := t.runner.Run(cmdCtx, sandbox.ExecRequest{
		Argv: []string{t.shell, t.shellFlag, params.Command},
		Dir:  workDir,
	}, executionProfile(ctx))
	output := string(result.Stdout)
	if len(result.Stderr) > 0 {
		if output != "" {
			output += "\n"
		}
		output += string(result.Stderr)
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
		IsError: runErr != nil,
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
