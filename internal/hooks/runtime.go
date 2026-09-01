// Package hooks runs configured lifecycle hooks outside the Foya kernel
// process. Hook commands receive a structured JSON request on stdin and may
// return a structured JSON result on stdout.
package hooks

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

const (
	maxHookOutputBytes = 64 << 10
	blockExitCode      = 2
	haltExitCode       = 49
)

// Request is the stable input contract delivered to hook commands.
// Fields irrelevant to an event are omitted.
type Request struct {
	Version          int             `json:"version"`
	Event            Event           `json:"event"`
	SessionID        string          `json:"session_id,omitempty"`
	RunID            string          `json:"run_id,omitempty"`
	CWD              string          `json:"cwd,omitempty"`
	ProjectPath      string          `json:"project_path,omitempty"`
	Model            string          `json:"model,omitempty"`
	UserPrompt       string          `json:"user_prompt,omitempty"`
	ToolName         string          `json:"tool_name,omitempty"`
	ToolCallID       string          `json:"tool_call_id,omitempty"`
	ToolInput        json.RawMessage `json:"tool_input,omitempty"`
	ToolOutput       string          `json:"tool_output,omitempty"`
	AssistantMessage string          `json:"assistant_message,omitempty"`
	Notification     string          `json:"notification,omitempty"`
}

// Decision is a control decision returned by a hook.
type Decision string

const (
	DecisionNone  Decision = "none"
	DecisionAllow Decision = "allow"
	DecisionDeny  Decision = "deny"
)

// Result is the normalized outcome of one configured command.
type Result struct {
	ID           string          `json:"id,omitempty"`
	Name         string          `json:"name"`
	Event        Event           `json:"event"`
	Decision     Decision        `json:"decision"`
	Halt         bool            `json:"halt,omitempty"`
	Reason       string          `json:"reason,omitempty"`
	Context      []string        `json:"context,omitempty"`
	UpdatedInput json.RawMessage `json:"updated_input,omitempty"`
	Duration     time.Duration   `json:"duration"`
	Error        string          `json:"error,omitempty"`
}

// Outcome merges every matching hook result in configuration order.
// Deny wins over allow. A halt is sticky. UpdatedInput is the latest valid
// shallow JSON object merge, and Context is appended in command order.
type Outcome struct {
	Decision     Decision
	Halt         bool
	Reason       string
	Context      []string
	UpdatedInput json.RawMessage
	Results      []Result
}

// Runtime loads and dispatches hooks. It deliberately has no dependency on
// AgentLoop, tools, approvals, or the transport layer.
type Runtime struct {
	homeDir string
}

// NewRuntime creates a hook runtime. An empty homeDir disables global hook
// discovery while project hooks remain available.
func NewRuntime(homeDir string) *Runtime {
	return &Runtime{homeDir: homeDir}
}

// Run invokes matching synchronous hooks. Notification is intentionally
// rejected here because callers must use Notify for its fire-and-forget
// semantics.
func (r *Runtime) Run(ctx context.Context, request Request) Outcome {
	if request.Event.IsAsync() {
		return Outcome{Decision: DecisionNone}
	}
	return r.dispatch(ctx, request)
}

// Notify invokes Notification hooks asynchronously. Their output cannot
// affect the caller; completed receives it only for audit recording.
func (r *Runtime) Notify(ctx context.Context, request Request, completed func(Outcome)) {
	if request.Event != EventNotification {
		request.Event = EventNotification
	}
	go func() {
		outcome := r.dispatch(context.WithoutCancel(ctx), request)
		if completed != nil {
			completed(outcome)
		}
	}()
}

