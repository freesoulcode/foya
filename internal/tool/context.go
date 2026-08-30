package tool

import (
	"context"

	"github.com/freesoulcode/foya/internal/provider"
)

// ctxKey 是工具相关上下文值的键类型。
type ctxKey int

const (
	ctxKeyCWD ctxKey = iota
	ctxKeyApproval
	ctxKeyModelRuntime
	ctxKeyProjectID
)

// WithCWD 把执行目录注入上下文(由 engine 设置)。
func WithCWD(ctx context.Context, dir string) context.Context {
	return context.WithValue(ctx, ctxKeyCWD, dir)
}

// CWDFromContext 取出工具执行目录。
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

// ModelRuntime 是当前会话回合绑定的模型与 Provider。
type ModelRuntime struct {
	Provider provider.Provider
	Model    string
}

func WithModelRuntime(ctx context.Context, runtime ModelRuntime) context.Context {
	return context.WithValue(ctx, ctxKeyModelRuntime, runtime)
}

func ModelRuntimeFromContext(ctx context.Context) (ModelRuntime, bool) {
	runtime, ok := ctx.Value(ctxKeyModelRuntime).(ModelRuntime)
	return runtime, ok
}
