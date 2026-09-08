package tool

import (
	"context"

	model "github.com/freesoulcode/foya/internal/model"
)

// ctxKey identifies tool values stored in a context.
type ctxKey int

const (
	ctxKeyCWD ctxKey = iota
	ctxKeyApproval
	ctxKeyModelRuntime
	ctxKeyProjectID
	ctxKeySessionID
	ctxKeyRunID
	ctxKeyDeferredTools
	ctxKeyActiveTools
)

// WithCWD stores the engine-selected working directory in a context.
func WithCWD(ctx context.Context, dir string) context.Context {
	return context.WithValue(ctx, ctxKeyCWD, dir)
}

// CWDFromContext returns the tool working directory.
func CWDFromContext(ctx context.Context) string {
	if d, ok := ctx.Value(ctxKeyCWD).(string); ok {
		return d
	}
	return ""
}

func WithProjectID(ctx context.Context, projectID string) context.Context {
	return context.WithValue(ctx, ctxKeyProjectID, projectID)
}

func ProjectIDFromContext(ctx context.Context) string {
	if id, ok := ctx.Value(ctxKeyProjectID).(string); ok {
		return id
	}
	return ""
}

// WithSessionID identifies the session that initiated a tool call.
func WithSessionID(ctx context.Context, sessionID string) context.Context {
	return context.WithValue(ctx, ctxKeySessionID, sessionID)
}

// SessionIDFromContext returns the session that initiated a tool call.
func SessionIDFromContext(ctx context.Context) string {
	if id, ok := ctx.Value(ctxKeySessionID).(string); ok {
		return id
	}
	return ""
}

// WithRunID identifies the root user turn that owns a tool call and every
// delegated descendant.
func WithRunID(ctx context.Context, runID string) context.Context {
	return context.WithValue(ctx, ctxKeyRunID, runID)
}

func RunIDFromContext(ctx context.Context) string {
	if id, ok := ctx.Value(ctxKeyRunID).(string); ok {
		return id
	}
	return ""
}

type DeferredToolActivator interface {
	ActivateTool(name string)
}

func WithDeferredToolActivator(ctx context.Context, activator DeferredToolActivator) context.Context {
	return context.WithValue(ctx, ctxKeyDeferredTools, activator)
}

func DeferredToolActivatorFromContext(ctx context.Context) (DeferredToolActivator, bool) {
	activator, ok := ctx.Value(ctxKeyDeferredTools).(DeferredToolActivator)
	return activator, ok
}

func WithActiveToolSnapshot(ctx context.Context, active map[string]bool) context.Context {
	return context.WithValue(ctx, ctxKeyActiveTools, active)
}

func ActiveToolFromContext(ctx context.Context, name string) bool {
	active, ok := ctx.Value(ctxKeyActiveTools).(map[string]bool)
	if !ok {
		return true
	}
	return active[name]
}

// ModelRuntime identifies the model and provider bound to the current turn.
type ModelRuntime struct {
	Provider model.Provider
	Model    string
}

func WithModelRuntime(ctx context.Context, runtime ModelRuntime) context.Context {
	return context.WithValue(ctx, ctxKeyModelRuntime, runtime)
}

func ModelRuntimeFromContext(ctx context.Context) (ModelRuntime, bool) {
	runtime, ok := ctx.Value(ctxKeyModelRuntime).(ModelRuntime)
	return runtime, ok
}
