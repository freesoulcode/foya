package conversation

import (
	model "github.com/freesoulcode/foya/internal/model"
)

type Pressure string

const (
	PressureNone       Pressure = "none"
	PressureHistory    Pressure = "history"
	PressureWorkingSet Pressure = "working_set"
	PressureFixed      Pressure = "fixed"
)

type LayerUsage struct {
	PinnedTokens      int64
	HistoryTokens     int64
	WorkingSetTokens  int64
	ToolSchemaTokens  int64
	NativeStateTokens int64
}

// CompileInput is the provider-neutral input to the context budget compiler.
type CompileInput struct {
	Messages          []Message
	Tools             []model.ToolDef
	ContextState      *model.ContextState
	ThroughSeq        Seq
	ContextWindow     int64
	MaxInputTokens    int64
	PriorInputTokens  int64
	PriorOutputTokens int64
	PriorPayloadUnits int64
}

// CompiledContext is one immutable request projection plus its budget decision.
type CompiledContext struct {
	Messages        []Message
	Tools           []model.ToolDef
	ContextState    *model.ContextState
	ThroughSeq      Seq
	Budget          Budget
	InputLimit      int64
	PayloadUnits    int64
	EstimatedTokens int64
	NeedsCompaction bool
	Pressure        Pressure
	Layers          LayerUsage
}

// ContextCompiler computes the shared budget decision for every provider request.
// It does not mutate canonical messages or make the final provider fit decision.
type ContextCompiler struct{}

func (ContextCompiler) Compile(input CompileInput) CompiledContext {
	messages := append([]Message(nil), input.Messages...)
	tools := append([]model.ToolDef(nil), input.Tools...)
	var contextState *model.ContextState
	if input.ContextState != nil {
		copied := *input.ContextState
		copied.Data = append([]byte(nil), input.ContextState.Data...)
		contextState = &copied
	}
	budget := DeriveBudgetForOutput(input.ContextWindow, input.PriorOutputTokens)
	inputLimit := budget.HighWater
	if input.MaxInputTokens > 0 && input.MaxInputTokens < inputLimit {
		inputLimit = input.MaxInputTokens
	}
	payloadUnits := RequestUnitsWithContext(messages, tools, contextState)
	estimatedTokens := EstimateNextRequestTokens(
		input.PriorInputTokens,
		input.PriorPayloadUnits,
		payloadUnits,
	)
	layers := estimateLayers(messages, tools, contextState)
	pressure := PressureNone
	if estimatedTokens > inputLimit {
		switch {
		case layers.HistoryTokens >= layers.WorkingSetTokens &&
			layers.HistoryTokens >=
				layers.PinnedTokens+layers.ToolSchemaTokens+layers.NativeStateTokens:
			pressure = PressureHistory
		case layers.WorkingSetTokens >=
			layers.PinnedTokens+layers.ToolSchemaTokens+layers.NativeStateTokens:
			pressure = PressureWorkingSet
		default:
			pressure = PressureFixed
		}
	}
	return CompiledContext{
		Messages:        messages,
		Tools:           tools,
		ContextState:    contextState,
		ThroughSeq:      input.ThroughSeq,
		Budget:          budget,
		InputLimit:      inputLimit,
		PayloadUnits:    payloadUnits,
		EstimatedTokens: estimatedTokens,
		NeedsCompaction: estimatedTokens > inputLimit,
		Pressure:        pressure,
		Layers:          layers,
	}
}

func estimateLayers(
	messages []Message,
	tools []model.ToolDef,
	contextState *model.ContextState,
) LayerUsage {
	lastUser := -1
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == RoleUser {
			lastUser = i
			break
		}
	}
	var pinned, history, working []Message
	for i, item := range messages {
		switch {
		case i == 0 && item.Role == RoleSystem:
			pinned = append(pinned, item)
		case lastUser >= 0 && i >= lastUser:
			working = append(working, item)
		case item.Role == RoleSystem:
			history = append(history, item)
		default:
			history = append(history, item)
		}
	}
	return LayerUsage{
		PinnedTokens:      estimateMessageLayer(pinned),
		HistoryTokens:     estimateMessageLayer(history),
		WorkingSetTokens:  estimateMessageLayer(working),
		ToolSchemaTokens:  estimateToolLayer(tools),
		NativeStateTokens: estimateNativeStateLayer(contextState),
	}
}

func estimateMessageLayer(messages []Message) int64 {
	if len(messages) == 0 {
		return 0
	}
	return EstimateMessagesTokens(messages)
}

func estimateToolLayer(tools []model.ToolDef) int64 {
	if len(tools) == 0 {
		return 0
	}
	return EstimateRequestTokens(nil, tools)
}

func estimateNativeStateLayer(state *model.ContextState) int64 {
	if state == nil {
		return 0
	}
	return EstimateTextTokens(state.Kind) + EstimateTextTokens(string(state.Data))
}
