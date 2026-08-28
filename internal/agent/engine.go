// 回合引擎实现:驱动 provider 完成多轮对话与工具调用闭环。
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/freesoulcode/foya/internal/approval"
	"github.com/freesoulcode/foya/internal/broker"
	"github.com/freesoulcode/foya/internal/event"
	"github.com/freesoulcode/foya/internal/message"
	"github.com/freesoulcode/foya/internal/provider"
	"github.com/freesoulcode/foya/internal/session"
	"github.com/freesoulcode/foya/internal/state"
	"github.com/freesoulcode/foya/internal/title"
	"github.com/freesoulcode/foya/internal/tool"
)

const maxToolSteps = 50

// SessionLookup 是引擎读取会话元数据所需的最小依赖。
type SessionLookup interface {
	Get(id string) (*session.Session, bool)
}

// titleStore 是标题生成所需的会话存储。
type titleStore interface {
	SessionLookup
	SetGeneratedTitle(id, t string) (bool, error)
}

// pendingToolCall 在流式过程中累积一个工具调用的分片。
type pendingToolCall struct {
	ID        string
	Name      string
	argsBuf   string
	argsReady bool
}

func (p *pendingToolCall) input() json.RawMessage {
	if p.argsBuf == "" {
		return json.RawMessage("{}")
	}
	return json.RawMessage(p.argsBuf)
}

// Engine 是回合引擎。
type Engine struct {
	log      *state.MemLog
	bus      *broker.Broker[event.Event]
	sessions titleStore
	tools    tool.Registry
	approval approval.Gateway

	mu       sync.RWMutex
	provider provider.Provider
	model    string

	// cancels 持有每个会话当前回合的取消函数。回合进行中时存在,
	// 结束后删除。Cancel 据此中断正在跑的回合(provider HTTP、
	// 工具执行、审批等待都会随 ctx 取消而终止)。
	cancels sync.Map // sessionID -> context.CancelFunc
}

// NewEngine 组装回合引擎。
func NewEngine(
	log *state.MemLog,
	bus *broker.Broker[event.Event],
	sessions titleStore,
	p provider.Provider,
	model string,
	tools tool.Registry,
	gw approval.Gateway,
) *Engine {
	return &Engine{
		log:      log,
		bus:      bus,
		sessions: sessions,
		provider: p,
		model:    model,
		tools:    tools,
		approval: gw,
	}
}

// SwitchProvider 运行时热替换 provider 与默认模型。
func (e *Engine) SwitchProvider(p provider.Provider, model string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.provider = p
	e.model = model
}

func (e *Engine) currentProvider() (provider.Provider, string) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.provider, e.model
}

