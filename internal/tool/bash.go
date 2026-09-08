package tool

import (
	"context"
	"encoding/json"
	"runtime"
	"strings"
	"time"

	interaction "github.com/freesoulcode/foya/internal/interaction"
	"github.com/freesoulcode/foya/internal/sandbox"
)

const maxOutputLen = 30000

// BashParams contains arguments for the bash tool.
type BashParams struct {
	Command        string `json:"command"`
	Background     bool   `json:"background,omitempty"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty"`
}

// bashTool executes shell commands.
type bashTool struct {
	gw         interaction.Gateway
	background BackgroundCommandManager
	runner     sandbox.Runner
	shell      string
	shellFlag  string
}

// NewBashTool creates a shell command tool.
func NewBashTool(gw interaction.Gateway, runner sandbox.Runner) Tool {
	var background BackgroundCommandManager
	if _, ok := runner.(sandbox.ManagedRunner); ok {
		background = NewBackgroundCommandManager(runner)
	}
	return NewBashToolWithManager(gw, runner, background)
}

func NewBashToolWithManager(
	gw interaction.Gateway,
	runner sandbox.Runner,
	background BackgroundCommandManager,
) Tool {
	shell, flag := defaultShell()
	return &bashTool{
		gw: gw, runner: runner, background: background,
		shell: shell, shellFlag: flag,
	}
}

func (t *bashTool) Name() string       { return "bash" }
func (t *bashTool) Exposure() Exposure { return ExposureDirect }
func (t *bashTool) Description() string {
	return "Execute a shell command and return its output. Never use rm or another permanent deletion command; use the delete tool so items go to the system Trash or Recycle Bin."
}

func (t *bashTool) Spec() []byte {
	return []byte(`{
		"type": "object",
		"properties": {
			"command": {
				"type": "string",
				"description": "The shell command to execute"
			},
			"background": {
				"type": "boolean",
				"description": "Start the command as a kernel-managed background process and return immediately"
			},
			"timeout_seconds": {
				"type": "integer",
				"minimum": 0,
				"maximum": 86400,
				"description": "Maximum runtime. Foreground commands default to 120 seconds; background commands default to no timeout"
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
	if interaction.ModeFromContext(ctx) != interaction.ModeFullAccess &&
		containsPermanentDeletion(params.Command) {
		return errResult("permanent shell deletion is disabled; use the delete tool to move the path to the system Trash or Recycle Bin"), nil
	}
	if params.TimeoutSeconds < 0 || params.TimeoutSeconds > 86400 {
		return errResult("timeout_seconds must be between 0 and 86400"), nil
	}

	workDir := CWDFromContext(ctx)
	decision, err := t.gw.Request(ctx, interaction.Request{
		ToolName: "bash",
		Action:   "execute",
		Detail:   params.Command,
		Resource: params.Command,
		Scope:    workDir,
	})
	if err != nil {
		return errResult("Approval interrupted: " + err.Error()), nil
	}
	if decision == interaction.DecisionDenied {
		return errResult("User denied command execution"), nil
	}

	if t.runner == nil {
		return errResult("execution boundary is unavailable"), nil
	}
	request := sandbox.ExecRequest{
		Argv: []string{t.shell, t.shellFlag, params.Command},
		Dir:  workDir,
	}
	profile := executionProfile(ctx)
	if params.Background {
		if t.background == nil {
			return errResult("background execution is unavailable"), nil
		}
		var timeout time.Duration
		if params.TimeoutSeconds > 0 {
			timeout = time.Duration(params.TimeoutSeconds) * time.Second
		}
		snapshot, err := t.background.Start(
			SessionIDFromContext(ctx),
			params.Command,
			request,
			profile,
			timeout,
		)
		if err != nil {
			return errResult("failed to start background command: " + err.Error()), nil
		}
		data, _ := json.Marshal(struct {
			Status      string                    `json:"status"`
			InitiatedBy string                    `json:"initiated_by"`
			Message     string                    `json:"message"`
			Command     BackgroundCommandSnapshot `json:"command"`
		}{
			Status:      "running_in_background",
			InitiatedBy: "agent",
			Message:     "The command was started in the background by the agent.",
			Command:     snapshot,
		})
		return textResult(string(data)), nil
	}

	timeout := 120 * time.Second
	if params.TimeoutSeconds > 0 {
		timeout = time.Duration(params.TimeoutSeconds) * time.Second
	}
	var (
		result sandbox.ExecResult
		runErr error
	)
	if t.background != nil {
		var snapshot BackgroundCommandSnapshot
		var promoted bool
		result, snapshot, promoted, runErr = t.background.RunForeground(
			ctx,
			SessionIDFromContext(ctx),
			call.ID,
			params.Command,
			request,
			profile,
			timeout,
		)
		if promoted {
			data, _ := json.Marshal(struct {
				Status      string                    `json:"status"`
				InitiatedBy string                    `json:"initiated_by"`
				Message     string                    `json:"message"`
				Command     BackgroundCommandSnapshot `json:"command"`
			}{
				Status:      "running_in_background",
				InitiatedBy: "user",
				Message:     "The user moved this command to the background. Continue the task without waiting for it.",
				Command:     snapshot,
			})
			return textResult(string(data)), nil
		}
	} else {
		cmdCtx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		result, runErr = t.runner.Run(cmdCtx, request, profile)
	}
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
	output = truncateToolOutput(output, truncationOptions{
		MaxLines:  defaultMaxOutputLines,
		MaxBytes:  maxOutputLen,
		Direction: keepOutputTail,
	}).Content
	return Result{
		Content: []ContentPart{{Type: "text", Text: output}},
		IsError: runErr != nil,
	}, nil
}

func containsPermanentDeletion(command string) bool {
	normalized := strings.NewReplacer(
		"\n", " ",
		"\r", " ",
		";", " ",
		"&&", " ",
		"||", " ",
		"|", " ",
		"(", " ",
		")", " ",
	).Replace(command)
	fields := strings.Fields(normalized)
	for index, field := range fields {
		token := strings.ToLower(strings.Trim(field, `"'`))
		if slash := strings.LastIndexAny(token, `/\`); slash >= 0 {
			token = token[slash+1:]
		}
		switch token {
		case "rm", "rmdir", "unlink", "remove-item", "del", "erase", "shred":
			return true
		case "-delete":
			return index > 0
		case "clean":
			if index > 0 && strings.EqualFold(strings.Trim(fields[index-1], `"'`), "git") {
				return true
			}
		}
	}
	return false
}

