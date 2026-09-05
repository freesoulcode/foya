// Package hooks runs configured lifecycle hooks outside the Foya kernel
// process. Hook commands receive a structured JSON request on stdin and may
// return a structured JSON result on stdout.
package hooks

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	foyatelemetry "github.com/freesoulcode/foya/internal/telemetry"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

const (
	maxHookOutputBytes = 64 << 10
	blockExitCode      = 2
	haltExitCode       = 49
)

// Request is the stable input contract delivered to hook commands.
// Fields irrelevant to an event are omitted.
type Request struct {
	Version           int             `json:"version"`
	EventID           string          `json:"event_id"`
	Event             Event           `json:"event"`
	OccurredAt        time.Time       `json:"occurred_at"`
	SessionID         string          `json:"session_id,omitempty"`
	RootSessionID     string          `json:"root_session_id,omitempty"`
	RunID             string          `json:"run_id,omitempty"`
	CWD               string          `json:"cwd,omitempty"`
	ProjectID         string          `json:"project_id,omitempty"`
	ProjectPath       string          `json:"project_path,omitempty"`
	Model             string          `json:"model,omitempty"`
	Source            string          `json:"source,omitempty"`
	Status            string          `json:"status,omitempty"`
	Reason            string          `json:"reason,omitempty"`
	Error             string          `json:"error,omitempty"`
	StartedAt         *time.Time      `json:"started_at,omitempty"`
	CompletedAt       *time.Time      `json:"completed_at,omitempty"`
	DurationMS        int64           `json:"duration_ms,omitempty"`
	UserPrompt        string          `json:"user_prompt,omitempty"`
	ToolName          string          `json:"tool_name,omitempty"`
	ToolCallID        string          `json:"tool_call_id,omitempty"`
	ToolInput         json.RawMessage `json:"tool_input,omitempty"`
	ToolOutput        string          `json:"tool_output,omitempty"`
	ToolIsError       bool            `json:"tool_is_error,omitempty"`
	AssistantMessage  string          `json:"assistant_message,omitempty"`
	AgentID           string          `json:"agent_id,omitempty"`
	AgentType         string          `json:"agent_type,omitempty"`
	ParentSessionID   string          `json:"parent_session_id,omitempty"`
	ChildSessionID    string          `json:"child_session_id,omitempty"`
	CompactionTrigger string          `json:"compaction_trigger,omitempty"`
	CompactionPhase   string          `json:"compaction_phase,omitempty"`
	Notification      string          `json:"notification,omitempty"`
	Metadata          map[string]any  `json:"metadata,omitempty"`
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
	EventID      string          `json:"event_id"`
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

// Run invokes matching synchronous lifecycle hooks. Observational events are
// rejected here because callers must use Observe or Notify.
func (r *Runtime) Run(ctx context.Context, request Request) Outcome {
	return r.RunWithCompletion(ctx, request, nil)
}

// RunWithCompletion dispatches synchronous handlers inline and reports each
// handler configured with async=true through completed after it finishes.
func (r *Runtime) RunWithCompletion(
	ctx context.Context,
	request Request,
	completed func(Outcome),
) Outcome {
	if request.Event.IsAsync() {
		return Outcome{Decision: DecisionNone}
	}
	return r.dispatch(ctx, request, false, func(result Result) {
		if completed != nil {
			completed(Outcome{Decision: DecisionNone, Results: []Result{result}})
		}
	})
}

// Notify invokes Notification hooks asynchronously. Their output cannot
// affect the caller; completed receives it only for audit recording.
func (r *Runtime) Notify(ctx context.Context, request Request, completed func(Outcome)) {
	request.Event = EventNotification
	r.Observe(ctx, request, completed)
}

// Observe invokes an observational lifecycle event asynchronously. Hook output
// cannot alter the operation that produced the event.
func (r *Runtime) Observe(ctx context.Context, request Request, completed func(Outcome)) {
	go func() {
		outcome := r.dispatch(context.WithoutCancel(ctx), request, true, nil)
		if completed != nil {
			completed(outcome)
		}
	}()
}

func (r *Runtime) dispatch(
	ctx context.Context,
	request Request,
	forceInline bool,
	asyncCompleted func(Result),
) Outcome {
	request.Version = 1
	if request.EventID == "" {
		request.EventID = newEventID()
	}
	if request.OccurredAt.IsZero() {
		request.OccurredAt = time.Now().UTC()
	}
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
		if hook.Event != request.Event || !matches(hook, request) {
			continue
		}
		request.ToolInput = currentInput
		if hook.Async && !forceInline {
			hookCopy := hook
			requestCopy := request
			go func() {
				result := runCommand(context.WithoutCancel(ctx), hookCopy, requestCopy)
				if asyncCompleted != nil {
					asyncCompleted(result)
				}
			}()
			continue
		}
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

func newEventID() string {
	data := make([]byte, 12)
	if _, err := rand.Read(data); err != nil {
		return fmt.Sprintf("hook-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(data)
}

func matches(hook Config, request Request) bool {
	switch hook.Event {
	case EventPreToolUse, EventPostToolUse, EventPermissionRequest:
		return hook.MatchesTool(request.ToolName)
	case EventSessionStart, EventSessionEnd:
		return hook.MatchesTool(request.Source)
	case EventSubagentStart, EventSubagentStop:
		return hook.MatchesTool(request.AgentType)
	case EventPreCompact, EventPostCompact:
		return hook.MatchesTool(request.CompactionTrigger)
	default:
		return hook.IsEnabled()
	}
}

func runCommand(parent context.Context, hook Config, request Request) (result Result) {
	started := time.Now()
	traceName := "foya.turn"
	if request.RunID == "" {
		traceName = "foya.session"
	}
	langfuseSessionID := request.RootSessionID
	if langfuseSessionID == "" {
		langfuseSessionID = request.SessionID
	}
	parent, span := foyatelemetry.StartSpan(
		parent,
		"foya.hook",
		trace.SpanKindInternal,
		attribute.String("foya.hook.event", string(request.Event)),
		attribute.String("foya.hook.id", hook.ID),
		attribute.String("foya.hook.name", displayName(hook)),
		attribute.String("session.id", request.SessionID),
		attribute.String("langfuse.session.id", langfuseSessionID),
		attribute.String("langfuse.trace.name", traceName),
		attribute.String("langfuse.observation.type", "span"),
		attribute.String("foya.run.id", request.RunID),
	)
	defer func() {
		failed := result.Error != ""
		var resultErr error
		if failed {
			resultErr = errors.New(result.Error)
		}
		attrs := []attribute.KeyValue{
			attribute.String("foya.hook.decision", string(result.Decision)),
			attribute.Bool("foya.hook.halt", result.Halt),
		}
		foyatelemetry.EndSpan(span, "completed", resultErr, attrs...)
		foyatelemetry.RecordHook(
			parent,
			time.Since(started),
			failed,
			attribute.String("foya.hook.event", string(request.Event)),
			attribute.String("foya.hook.name", displayName(hook)),
			attribute.String("foya.hook.decision", string(result.Decision)),
		)
	}()
	result = Result{
		ID:       hook.ID,
		EventID:  request.EventID,
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
		if sensitiveEnvironmentName(name) {
			continue
		}
		env = append(env, item)
	}
	env = append(env,
		"FOYA_HOOK_EVENT="+string(request.Event),
		"FOYA_HOOK_EVENT_ID="+request.EventID,
		"FOYA_HOOK_SESSION_ID="+request.SessionID,
		"FOYA_HOOK_RUN_ID="+request.RunID,
		"FOYA_HOOK_PROJECT_ID="+request.ProjectID,
		"FOYA_HOOK_PROJECT_PATH="+request.ProjectPath,
		"FOYA_HOOK_TOOL_NAME="+request.ToolName,
		"FOYA_HOOK_TOOL_CALL_ID="+request.ToolCallID,
	)
	return env
}

func sensitiveEnvironmentName(name string) bool {
	name = strings.ToUpper(strings.TrimSpace(name))
	for _, marker := range []string{"API_KEY", "SECRET", "TOKEN", "PASSWORD"} {
		if strings.Contains(name, marker) {
			return true
		}
	}
	return name == "LANGFUSE_AUTH" ||
		strings.HasPrefix(name, "OTEL_EXPORTER_OTLP_") && strings.HasSuffix(name, "_HEADERS")
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