func (e *Engine) currentModel(sessionID string) string {
	if e.sessions != nil {
		if s, ok := e.sessions.Get(sessionID); ok && s.Model != "" {
			return s.Model
		}
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.model
}

// ListModels 列出当前 provider 可用的模型。
func (e *Engine) ListModels(ctx context.Context) ([]string, error) {
	prov, _ := e.currentProvider()
	lister, ok := prov.(provider.ModelLister)
	if !ok {
		return nil, fmt.Errorf("当前 provider 不支持列出模型")
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	return lister.ListModels(ctx)
}

// toolCallPayload 是 tool_begin/tool_end 事件的负载。
type toolCallPayload struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Input    string `json:"input,omitempty"`
	Output   string `json:"output,omitempty"`
	IsError  bool   `json:"is_error,omitempty"`
}

// RunTurn 同步执行一轮对话(可能含多步工具调用)。
// 同一时刻一个会话只能有一个回合;重复提交返回错误。可用 Cancel 中断。
func (e *Engine) RunTurn(ctx context.Context, sessionID, userText string) error {
	// 注册 per-session cancel:同一会话只允许一个活跃回合。
	turnCtx, cancel := context.WithCancel(ctx)
	if _, loaded := e.cancels.LoadOrStore(sessionID, cancel); loaded {
		cancel()
		return fmt.Errorf("该会话已有回合正在运行")
	}
	defer e.cancels.Delete(sessionID)
	defer cancel()
	ctx = turnCtx

	model := e.currentModel(sessionID)

	// 注入会话级配置到 context:工作目录供工具读,审批档位供网关读。
	if e.sessions != nil {
		ctx = tool.WithWorkspace(ctx, e.resolveWorkspace(sessionID))
		ctx = approval.WithMode(ctx, e.resolveApprovalMode(sessionID))
		ctx = approval.WithSession(ctx, sessionID)
	}

	// 标题生成(首条用户消息时后台触发)。
	if e.sessions != nil {
		if hist, err := e.log.History(ctx, sessionID); err == nil && !hasUserMessage(hist) {
			if s, ok := e.sessions.Get(sessionID); ok && s.Title == "" && !s.TitleIsManual {
				detached := context.WithoutCancel(ctx)
				go e.generateTitle(detached, sessionID, userText)
			}
		}
	}

	// 用户消息入日志。
	userMsg := message.Message{Role: message.RoleUser, Content: userText}
	e.emit(ctx, sessionID, event.KindMessageEnd, userMsg, true)
	e.emit(ctx, sessionID, event.KindTurnStarted, nil, true)

	// 多步循环:模型 → 工具 → 模型 ...
	for step := 0; step < maxToolSteps; step++ {
		history, err := e.log.History(ctx, sessionID)
		if err != nil {
			return err
		}
		toolDefs := e.tools.Specs()

		// 临时前置系统提示词(不写入日志,仅用于本次模型请求)。
		workspace := e.resolveWorkspace(sessionID)
		messages := append([]message.Message{
			{Role: message.RoleSystem, Content: buildSystemPrompt(workspace)},
		}, history...)

		prov, _ := e.currentProvider()
		stream, err := prov.Stream(ctx, provider.Request{
			Model:    model,
			Messages: messages,
			Tools:    toolDefs,
		})
		if err != nil {
			// ctx 取消(用户点停止)不算错误,只安静结束回合。
			if ctx.Err() != nil {
				e.emit(ctx, sessionID, event.KindTurnComplete, nil, true)
				return nil
			}
			e.emit(ctx, sessionID, event.KindError, err.Error(), true)
			e.emit(ctx, sessionID, event.KindTurnComplete, nil, true)
			return err
		}

		var accText string
		var accReasoning string
		pending := make(map[int]*pendingToolCall)
		var pendingOrder []int
		var finishReason string

		for ev := range stream {
			switch ev.Type {
			case "text_delta":
				accText += ev.Text
				e.bus.Publish(topic(sessionID), event.Event{
					Kind: event.KindMessageDelta, Session: sessionID,
					Time: time.Now(), Payload: ev.Text,
				})
			case "reasoning_delta":
				accReasoning += ev.Text
				e.bus.Publish(topic(sessionID), event.Event{
					Kind: event.KindReasoningDelta, Session: sessionID,
					Time: time.Now(), Payload: ev.Text,
				})
			case "tool_call_delta":
				pc, exists := pending[ev.ToolIndex]
				if !exists {
					pc = &pendingToolCall{ID: ev.ToolCallID, Name: ev.ToolName}
					pending[ev.ToolIndex] = pc
					pendingOrder = append(pendingOrder, ev.ToolIndex)
				}
				if ev.ToolCallID != "" {
					pc.ID = ev.ToolCallID
				}
				if ev.ToolName != "" {
					pc.Name = ev.ToolName
				}
				if ev.ToolArgsDlt != "" {
					pc.argsBuf += ev.ToolArgsDlt
				}
			case "error":
				// ctx 被取消(用户点停止):安静结束回合,不弹错误气泡。
				if ctx.Err() != nil {
					e.emit(ctx, sessionID, event.KindTurnComplete, nil, true)
					return nil
				}
				e.emit(ctx, sessionID, event.KindError, ev.Text, true)
				e.emit(ctx, sessionID, event.KindTurnComplete, nil, true)
				return nil
			case "done":
				finishReason = ev.FinishReason
			}
		}

		// 组装助手消息(可能携带 tool_calls)。
		asstMsg := message.Message{Role: message.RoleAssistant, Content: accText, Reasoning: accReasoning}
		var toolCalls []message.ToolCall
		for _, idx := range pendingOrder {
			pc := pending[idx]
			toolCalls = append(toolCalls, message.ToolCall{
				ID:    pc.ID,
				Name:  pc.Name,
				Input: pc.input(),
			})
		}
		if len(toolCalls) > 0 {
			asstMsg.ToolCalls = toolCalls
		}
		e.emit(ctx, sessionID, event.KindMessageEnd, asstMsg, true)

		// 没有工具调用,回合结束。
		if finishReason != "tool_calls" || len(toolCalls) == 0 {
			break
		}

		// 执行每个工具调用,结果作为 tool 消息入日志。
		for i, tc := range toolCalls {
			e.emit(ctx, sessionID, event.KindToolBegin, toolCallPayload{
				ID: tc.ID, Name: tc.Name, Input: string(tc.Input),
			}, true)

			// 回合被取消(用户点停止):正在执行的工具标记为「已中断」而非错误,
			// 未开始的工具也补占位结果,保证日志中每个 tool_call 都有对应 tool 消息。
			output := "已中断"
			isErr := false
			if ctx.Err() == nil {
				result := e.executeTool(ctx, tc)
				output = resultText(result)
				isErr = result.IsError
				if ctx.Err() != nil {
					output = "已中断"
					isErr = false
				}
			}
			e.emit(ctx, sessionID, event.KindToolEnd, toolCallPayload{
				ID: tc.ID, Name: tc.Name, Output: output, IsError: isErr,
			}, true)

			toolMsg := message.Message{
				Role:       message.RoleTool,
				ToolCallID: tc.ID,
				Content:    output,
			}
			e.emit(ctx, sessionID, event.KindMessageEnd, toolMsg, true)

			if ctx.Err() != nil {
				for _, rest := range toolCalls[i+1:] {
					e.emit(ctx, sessionID, event.KindToolBegin, toolCallPayload{
						ID: rest.ID, Name: rest.Name, Input: string(rest.Input),
					}, true)
					e.emit(ctx, sessionID, event.KindToolEnd, toolCallPayload{
						ID: rest.ID, Name: rest.Name, Output: "已中断",
					}, true)
					e.emit(ctx, sessionID, event.KindMessageEnd, message.Message{
						Role: message.RoleTool, ToolCallID: rest.ID, Content: "已中断",
					}, true)
				}
				e.emit(ctx, sessionID, event.KindTurnComplete, nil, true)
				return nil
			}
		}
	}

	e.emit(ctx, sessionID, event.KindTurnComplete, nil, true)
	return nil
}

// Cancel 中断指定会话当前正在运行的回合(若有)。
// 取消会传播到 provider HTTP 请求、工具执行、审批等待。无活跃回合时 no-op。
func (e *Engine) Cancel(sessionID string) {
	if v, ok := e.cancels.Load(sessionID); ok {
		v.(context.CancelFunc)()
	}
}

// executeTool 查注册表并执行单个工具调用。
func (e *Engine) executeTool(ctx context.Context, tc message.ToolCall) tool.Result {
	t, ok := e.tools.Get(tc.Name)
	if !ok {
		return tool.Result{IsError: true, Content: []tool.ContentPart{{Type: "text", Text: "unknown tool: " + tc.Name}}}
	}
	call := tool.Call{ID: tc.ID, Name: tc.Name, Input: tc.Input}
	result, err := t.Run(ctx, call)
	if err != nil {
		return tool.Result{IsError: true, Content: []tool.ContentPart{{Type: "text", Text: err.Error()}}}
	}
	return result
}

// resultText 把工具结果拼成文本,用于回灌模型和事件负载。
func resultText(r tool.Result) string {
	var sb string
	for _, p := range r.Content {
		if p.Type == "text" && p.Text != "" {
			if sb != "" {
				sb += "\n"
			}
			sb += p.Text
		}
	}
	if sb == "" {
		sb = "(no output)"
	}
	if r.IsError {
		sb = "Error: " + sb
	}
	return sb
}

// resolveWorkspace / resolveApprovalMode 从会话状态读取实时配置。
func (e *Engine) resolveWorkspace(sessionID string) string {
	if s, ok := e.sessions.Get(sessionID); ok {
		return s.Workspace
	}
	return ""
}

func (e *Engine) resolveApprovalMode(sessionID string) approval.Mode {
	if s, ok := e.sessions.Get(sessionID); ok && s.ApprovalMode != "" {
		return approval.Mode(s.ApprovalMode)
	}
	return approval.ModeAsk
}

// emit 追加事件到日志并广播。
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

func topic(sessionID string) string { return "session:" + sessionID }

func hasUserMessage(msgs []message.Message) bool {
	for _, m := range msgs {
		if m.Role == message.RoleUser {
			return true
		}
	}
	return false
}

// generateTitle 在后台生成会话标题。
func (e *Engine) generateTitle(ctx context.Context, sessionID, userText string) {
	prov, _ := e.currentProvider()
	model := e.currentModel(sessionID)
	var generated string
	if c, ok := prov.(provider.Completer); ok {
		generated = title.Generate(ctx, c, model, userText)
	}
	if generated == "" {
		generated = title.Fallback(userText)
	}
	ok, err := e.sessions.SetGeneratedTitle(sessionID, generated)
	if err != nil || !ok {
		return
	}
	s, exists := e.sessions.Get(sessionID)
	if !exists {
		return
	}
	e.emit(ctx, sessionID, event.KindSessionUpdated, s, true)
}
