// 回合引擎实现:驱动 provider 完成多轮对话闭环。
package agent

import (
	"context"
	"sync"
	"time"

	"github.com/freesoulcode/foya/internal/broker"
	"github.com/freesoulcode/foya/internal/event"
	"github.com/freesoulcode/foya/internal/message"
	"github.com/freesoulcode/foya/internal/provider"
	"github.com/freesoulcode/foya/internal/state"
)

// Engine 是回合引擎的实现,驱动一轮对话:
// 用户消息入日志 → 投影历史 → 调 provider 流式 → 增量发事件 →
// 助手消息入日志 → 发 TurnComplete。历史累积即多轮上下文。
type Engine struct {
	log *state.MemLog
	bus *broker.Broker[event.Event]

	mu       sync.RWMutex // 保护 provider/model 的热替换
	provider provider.Provider
	model    string
}

// NewEngine 组装回合引擎。
func NewEngine(log *state.MemLog, bus *broker.Broker[event.Event], p provider.Provider, model string) *Engine {
	return &Engine{log: log, bus: bus, provider: p, model: model}
}

// SwitchProvider 运行时热替换 provider 与默认模型(供设置界面切换)。
func (e *Engine) SwitchProvider(p provider.Provider, model string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.provider = p
	e.model = model
}

// currentProvider 原子读取当前 provider 与模型。
func (e *Engine) currentProvider() (provider.Provider, string) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.provider, e.model
}

// RunTurn 同步执行一轮对话。事件通过 broker 按 session topic 广播。
func (e *Engine) RunTurn(ctx context.Context, sessionID, userText string) error {
	// 1. 用户消息写入日志(成为历史的一部分)。
	userMsg := message.Message{Role: message.RoleUser, Content: userText}
	e.emit(ctx, sessionID, event.KindMessageEnd, userMsg, true)

	// 2. 回合开始。
	e.emit(ctx, sessionID, event.KindTurnStarted, nil, true)

	// 3. 从日志投影完整历史(多轮上下文)。
	history, err := e.log.History(ctx, sessionID)
	if err != nil {
		return err
	}

	// 4. 调 provider 流式(读取当前 provider,支持运行时热替换)。
	prov, model := e.currentProvider()
	stream, err := prov.Stream(ctx, provider.Request{Model: model, Messages: history})
	if err != nil {
		e.emit(ctx, sessionID, event.KindError, err.Error(), true)
		return err
	}

	// 5. 消费流:每个 delta 有损广播,累积成完整助手消息。
	var acc string
	for ev := range stream {
		switch ev.Type {
		case "text_delta":
			acc += ev.Text
			e.bus.Publish(topic(sessionID), event.Event{
				Kind: event.KindMessageDelta, Session: sessionID,
				Time: time.Now(), Payload: ev.Text,
			})
		case "error":
			e.emit(ctx, sessionID, event.KindError, ev.Text, true)
			return nil
		case "done":
			// 结束,落历史。
		}
	}

	// 6. 助手消息完成,写入日志(成为下一轮的上下文)。
	asstMsg := message.Message{Role: message.RoleAssistant, Content: acc}
	e.emit(ctx, sessionID, event.KindMessageEnd, asstMsg, true)

	// 7. 回合结束(必达)。
	e.emit(ctx, sessionID, event.KindTurnComplete, nil, true)
	return nil
}

// emit 追加事件到日志并广播。mustDeliver 决定投递级别。
func (e *Engine) emit(ctx context.Context, sessionID string, kind event.Kind, payload any, mustDeliver bool) {
	ev := event.Event{Kind: kind, Session: sessionID, Time: time.Now(), Payload: payload}
	seq, _ := e.log.Append(ctx, ev)
	ev.Seq = seq
	if mustDeliver {
		_ = e.bus.PublishMustDeliver(ctx, topic(sessionID), ev)
	} else {
		e.bus.Publish(topic(sessionID), ev)
	}
}

// topic 是某会话的事件 topic。
func topic(sessionID string) string { return "session:" + sessionID }
