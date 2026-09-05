package compaction

import (
	"context"
	"errors"

	"github.com/freesoulcode/foya/internal/provider"
)

type ProjectionKind string

const (
	ProjectionText           ProjectionKind = "text"
	ProjectionProviderNative ProjectionKind = "provider_native"
)

var ErrCompactorUnavailable = errors.New("context compactor unavailable")

// Candidate is an unpersisted context projection returned by a Compactor.
type Candidate struct {
	Kind              ProjectionKind
	Text              string
	ProviderStateKind string
	ProviderState     []byte
	FinishReason      string
	Usage             *provider.Usage
}

// Compactor generates a candidate projection without mutating canonical state.
type Compactor interface {
	Kind() ProjectionKind
	Supports(provider.Provider) bool
	Compact(
		ctx context.Context,
		provider provider.Provider,
		request provider.Request,
	) (Candidate, error)
}

// TextCompactor is the portable fallback for every provider that supports
// short non-streaming completion.
type TextCompactor struct{}

func (TextCompactor) Kind() ProjectionKind { return ProjectionText }

func (TextCompactor) Supports(prov provider.Provider) bool {
	_, ok := prov.(provider.DetailedCompleter)
	return ok
}

func (TextCompactor) Compact(
	ctx context.Context,
	prov provider.Provider,
	request provider.Request,
) (Candidate, error) {
	if completer, ok := prov.(provider.DetailedCompleter); ok {
		result, err := completer.CompleteDetailed(ctx, request)
		if err != nil {
			return Candidate{}, err
		}
		return Candidate{
			Kind:         ProjectionText,
			Text:         result.Text,
			FinishReason: result.FinishReason,
			Usage:        result.Usage,
		}, nil
	}
	return Candidate{}, ErrCompactorUnavailable
}

// ProviderNativeCompactor delegates to a provider that owns both creation and
// replay semantics for an opaque context state.
type ProviderNativeCompactor struct{}

func (ProviderNativeCompactor) Kind() ProjectionKind { return ProjectionProviderNative }

func (ProviderNativeCompactor) Supports(prov provider.Provider) bool {
	_, ok := prov.(provider.NativeContextCompactor)
	return ok
}

func (ProviderNativeCompactor) Compact(
	ctx context.Context,
	prov provider.Provider,
	request provider.Request,
) (Candidate, error) {
	native, ok := prov.(provider.NativeContextCompactor)
	if !ok {
		return Candidate{}, ErrCompactorUnavailable
	}
	result, err := native.CompactContext(ctx, request)
	if err != nil {
		return Candidate{}, err
	}
	return Candidate{
		Kind:              ProjectionProviderNative,
		ProviderStateKind: result.State.Kind,
		ProviderState:     append([]byte(nil), result.State.Data...),
		Usage:             result.Usage,
	}, nil
}
