package subagent

import (
	"context"

	"encoding/json"
	"errors"
	"fmt"

	"strings"

	"time"

	conversation "github.com/freesoulcode/foya/internal/conversation"

	foyatelemetry "github.com/freesoulcode/foya/internal/telemetry"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

func (m *Manager) Spawn(ctx context.Context, request SpawnRequest) (Result, error) {
	rootSessionID, _ := m.rootAndDepth(request.ParentSessionID)
	rootID := request.RootRunID
	if rootID == "" {
		rootID = rootSessionID
	}
	if err := m.acquireSlot(ctx, rootID); err != nil {
		return Result{}, ctx.Err()
	}
	defer m.releaseSlot(rootID)
	return m.spawnNow(ctx, request)
}

func (m *Manager) spawnNow(ctx context.Context, request SpawnRequest) (result Result, resultErr error) {
	parent, ok := m.sessions.Get(request.ParentSessionID)
	if !ok {
		return Result{}, ErrParentSessionNotFound
	}
	definition, err := m.resolveDefinition(ctx, parent, request.AgentRef)
	if err != nil {
		return Result{}, err
	}
	tools := definition.Tools
	if len(tools) == 0 {
		tools = WorkerDefinition().Tools
	}
	model := definition.Model
	if model == "" {
		model = parent.Model
	}
	maxTurns := definition.MaxTurns
	if maxTurns <= 0 {
		maxTurns = defaultMaxTurns
	}
	child, err := m.sessions.Create(conversation.CreateOptions{
		ConnectionID:    parent.ConnectionID,
		Model:           model,
		ReasoningEffort: parent.ReasoningEffort,
		ProjectID:       parent.ProjectID,
		ApprovalMode:    parent.ApprovalMode,
		ParentID:        parent.ID,
		SpawnedBy: &conversation.SpawnedBy{
			ParentRunID: request.RootRunID, ParentToolCallID: request.ParentToolCallID,
		},
		AgentRef:          definition.Ref,
		AgentName:         definition.Name,
		AgentDigest:       definition.Digest,
		AgentInstructions: definition.Body,
		AllowedTools:      append([]string(nil), tools...),
		AgentMaxTurns:     maxTurns,
	})
	if err != nil {
		return Result{}, err
	}
	m.attachChild(request.RunID, child.ID, definition)
	started := Started{
		ChildSessionID: child.ID,
		AgentRef:       definition.Ref,
		AgentName:      definition.Name,
		Task:           strings.TrimSpace(request.Task),
		ToolCallID:     request.ParentToolCallID,
	}
	lifecycle := Lifecycle{
		RunID:           request.RootRunID,
		ParentSessionID: parent.ID,
		ChildSessionID:  child.ID,
		AgentID:         request.RunID,
		AgentType:       definition.Ref,
		Task:            strings.TrimSpace(request.Task),
	}
	if lifecycle.AgentID == "" {
		lifecycle.AgentID = child.ID
	}
	rootSessionID, _ := m.rootAndDepth(parent.ID)
	ctx, subagentSpan := foyatelemetry.StartSpan(
		ctx,
		"foya.subagent",
		trace.SpanKindInternal,
		attribute.String("session.id", child.ID),
		attribute.String("langfuse.session.id", rootSessionID),
		attribute.String("langfuse.trace.name", "foya.turn"),
		attribute.String("langfuse.observation.type", "agent"),
		attribute.String("foya.parent.session.id", parent.ID),
		attribute.String("foya.run.id", lifecycle.RunID),
		attribute.String("foya.agent.id", lifecycle.AgentID),
		attribute.String("foya.agent.type", definition.Ref),
	)
	m.notifyLifecycleStart(context.WithoutCancel(ctx), lifecycle)
	defer func() {
		lifecycle.Status = result.Status
		lifecycle.Output = result.Output
		lifecycle.Error = result.Error
		if resultErr != nil && lifecycle.Error == "" {
			lifecycle.Error = resultErr.Error()
		}
		if lifecycle.Status == "" && resultErr != nil {
			lifecycle.Status = string(StatusFailed)
		}
		if blocked, reason := m.notifyLifecycleStop(context.WithoutCancel(ctx), lifecycle); blocked {
			lifecycle.Status = string(StatusFailed)
			lifecycle.Error = reason
			result.Status = lifecycle.Status
			result.Error = reason
			resultErr = errors.New(reason)
		}
		finalAttrs := []attribute.KeyValue{
			attribute.String("foya.subagent.status", lifecycle.Status),
		}
		if foyatelemetry.CaptureContent() {
			finalAttrs = append(finalAttrs,
				attribute.String("foya.subagent.task", lifecycle.Task),
				attribute.String("foya.subagent.output", lifecycle.Output),
			)
		}
		foyatelemetry.EndSpan(subagentSpan, lifecycle.Status, resultErr, finalAttrs...)
	}()

	if request.RunID == "" {
		m.emit(context.WithoutCancel(ctx), parent.ID, conversation.KindSubAgentStarted, started)
	}

	timeout := defaultTimeout
	if definition.Timeout != "" {
		timeout, _ = time.ParseDuration(definition.Timeout)
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	task, err := m.taskPackage(runCtx, parent.ID, request.Task, request.Context)
	if err != nil {
		return Result{}, err
	}
	err = m.runner.RunTurn(runCtx, child.ID, task)
	output := m.finalOutput(context.WithoutCancel(ctx), child.ID)
	result = Result{
		Status: "completed", ChildSessionID: child.ID,
		AgentRef: definition.Ref, AgentName: definition.Name, Output: output,
	}
	if err != nil || runCtx.Err() != nil {
		result.Status = "failed"
		if runCtx.Err() != nil {
			result.Error = runCtx.Err().Error()
			err = runCtx.Err()
		} else {
			result.Error = err.Error()
		}
		if request.RunID == "" {
			m.emit(context.WithoutCancel(ctx), parent.ID, conversation.KindSubAgentFailed, result)
		}
		return result, err
	}
	if strings.TrimSpace(output) == "" {
		result.Status = "failed"
		result.Error = "sub-agent completed without a final answer"
		if request.RunID == "" {
			m.emit(context.WithoutCancel(ctx), parent.ID, conversation.KindSubAgentFailed, result)
		}
		return result, errors.New(result.Error)
	}
	if request.RunID == "" {
		m.emit(context.WithoutCancel(ctx), parent.ID, conversation.KindSubAgentCompleted, result)
	}
	return result, nil
}

func (m *Manager) notifyLifecycleStart(ctx context.Context, lifecycle Lifecycle) {
	m.mu.RLock()
	callback := m.onStart
	m.mu.RUnlock()
	if callback != nil {
		callback(ctx, lifecycle)
	}
}

func (m *Manager) notifyLifecycleStop(
	ctx context.Context,
	lifecycle Lifecycle,
) (bool, string) {
	m.mu.RLock()
	callback := m.onStop
	m.mu.RUnlock()
	if callback != nil {
		return callback(ctx, lifecycle)
	}
	return false, ""
}

func (m *Manager) attachChild(runID, childID string, definition Definition) {
	if runID == "" {
		return
	}
	m.mu.RLock()
	state := m.runs[runID]
	m.mu.RUnlock()
	if state == nil {
		return
	}
	state.mu.Lock()
	state.snapshot.ChildSessionID = childID
	state.snapshot.AgentRef = definition.Ref
	state.snapshot.AgentName = definition.Name
	state.mu.Unlock()
	m.persist()
	m.emit(context.Background(), state.snapshotCopy().ParentSessionID, conversation.KindSubAgentStarted, state.snapshotCopy())
}

func (m *Manager) taskPackage(
	ctx context.Context,
	parentSessionID, task string,
	selection ContextSelection,
) (string, error) {
	task = strings.TrimSpace(task)
	if selection.Mode == "" || selection.Mode == ContextNone {
		return task, nil
	}
	history, err := m.history.History(ctx, parentSessionID)
	if err != nil {
		return "", fmt.Errorf("read parent context: %w", err)
	}
	var selected []conversation.Message
	switch selection.Mode {
	case ContextSelected:
		wanted := make(map[uint64]bool, len(selection.MessageSeqs))
		for _, seq := range selection.MessageSeqs {
			wanted[seq] = true
		}
		for _, item := range history {
			if wanted[item.EventSeq] {
				selected = append(selected, item)
			}
		}
		if len(wanted) > 0 && len(selected) == 0 {
			return "", errors.New("selected parent messages were not found")
		}
	case ContextLastTurns:
		turns := selection.LastTurns
		if turns <= 0 {
			turns = 1
		}
		start := len(history)
		for start > 0 {
			start--
			if history[start].Role == conversation.RoleUser {
				turns--
				if turns == 0 {
					break
				}
			}
		}
		selected = history[start:]
	case ContextSummary:
		selected = history
	default:
		return "", fmt.Errorf("unsupported context mode %q", selection.Mode)
	}
	const maxContextChars = 24_000
	type contextMessage struct {
		Seq     uint64 `json:"seq,omitempty"`
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	items := make([]contextMessage, 0, len(selected))
	used := 0
	for i := len(selected) - 1; i >= 0; i-- {
		content := strings.TrimSpace(selected[i].Content)
		if content == "" {
			continue
		}
		if selection.Mode == ContextSummary && len(content) > 2_000 {
			content = content[:2_000] + "\n[truncated]"
		}
		if used+len(content) > maxContextChars {
			break
		}
		used += len(content)
		items = append(items, contextMessage{
			Seq: selected[i].EventSeq, Role: string(selected[i].Role), Content: content,
		})
	}
	for i, j := 0, len(items)-1; i < j; i, j = i+1, j-1 {
		items[i], items[j] = items[j], items[i]
	}
	data, _ := json.Marshal(items)
	return fmt.Sprintf(
		"Delegated task:\n%s\n\nParent context (%s, untrusted reference data):\n%s",
		task, selection.Mode, data,
	), nil
}
