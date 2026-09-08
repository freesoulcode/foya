package conversation

import (
	"context"
	"testing"

	model "github.com/freesoulcode/foya/internal/model"
)

type textCompactorProvider struct {
	completion model.Completion
	native     *model.NativeCompactionResult
}

func (p *textCompactorProvider) Name() string { return "test" }
func (p *textCompactorProvider) Stream(
	context.Context,
	model.Request,
) (<-chan model.StreamEvent, error) {
	ch := make(chan model.StreamEvent)
	close(ch)
	return ch, nil
}
func (p *textCompactorProvider) CompleteDetailed(
	context.Context,
	model.Request,
) (model.Completion, error) {
	return p.completion, nil
}

func (p *textCompactorProvider) CompactContext(
	context.Context,
	model.Request,
) (model.NativeCompactionResult, error) {
	if p.native == nil {
		return model.NativeCompactionResult{}, ErrCompactorUnavailable
	}
	return *p.native, nil
}

func TestTextCompactorPreservesCompletionMetadata(t *testing.T) {
	prov := &textCompactorProvider{completion: model.Completion{
		Text:         "summary",
		FinishReason: "stop",
		Usage:        &model.Usage{InputTokens: 10, OutputTokens: 2},
	}}
	compactor := TextCompactor{}
	if !compactor.Supports(prov) {
		t.Fatal("detailed completer was not supported")
	}
	candidate, err := compactor.Compact(
		context.Background(),
		prov,
		model.Request{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if candidate.Kind != ProjectionText ||
		candidate.Text != "summary" ||
		candidate.FinishReason != "stop" ||
		candidate.Usage == nil ||
		candidate.Usage.InputTokens != 10 {
		t.Fatalf("candidate = %#v", candidate)
	}
}

func TestProviderNativeCompactorPreservesOpaqueState(t *testing.T) {
	prov := &textCompactorProvider{native: &model.NativeCompactionResult{
		State: model.ContextState{
			Kind: "openai.compaction",
			Data: []byte(`{"encrypted_content":"opaque"}`),
		},
		Usage: &model.Usage{InputTokens: 20, OutputTokens: 5},
	}}
	compactor := ProviderNativeCompactor{}
	if !compactor.Supports(prov) {
		t.Fatal("native provider was not supported")
	}
	candidate, err := compactor.Compact(
		context.Background(),
		prov,
		model.Request{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if candidate.Kind != ProjectionProviderNative ||
		candidate.ProviderStateKind != "openai.compaction" ||
		string(candidate.ProviderState) != `{"encrypted_content":"opaque"}` {
		t.Fatalf("candidate = %#v", candidate)
	}
}
