// Package backend 是传输无关的业务层:管理多连接、多会话、事件扇出、
// 实例级鉴权。它不关心底层是 Unix socket 还是 TCP,server 层把请求
// 转成对 Backend 的调用。
//
// 这是「一个内核多客户端」的落地关键:session 归 Backend 所有,多个
// 客户端连接可订阅同一 session,Backend 负责把事件扇出给所有订阅者。
package backend

import (
	"github.com/freesoulcode/foya/internal/agent"
	"github.com/freesoulcode/foya/internal/session"
)

// Backend 是内核业务的统一入口(传输无关)。
type Backend struct {
	sessions session.Manager
	loop     agent.Loop
}

// New 组装一个 Backend。
func New(sessions session.Manager, loop agent.Loop) *Backend {
	return &Backend{sessions: sessions, loop: loop}
}

// Sessions 暴露会话管理器。
func (b *Backend) Sessions() session.Manager { return b.sessions }

// Loop 暴露回合引擎。
func (b *Backend) Loop() agent.Loop { return b.loop }