func (r *Runtime) dispatch(ctx context.Context, request Request) Outcome {
	request.Version = 1
	hooks, err := LoadForProject(r.homeDir, request.ProjectPath)
	if err != nil {
		return Outcome{
			Decision: DecisionNone,
			Results: []Result{{
				Name:  "configuration",
				Event: request.Event,
				Error: err.Error(),
			}},
		}
	}

	outcome := Outcome{Decision: DecisionNone}
	originalInput := cloneJSON(request.ToolInput)
	currentInput := cloneJSON(request.ToolInput)
	for _, hook := range hooks {
		if hook.Event != request.Event || !matches(hook, request.ToolName) {
			continue
		}
		request.ToolInput = currentInput
		result := runCommand(ctx, hook, request)
		outcome.Results = append(outcome.Results, result)

		switch result.Decision {
		case DecisionDeny:
			outcome.Decision = DecisionDeny
			if result.Reason != "" {
				outcome.Reason = appendText(outcome.Reason, result.Reason)
			}
		case DecisionAllow:
			if outcome.Decision != DecisionDeny {
				outcome.Decision = DecisionAllow
			}
		}
		if result.Halt {
			outcome.Halt = true
			if result.Reason != "" && result.Decision != DecisionDeny {
				outcome.Reason = appendText(outcome.Reason, result.Reason)
			}
		}
		outcome.Context = append(outcome.Context, result.Context...)

		if outcome.Decision != DecisionDeny && len(result.UpdatedInput) > 0 {
			merged, mergeErr := shallowMerge(currentInput, result.UpdatedInput)
			if mergeErr != nil {
				result.Error = appendText(result.Error, "invalid updated_input: "+mergeErr.Error())
				outcome.Results[len(outcome.Results)-1] = result
				continue
			}
			currentInput = merged
		}
	}
	if len(currentInput) > 0 && !bytes.Equal(currentInput, originalInput) {
		outcome.UpdatedInput = currentInput
	}
	return outcome
}

func matches(hook Config, toolName string) bool {
	switch hook.Event {
	case EventPreToolUse, EventPostToolUse:
		return hook.MatchesTool(toolName)
	default:
		return hook.IsEnabled()
	}
}

func runCommand(parent context.Context, hook Config, request Request) Result {
	started := time.Now()
	result := Result{
		ID:       hook.ID,
		Name:     displayName(hook),
		Event:    request.Event,
		Decision: DecisionNone,
	}
	payload, err := json.Marshal(request)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	ctx, cancel := context.WithTimeout(parent, hook.TimeoutDuration())
	defer cancel()

	command, args := shellCommand(hook.Command)
	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Stdin = bytes.NewReader(payload)
	cmd.Dir = request.CWD
	cmd.Env = hookEnvironment(request)
	var stdout, stderr limitedBuffer
	stdout.limit = maxHookOutputBytes
	stderr.limit = maxHookOutputBytes
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err = cmd.Run()
	result.Duration = time.Since(started)
	if ctx.Err() != nil {
		result.Error = "hook timed out or was cancelled: " + ctx.Err().Error()
		return result
	}
	if err != nil {
		exitErr := new(exec.ExitError)
		if errors.As(err, &exitErr) {
			switch exitErr.ExitCode() {
			case blockExitCode:
				result.Decision = DecisionDeny
				result.Reason = nonEmpty(strings.TrimSpace(stderr.String()), "blocked by hook")
				return result
			case haltExitCode:
				result.Decision = DecisionDeny
				result.Halt = true
				result.Reason = nonEmpty(strings.TrimSpace(stderr.String()), "turn halted by hook")
				return result
			}
		}
		result.Error = nonEmpty(strings.TrimSpace(stderr.String()), err.Error())
		return result
	}
	if stdout.truncated || stderr.truncated {
		result.Error = "hook output exceeded 64 KiB"
		return result
	}

	parsed, parseErr := parseOutput(stdout.Bytes())
	if parseErr != nil {
		result.Error = parseErr.Error()
		return result
	}
	result.Decision = parsed.Decision
	result.Halt = parsed.Halt
	result.Reason = parsed.Reason
	result.Context, _ = parseContext(parsed.Context)
	result.UpdatedInput = parsed.UpdatedInput
	return result
}

type commandOutput struct {
	Decision     Decision        `json:"decision"`
	Halt         bool            `json:"halt,omitempty"`
	Reason       string          `json:"reason,omitempty"`
	Context      json.RawMessage `json:"context,omitempty"`
	UpdatedInput json.RawMessage `json:"updated_input,omitempty"`
}