type bashProcessParams struct {
	CommandID string `json:"command_id"`
}

type bashStatusTool struct {
	background BackgroundCommandManager
}

func NewBashStatusTool(background BackgroundCommandManager) Tool {
	return &bashStatusTool{background: background}
}

func (t *bashStatusTool) Name() string       { return "bash_status" }
func (t *bashStatusTool) Exposure() Exposure { return ExposureDirect }
func (t *bashStatusTool) Parallel() bool     { return true }
func (t *bashStatusTool) Description() string {
	return "Read the current status and accumulated output of a kernel-managed background command."
}
func (t *bashStatusTool) Spec() []byte {
	return []byte(`{
		"type":"object",
		"properties":{"command_id":{"type":"string"}},
		"required":["command_id"],
		"additionalProperties":false
	}`)
}
func (t *bashStatusTool) Run(ctx context.Context, call Call) (Result, error) {
	params, result := parseBashProcessParams(call)
	if result != nil {
		return *result, nil
	}
	snapshot, err := t.background.Get(SessionIDFromContext(ctx), params.CommandID)
	if err != nil {
		return errResult(err.Error()), nil
	}
	data, _ := json.Marshal(snapshot)
	return textResult(string(data)), nil
}

type bashCancelTool struct {
	background BackgroundCommandManager
}

func NewBashCancelTool(background BackgroundCommandManager) Tool {
	return &bashCancelTool{background: background}
}

func (t *bashCancelTool) Name() string       { return "bash_cancel" }
func (t *bashCancelTool) Exposure() Exposure { return ExposureDirect }
func (t *bashCancelTool) Description() string {
	return "Stop a kernel-managed background command started by bash."
}
func (t *bashCancelTool) Spec() []byte {
	return []byte(`{
		"type":"object",
		"properties":{"command_id":{"type":"string"}},
		"required":["command_id"],
		"additionalProperties":false
	}`)
}
func (t *bashCancelTool) Run(ctx context.Context, call Call) (Result, error) {
	params, result := parseBashProcessParams(call)
	if result != nil {
		return *result, nil
	}
	snapshot, err := t.background.Stop(
		SessionIDFromContext(ctx),
		params.CommandID,
		"agent",
	)
	if err != nil {
		return errResult(err.Error()), nil
	}
	data, _ := json.Marshal(snapshot)
	return textResult(string(data)), nil
}

func parseBashProcessParams(call Call) (bashProcessParams, *Result) {
	var params bashProcessParams
	if err := json.Unmarshal(call.Input, &params); err != nil {
		result := errResult("invalid arguments: " + err.Error())
		return params, &result
	}
	if strings.TrimSpace(params.CommandID) == "" {
		result := errResult("command_id is required")
		return params, &result
	}
	return params, nil
}

func defaultShell() (shell, flag string) {
	if runtime.GOOS == "windows" {
		return "cmd", "/c"
	}
	return "/bin/sh", "-c"
}

func errResult(msg string) Result {
	return Result{IsError: true, Content: []ContentPart{{Type: "text", Text: msg}}}
}
