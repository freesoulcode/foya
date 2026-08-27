// Package approval 是审批网关:工具执行前决定自动放行 / 问用户 / 拒绝。
//
// 内核内部用 channel 同步阻塞实现,对外表现为「SSE 出请求 + REST 回决策」
// 的异步对,靠 requestID 关联。多客户端场景下,任一端(如手机)回决策,
// 内核解除阻塞并广播结果,其它端(如桌面)自动同步。
package approval

import "context"

// Mode 是审批档位。
type Mode string

const (
	ModeExplore Mode = "explore" // 只读探索,不问
	ModeAsk     Mode = "ask"     // 危险操作询问
	ModeBypass  Mode = "bypass"  // 不问(信任自动化)
)

// Decision 是审批决策。
type Decision string

const (
	DecisionAutoApprove       Decision = "auto_approve"
	DecisionApproved          Decision = "approved"
	DecisionApprovedForSession Decision = "approved_for_session"
	DecisionDenied            Decision = "denied"
)

// SafetyCheck 是策略判定的三态结果。
type Decision3 string

const (
	SafetyAutoApprove Decision3 = "auto_approve"
	SafetyAskUser     Decision3 = "ask_user"
	SafetyReject      Decision3 = "reject"
)

// Request 是一次审批请求。
type Request struct {
	ID       string
	Session  string
	ToolName string
	Action   string // read / write / execute
	Detail   string
}

// Gateway 是审批网关。
type Gateway interface {
	// Request 由工具内部就地调用;内部阻塞直到收到决策或 ctx 取消。
	Request(ctx context.Context, req Request) (Decision, error)
	// Resolve 由客户端经 REST 回执触发;按 requestID 解除阻塞。
	// 采用 take 语义:同一请求只有第一个决策生效(多端竞争安全)。
	Resolve(requestID string, d Decision)
}
