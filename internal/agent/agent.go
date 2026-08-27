// Package agent 是回合引擎:驱动「组装上下文 → 请求模型 → 解析工具调用
// → 执行 → 回灌 → 判断是否继续」的单回合循环。
//
// 回合是可取消的后台 goroutine,主循环永远能响应中断。每个 session
// 串行执行回合,多 session 并发。
package agent

import "context"

// TurnKind 标识回合类型。
type TurnKind string

const (
	TurnRegular  TurnKind = "regular"  // 常规聊天回合
	TurnCompact  TurnKind = "compact"  // 上下文压缩
	TurnSubAgent TurnKind = "subagent" // 子 agent
)

// StopReason 说明回合为何结束。
type StopReason string

const (
	StopEnd       StopReason = "stop"       // 模型正常结束
	StopToolCalls StopReason = "tool_calls" // 需执行工具后继续
	StopLength    StopReason = "length"     // 被 token 上限截断
	StopError     StopReason = "error"
	StopAborted   StopReason = "aborted" // 被取消
)

// TurnInput 是发起一个回合的输入。
type TurnInput struct {
	Message string // 占位:后续替换为结构化消息
}

// TurnResult 是一个回合的结果。
type TurnResult struct {
	NeedsFollowUp bool
	StopReason    StopReason
}

// Turn 是一个可取消的回合任务。
type Turn interface {
	Run(ctx context.Context, in TurnInput) (TurnResult, error)
	Kind() TurnKind
}

// Loop 是回合引擎:每 session 串行,多 session 并发。
type Loop interface {
	// Submit 提交一个回合;同 session 若在跑,按策略排队或抢占。
	Submit(sessionID string, in TurnInput) (runID string, err error)
	// Cancel 取消指定 session 的当前回合。
	Cancel(sessionID string)
	// Steer 在回合运行中插话。
	Steer(sessionID string, msg string)
}