func parseOutput(data []byte) (commandOutput, error) {
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return commandOutput{Decision: DecisionNone}, nil
	}
	var output commandOutput
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&output); err != nil {
		return commandOutput{}, fmt.Errorf("invalid hook JSON output: %w", err)
	}
	switch output.Decision {
	case "", DecisionNone:
		output.Decision = DecisionNone
	case DecisionAllow, DecisionDeny:
	default:
		return commandOutput{}, fmt.Errorf("invalid hook decision %q", output.Decision)
	}
	if _, err := parseContext(output.Context); err != nil {
		return commandOutput{}, err
	}
	if len(output.UpdatedInput) > 0 && !isJSONObject(output.UpdatedInput) {
		return commandOutput{}, errors.New("updated_input must be a JSON object")
	}
	return output, nil
}

func parseContext(raw json.RawMessage) ([]string, error) {
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return nil, nil
	}
	var single string
	if err := json.Unmarshal(raw, &single); err == nil {
		return nonEmptySlice(single), nil
	}
	var multiple []string
	if err := json.Unmarshal(raw, &multiple); err != nil {
		return nil, errors.New("context must be a string or string array")
	}
	out := make([]string, 0, len(multiple))
	for _, item := range multiple {
		out = append(out, nonEmptySlice(item)...)
	}
	return out, nil
}

func shellCommand(command string) (string, []string) {
	if runtime.GOOS == "windows" {
		return "cmd.exe", []string{"/C", command}
	}
	return "/bin/sh", []string{"-c", command}
}

func hookEnvironment(request Request) []string {
	env := make([]string, 0, len(os.Environ())+8)
	for _, item := range os.Environ() {
		name, _, _ := strings.Cut(item, "=")
		if strings.Contains(strings.ToUpper(name), "API_KEY") {
			continue
		}
		env = append(env, item)
	}
	env = append(env,
		"FOYA_HOOK_EVENT="+string(request.Event),
		"FOYA_HOOK_SESSION_ID="+request.SessionID,
		"FOYA_HOOK_RUN_ID="+request.RunID,
		"FOYA_HOOK_PROJECT_PATH="+request.ProjectPath,
		"FOYA_HOOK_TOOL_NAME="+request.ToolName,
		"FOYA_HOOK_TOOL_CALL_ID="+request.ToolCallID,
	)
	return env
}

func displayName(hook Config) string {
	if hook.Name != "" {
		return hook.Name
	}
	if hook.ID != "" {
		return hook.ID
	}
	return hook.Command
}

func appendText(current, next string) string {
	if current == "" {
		return next
	}
	if next == "" {
		return current
	}
	return current + "\n" + next
}

func nonEmpty(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}

func nonEmptySlice(value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return []string{value}
}

func cloneJSON(input json.RawMessage) json.RawMessage {
	if len(input) == 0 {
		return json.RawMessage(`{}`)
	}
	out := make(json.RawMessage, len(input))
	copy(out, input)
	return out
}

func isJSONObject(input json.RawMessage) bool {
	var object map[string]json.RawMessage
	return json.Unmarshal(input, &object) == nil
}

func shallowMerge(base, patch json.RawMessage) (json.RawMessage, error) {
	var result map[string]json.RawMessage
	if err := json.Unmarshal(base, &result); err != nil {
		return nil, errors.New("tool input must be a JSON object")
	}
	var changes map[string]json.RawMessage
	if err := json.Unmarshal(patch, &changes); err != nil {
		return nil, errors.New("updated_input must be a JSON object")
	}
	for key, value := range changes {
		result[key] = value
	}
	return json.Marshal(result)
}

type limitedBuffer struct {
	bytes.Buffer
	limit     int
	truncated bool
}

func (b *limitedBuffer) Write(input []byte) (int, error) {
	remaining := b.limit - b.Len()
	if remaining <= 0 {
		b.truncated = true
		return len(input), nil
	}
	if len(input) > remaining {
		_, _ = b.Buffer.Write(input[:remaining])
		b.truncated = true
		return len(input), nil
	}
	return b.Buffer.Write(input)
}
