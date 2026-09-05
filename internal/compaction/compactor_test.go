package compaction

import (
	"context"
	"testing"

	"github.com/freesoulcode/foya/internal/provider"
)

type textCompactorProvider struct {
	completion provider.Completion
	native     *provider.NativeCompactionResult
}

func (p *textCompactorProvider) Name() string { return "test" }
func (p *textCompactorProvider) Stream(
	context.Context,
	provider.Request,
) (<-chan provider.StreamEvent, error) {
	ch := make(chan provider.StreamEvent)
	close(ch)
	return ch, nil
}
func (p *textCompactorProvider) CompleteDetailed(
	context.Context,
	provider.Request,
) (provider.Completion, error) {
	return p.completion, nil
}

func (p *textCompactorProvider) CompactContext(
	context.Context,
	provider.Request,
) (provider.NativeCompactionResult, error) {
	if p.native == nil {
		return provider.NativeCompactionResult{}, ErrCompactorUnavailable
	}
	return *p.native, nil
}

func TestTextCompactorPreservesCompletionMetadata(t *testing.T) {
	prov := &textCompactorProvider{completion: provider.Completion{
		Text:         "summary",
		FinishReason: "stop",
		Usage:        &provider.Usage{InputTokens: 10, OutputTokens: 2},
	}}
	compactor := TextCompactor{}
	if !compactor.Supports(prov) {
		t.Fatal("detailed completer was not supported")
	}
	candidate, err := compactor.Compact(
		context.Background(),
		prov,
		provider.Request{},
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
	prov := &textCompactorProvider{native: &provider.NativeCompactionResult{
		State: provider.ContextState{
			Kind: "openai.compaction",
			Data: []byte(`{"encrypted_content":"opaque"}`),
		},
		Usage: &provider.Usage{InputTokens: 20, OutputTokens: 5},
	}}
	compactor := ProviderNativeCompactor{}
	if !compactor.Supports(prov) {
		t.Fatal("native provider was not supported")
	}
	candidate, err := compactor.Compact(
		context.Background(),
		prov,
		provider.Request{},
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
