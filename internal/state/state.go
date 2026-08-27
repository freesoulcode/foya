// Package state 实现「日志即真相」:一条带单调序号的追加事件日志是
// 唯一真相来源,会话 / UI / 上下文 / 恢复都是它的投影。
//
// 事件日志持久化为 JSONL(权威),SQLite 仅作派生查询索引,坏了能从
// 日志重建。上下文压缩只生成带 coverage 的投影 checkpoint,永不改源日志。
package state

import (
	"context"

	"github.com/freesoulcode/foya/internal/event"
)

// Log 是会话事件日志的追加与读取接口(权威真相)。
type Log interface {
	// Append 追加一个事件,返回其分配的单调序号。
	Append(ctx context.Context, ev event.Event) (event.Seq, error)
	// Read 从指定序号(不含)之后读取事件,用于 SSE 补发与投影重建。
	Read(ctx context.Context, session string, after event.Seq) ([]event.Event, error)
}

// Projection 从事件日志派生某种视图。
type Projection[V any] interface {
	// Project 将某会话到指定序号为止的事件投影成视图。
	Project(ctx context.Context, session string, upto event.Seq) (V, error)
}
