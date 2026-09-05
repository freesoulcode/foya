package compaction

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/freesoulcode/foya/internal/message"
	"github.com/freesoulcode/foya/internal/provider"
)

func TestContextCompilerUsesCalibratedBudget(t *testing.T) {
	messages := []message.Message{{
		Role: message.RoleUser, Content: strings.Repeat("x", 8_000),
	}}
	units := RequestUnits(messages, nil)
	compiled := (ContextCompiler{}).Compile(CompileInput{
		Messages:          messages,
		ContextWindow:     10_000,
		PriorInputTokens:  7_000,
		PriorOutputTokens: 1_000,
		PriorPayloadUnits: units - 400,
	})
	if compiled.EstimatedTokens != 7_100 {
		t.Fatalf("estimated tokens = %d, want 7100", compiled.EstimatedTokens)
	}
	if compiled.Budget.ReserveTokens != 2_000 ||
		compiled.InputLimit != 8_000 ||
		compiled.NeedsCompaction ||
		compiled.Pressure != PressureNone {
		t.Fatalf("compiled context = %#v", compiled)
	}
}

func TestContextCompilerHonorsExplicitInputLimit(t *testing.T) {
	messages := []message.Message{{
		Role: message.RoleUser, Content: strings.Repeat("x", 8_000),
	}}
	compiled := (ContextCompiler{}).Compile(CompileInput{
		Messages:       messages,
		ContextWindow:  100_000,
		MaxInputTokens: 1_000,
	})
	if compiled.InputLimit != 1_000 || !compiled.NeedsCompaction {
		t.Fatalf("compiled context = %#v", compiled)
	}
	if compiled.Pressure != PressureWorkingSet {
		t.Fatalf("pressure = %q", compiled.Pressure)
	}
}

func TestContextCompilerClassifiesHistoricalPressure(t *testing.T) {
	compiled := (ContextCompiler{}).Compile(CompileInput{
		Messages: []message.Message{
			{Role: message.RoleSystem, Content: "system"},
			{Role: message.RoleUser, Content: "old"},
			{Role: message.RoleAssistant, Content: strings.Repeat("history ", 2_000)},
			{Role: message.RoleUser, Content: "current"},
		},
		ContextWindow: 1_000,
	})
	if !compiled.NeedsCompaction ||
		compiled.Pressure != PressureHistory ||
		compiled.Layers.HistoryTokens == 0 {
		t.Fatalf("compiled context = %#v", compiled)
	}
}

func TestContextCompilerCopiesProviderState(t *testing.T) {
	state := &provider.ContextState{
		Kind: "native",
		Data: json.RawMessage(`{"value":"original"}`),
	}
	compiled := (ContextCompiler{}).Compile(CompileInput{
		ContextState:  state,
		ContextWindow: 10_000,
	})
	state.Data[10] = 'X'
	if string(compiled.ContextState.Data) != `{"value":"original"}` {
		t.Fatalf("compiled state was mutated: %s", compiled.ContextState.Data)
	}
	if compiled.Layers.NativeStateTokens == 0 {
		t.Fatal("native state was not included in the layer budget")
	}
}
