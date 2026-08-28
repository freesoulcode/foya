package tool

import "context"

// ctxKey 是工具相关上下文值的键类型。
type ctxKey int

const (
	ctxKeyWorkspace ctxKey = iota
	ctxKeyApproval
)

// WithWorkspace 把工作目录注入上下文(由 engine 设置)。
func WithWorkspace(ctx context.Context, dir string) context.Context {
	return context.WithValue(ctx, ctxKeyWorkspace, dir)
}

// WorkspaceFromContext 取出工作目录(工具执行时用)。
func WorkspaceFromContext(ctx context.Context) string {
	if d, ok := ctx.Value(ctxKeyWorkspace).(string); ok {
		return d
	}
	return ""
}
