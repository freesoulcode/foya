// Package server 暴露内核的 REST + SSE 接口。
//
// 指令走 REST,事件流走 SSE。传输管道由 config 决定(本地 Unix socket /
// 远端 TCP+TLS),协议不变。
package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"

	"github.com/freesoulcode/foya/internal/agent"
	"github.com/freesoulcode/foya/internal/backend"
	"github.com/freesoulcode/foya/internal/config"
	"github.com/freesoulcode/foya/internal/event"
	"github.com/freesoulcode/foya/internal/protocol"
	"github.com/freesoulcode/foya/internal/session"
	"github.com/freesoulcode/foya/internal/terminal"
)

// Server 承载 REST + SSE 路由。
type Server struct {
	cfg     config.Config
	backend *backend.Backend
	mux     *http.ServeMux
}

// New 组装一个 Server。
func New(cfg config.Config, be *backend.Backend) *Server {
	s := &Server{cfg: cfg, backend: be, mux: http.NewServeMux()}
	s.routes()
	return s
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /healthz", s.handleHealth)
	s.mux.HandleFunc("GET /connections", s.handleListConnections)
	s.mux.HandleFunc("POST /connections", s.handleCreateConnection)
	s.mux.HandleFunc("PATCH /connections/{id}", s.handleUpdateConnection)
	s.mux.HandleFunc("DELETE /connections/{id}", s.handleDeleteConnection)
	s.mux.HandleFunc("GET /connections/{id}/models", s.handleListConnectionModels)
	s.mux.HandleFunc("POST /sessions", s.handleCreateSession)
	s.mux.HandleFunc("GET /sessions", s.handleListSessions)
	s.mux.HandleFunc("PATCH /sessions/{id}", s.handleUpdateSession)
	s.mux.HandleFunc("DELETE /sessions/{id}", s.handleDeleteSession)
	s.mux.HandleFunc("GET /sessions/{id}/events", s.handleEvents)
	s.mux.HandleFunc("GET /sessions/{id}/history", s.handleHistory)
	s.mux.HandleFunc("GET /sessions/{id}/usage", s.handleUsage)
	s.mux.HandleFunc("POST /sessions/{id}/turns", s.handleSubmitTurn)
	s.mux.HandleFunc("POST /sessions/{id}/turns/{message_seq}/edit", s.handleEditTurn)
	s.mux.HandleFunc("POST /sessions/{id}/compact", s.handleCompactSession)
	s.mux.HandleFunc("POST /sessions/{id}/cancel", s.handleCancelTurn)
	s.mux.HandleFunc("GET /sessions/{id}/queue", s.handleListQueue)
	s.mux.HandleFunc("POST /sessions/{id}/queue", s.handleEnqueueMessage)
	s.mux.HandleFunc("PATCH /sessions/{id}/queue/{message_id}", s.handleUpdateQueuedMessage)
	s.mux.HandleFunc("DELETE /sessions/{id}/queue/{message_id}", s.handleDeleteQueuedMessage)
	s.mux.HandleFunc("POST /sessions/{id}/queue/{message_id}/dispatch", s.handleDispatchQueuedMessage)
	s.mux.HandleFunc("POST /sessions/{id}/approvals/{request_id}", s.handleResolveApproval)
	s.mux.HandleFunc("POST /sessions/{id}/terminals", s.handleStartTerminal)
	s.mux.HandleFunc("GET /sessions/{id}/terminals/{ref}", s.handleAttachTerminal)
	s.mux.HandleFunc("POST /sessions/{id}/terminals/{ref}/input", s.handleWriteTerminal)
	s.mux.HandleFunc("POST /sessions/{id}/terminals/{ref}/resize", s.handleResizeTerminal)
	s.mux.HandleFunc("DELETE /sessions/{id}/terminals/{ref}", s.handleStopTerminal)
	s.mux.HandleFunc("GET /sessions/{id}/terminals/{ref}/events", s.handleTerminalEvents)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

// handleCreateSession 新建会话。未指定模型时回退到默认 Connection 的模型。
func (s *Server) handleCreateSession(w http.ResponseWriter, r *http.Request) {
	var req protocol.CreateSessionRequest
	// 兼容空 body(旧客户端):忽略解码错误。
	_ = json.NewDecoder(r.Body).Decode(&req)

	approval := req.ApprovalMode
	if approval == "" {
		approval = "ask"
	}

	sess, err := s.backend.CreateSession(session.CreateOptions{
		ConnectionID:    req.ConnectionID,
		Model:           req.Model,
		ReasoningEffort: session.ReasoningEffort(req.ReasoningEffort),
		Workspace:       req.Workspace,
		ApprovalMode:    approval,
	})
	if err != nil {
		if errors.Is(err, session.ErrInvalidReasoningEffort) {
			writeErr(w, http.StatusBadRequest, "invalid_reasoning_effort", err.Error())
			return
		}
		if errors.Is(err, backend.ErrConnectionNotFound) {
			writeErr(w, http.StatusBadRequest, "connection_not_found", err.Error())
			return
		}
		writeErr(w, http.StatusInternalServerError, "create_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, sess)
}

// handleUpdateSession 局部更新会话可变字段(模型/工作目录/审批档位/标题),
// 供会话进行中实时切换审批档位、手动改名等场景。更新对后续工具调用立即生效。
func (s *Server) handleUpdateSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req protocol.UpdateSessionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}

	// 手动改名走 RenameSession(置 TitleIsManual 并广播)。
	if req.Title != nil {
		if _, err := s.backend.RenameSession(r.Context(), id, *req.Title); err != nil {
			if errors.Is(err, session.ErrNotFound) {
				writeErr(w, http.StatusNotFound, "not_found", err.Error())
				return
			}
			writeErr(w, http.StatusInternalServerError, "rename_failed", err.Error())
			return
		}
	}

	// 置顶/取消置顶走 PinSession(单独记录 PinnedAt 并广播)。
	if req.Pinned != nil {
		if _, err := s.backend.PinSession(r.Context(), id, *req.Pinned); err != nil {
			if errors.Is(err, session.ErrNotFound) {
				writeErr(w, http.StatusNotFound, "not_found", err.Error())
				return
			}
			writeErr(w, http.StatusInternalServerError, "pin_failed", err.Error())
			return
		}
	}

	// 其余字段走局部更新;无字段时直接返回当前会话。
	sess, err := s.backend.UpdateSession(
		r.Context(),
		id,
		req.ConnectionID,
		req.Model,
		req.ReasoningEffort,
		req.Workspace,
		req.ApprovalMode,
	)
	if err != nil {
		if errors.Is(err, session.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "not_found", err.Error())
			return
		}
		if errors.Is(err, session.ErrWorkspaceLocked) {
			writeErr(w, http.StatusConflict, "workspace_locked", err.Error())
			return
		}
		if errors.Is(err, session.ErrInvalidReasoningEffort) {
			writeErr(w, http.StatusBadRequest, "invalid_reasoning_effort", err.Error())
			return
		}
		if errors.Is(err, backend.ErrConnectionNotFound) {
			writeErr(w, http.StatusBadRequest, "connection_not_found", err.Error())
			return
		}
		writeErr(w, http.StatusInternalServerError, "update_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, sess)
}

func publicConnection(connection config.Connection) protocol.ConnectionConfig {
	return protocol.ConnectionConfig{
		ID:           connection.ID,
		Name:         connection.Name,
		Kind:         connection.Kind,
		AuthKind:     connection.AuthKind,
		BaseURL:      connection.BaseURL,
		HasAPIKey:    connection.APIKey != "",
		DefaultModel: connection.DefaultModel,
		SortOrder:    connection.SortOrder,
	}
}

func toConnection(input protocol.ConnectionConfig) config.Connection {
	return config.Connection{
		ID:           input.ID,
		Name:         input.Name,
		Kind:         input.Kind,
		AuthKind:     input.AuthKind,
		BaseURL:      input.BaseURL,
		APIKey:       input.APIKey,
		DefaultModel: input.DefaultModel,
		SortOrder:    input.SortOrder,
	}
}

func (s *Server) handleListConnections(w http.ResponseWriter, _ *http.Request) {
	connections := s.backend.Connections()
	result := make([]protocol.ConnectionConfig, 0, len(connections))
	for _, connection := range connections {
		result = append(result, publicConnection(connection))
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleCreateConnection(w http.ResponseWriter, r *http.Request) {
	var input protocol.ConnectionConfig
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	connection, err := s.backend.CreateConnection(toConnection(input))
	if err != nil {
		if errors.Is(err, backend.ErrUnsupportedAuth) {
			writeErr(w, http.StatusBadRequest, "unsupported_auth_kind", err.Error())
			return
		}
		writeErr(w, http.StatusConflict, "connection_create_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, publicConnection(connection))
}

func (s *Server) handleUpdateConnection(w http.ResponseWriter, r *http.Request) {
	var input protocol.ConnectionConfig
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	connection, err := s.backend.UpdateConnection(r.PathValue("id"), toConnection(input))
	if err != nil {
		if errors.Is(err, backend.ErrConnectionNotFound) {
			writeErr(w, http.StatusNotFound, "connection_not_found", err.Error())
			return
		}
		if errors.Is(err, backend.ErrUnsupportedAuth) {
			writeErr(w, http.StatusBadRequest, "unsupported_auth_kind", err.Error())
			return
		}
		writeErr(w, http.StatusBadRequest, "connection_update_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, publicConnection(connection))
}

func (s *Server) handleDeleteConnection(w http.ResponseWriter, r *http.Request) {
	err := s.backend.DeleteConnection(r.PathValue("id"))
	if err != nil {
		switch {
		case errors.Is(err, backend.ErrConnectionNotFound):
			writeErr(w, http.StatusNotFound, "connection_not_found", err.Error())
		case errors.Is(err, backend.ErrConnectionInUse):
			writeErr(w, http.StatusConflict, "connection_in_use", err.Error())
		default:
			writeErr(w, http.StatusBadRequest, "connection_delete_failed", err.Error())
		}
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleListConnectionModels(w http.ResponseWriter, r *http.Request) {
	models, err := s.backend.ListModels(r.Context(), r.PathValue("id"))
	if err != nil {
		if errors.Is(err, backend.ErrConnectionNotFound) {
			writeErr(w, http.StatusNotFound, "connection_not_found", err.Error())
			return
		}
		writeErr(w, http.StatusBadGateway, "models_failed", err.Error())
		return
	}
	ids := make([]string, 0, len(models))
	contextWindows := make(map[string]int64)
	for _, model := range models {
		ids = append(ids, model.ID)
		if model.ContextWindow > 0 {
			contextWindows[model.ID] = model.ContextWindow
		}
	}
	writeJSON(w, http.StatusOK, protocol.ConnectionModelsResponse{
		Models:         ids,
		ContextWindows: contextWindows,
	})
}

// handleDeleteSession 删除会话:中断正在跑的回合、清除元数据与事件日志,
// 并广播 session_deleted 让所有已连接客户端移除该会话。
func (s *Server) handleDeleteSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.backend.DeleteSession(r.Context(), id); err != nil {
		if errors.Is(err, session.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "not_found", err.Error())
			return
		}
		writeErr(w, http.StatusInternalServerError, "delete_failed", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleListSessions 列出会话。
func (s *Server) handleListSessions(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.backend.ListSessions())
}

// handleHistory 返回某会话的对话历史(从事件日志投影)。
func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	msgs, err := s.backend.History(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "history_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, msgs)
}

// handleUsage 返回会话最近一次模型请求的 token 使用情况。
func (s *Server) handleUsage(w http.ResponseWriter, r *http.Request) {
	usage, err := s.backend.Usage(r.Context(), r.PathValue("id"))
	if err != nil {
		if errors.Is(err, session.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "not_found", err.Error())
			return
		}
		writeErr(w, http.StatusInternalServerError, "usage_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, usage)
}

// handleSubmitTurn 原子提交消息:空闲时立即启动,运行时进入 FIFO 队列。
// Backend 自己持有后台 runner,请求返回不影响回合生命周期。
func (s *Server) handleSubmitTurn(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req protocol.SubmitTurnRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	result, err := s.backend.SubmitTurn(r.Context(), id, req.Message)
	if err != nil {
		writeQueueErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, protocol.SubmitTurnResponse{
		RunID:  id,
		Status: result.Status,
		Queued: result.Queued,
	})
}

// handleEditTurn branches before an active user message and runs its edited
// replacement. Potential workspace effects require an explicit second request.
func (s *Server) handleEditTurn(w http.ResponseWriter, r *http.Request) {
	rawSeq := r.PathValue("message_seq")
	seq, err := strconv.ParseUint(rawSeq, 10, 64)
	if err != nil || seq == 0 {
		writeErr(w, http.StatusBadRequest, "invalid_message_seq", "invalid message sequence")
		return
	}
	var req protocol.EditTurnRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	result, err := s.backend.EditTurn(
		r.Context(),
		r.PathValue("id"),
		event.Seq(seq),
		req.Message,
		req.ConfirmEffects,
		event.Seq(req.ExpectedHeadSeq),
	)
	if err != nil {
		switch {
		case errors.Is(err, session.ErrNotFound):
			writeErr(w, http.StatusNotFound, "not_found", err.Error())
		case errors.Is(err, backend.ErrActiveUserMessageNotFound):
			writeErr(w, http.StatusNotFound, "message_not_found", err.Error())
		case errors.Is(err, backend.ErrEmptyMessage):
			writeErr(w, http.StatusBadRequest, "empty_message", err.Error())
		case errors.Is(err, backend.ErrMessageUnchanged):
			writeErr(w, http.StatusConflict, "message_unchanged", err.Error())
		case errors.Is(err, backend.ErrSessionBusy):
			writeErr(w, http.StatusConflict, "session_busy", err.Error())
		case errors.Is(err, backend.ErrSessionQueueNotEmpty):
			writeErr(w, http.StatusConflict, "queue_not_empty", err.Error())
		case errors.Is(err, backend.ErrHistoryChanged):
			writeErr(w, http.StatusConflict, "history_changed", err.Error())
		default:
			writeErr(w, http.StatusInternalServerError, "edit_failed", err.Error())
		}
		return
	}
	writeJSON(w, http.StatusOK, protocol.EditTurnResponse{
		Status:  result.Status,
		Effects: result.Effects,
		HeadSeq: uint64(result.HeadSeq),
	})
}

// handleCompactSession manually compacts completed history for an idle session.
func (s *Server) handleCompactSession(w http.ResponseWriter, r *http.Request) {
	checkpoint, err := s.backend.CompactSession(r.Context(), r.PathValue("id"))
	if err != nil {
		switch {
		case errors.Is(err, session.ErrNotFound):
			writeErr(w, http.StatusNotFound, "not_found", err.Error())
		case errors.Is(err, backend.ErrSessionBusy):
			writeErr(w, http.StatusConflict, "session_busy", err.Error())
		case errors.Is(err, agent.ErrNothingToCompact):
			writeErr(w, http.StatusConflict, "nothing_to_compact", err.Error())
		case errors.Is(err, agent.ErrCompactionUnavailable):
			writeErr(w, http.StatusNotImplemented, "compaction_unavailable", err.Error())
		default:
			writeErr(w, http.StatusBadGateway, "compaction_failed", err.Error())
		}
		return
	}
	writeJSON(w, http.StatusOK, protocol.CompactSessionResponse{
		ThroughSeq:            uint64(checkpoint.ThroughSeq),
		EstimatedTokensBefore: checkpoint.EstimatedTokensBefore,
		EstimatedTokensAfter:  checkpoint.EstimatedTokensAfter,
	})
}

// handleCancelTurn 取消该会话当前正在运行的回合(用户点停止)。
func (s *Server) handleCancelTurn(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s.backend.CancelTurn(id)
	w.WriteHeader(http.StatusNoContent)
}

// handleListQueue returns the current ordered queue snapshot.
func (s *Server) handleListQueue(w http.ResponseWriter, r *http.Request) {
	items, err := s.backend.ListQueuedMessages(r.PathValue("id"))
	if err != nil {
		writeQueueErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

// handleEnqueueMessage explicitly appends a message without starting it.
func (s *Server) handleEnqueueMessage(w http.ResponseWriter, r *http.Request) {
	var req protocol.QueueMessageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	item, err := s.backend.EnqueueMessage(r.Context(), r.PathValue("id"), req.Message)
	if err != nil {
		writeQueueErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

// handleUpdateQueuedMessage edits text and/or moves an item.
func (s *Server) handleUpdateQueuedMessage(w http.ResponseWriter, r *http.Request) {
	var req protocol.UpdateQueuedMessageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	item, err := s.backend.UpdateQueuedMessage(
		r.Context(),
		r.PathValue("id"),
		r.PathValue("message_id"),
		req.Message,
		req.Position,
	)
	if err != nil {
		writeQueueErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

// handleDeleteQueuedMessage removes one pending item.
func (s *Server) handleDeleteQueuedMessage(w http.ResponseWriter, r *http.Request) {
	err := s.backend.DeleteQueuedMessage(
		r.Context(),
		r.PathValue("id"),
		r.PathValue("message_id"),
	)
	if err != nil {
		writeQueueErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleDispatchQueuedMessage interrupts the active turn and prioritizes the
// selected item, or starts it directly when the session is idle.
func (s *Server) handleDispatchQueuedMessage(w http.ResponseWriter, r *http.Request) {
	item, err := s.backend.DispatchQueuedMessage(
		r.Context(),
		r.PathValue("id"),
		r.PathValue("message_id"),
	)
	if err != nil {
		writeQueueErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

// handleResolveApproval 接收客户端的审批决策(批准/拒绝)。
func (s *Server) handleResolveApproval(w http.ResponseWriter, r *http.Request) {
	requestID := r.PathValue("request_id")
	var req protocol.ApprovalDecisionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if req.RequestID == "" {
		req.RequestID = requestID
	}
	s.backend.ResolveApproval(req.RequestID, req.Decision)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleStartTerminal(w http.ResponseWriter, r *http.Request) {
	var req protocol.TerminalStartRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	resource, err := s.backend.StartTerminal(
		r.Context(),
		r.PathValue("id"),
		req.Cols,
		req.Rows,
	)
	if err != nil {
		writeTerminalErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, resource)
}

func (s *Server) handleAttachTerminal(w http.ResponseWriter, r *http.Request) {
	resource, err := s.backend.AttachTerminal(r.PathValue("id"), r.PathValue("ref"))
	if err != nil {
		writeTerminalErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resource)
}

func (s *Server) handleWriteTerminal(w http.ResponseWriter, r *http.Request) {
	var req protocol.TerminalInputRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if err := s.backend.WriteTerminal(r.PathValue("id"), r.PathValue("ref"), req.Input); err != nil {
		writeTerminalErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleResizeTerminal(w http.ResponseWriter, r *http.Request) {
	var req protocol.TerminalResizeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if err := s.backend.ResizeTerminal(
		r.PathValue("id"),
		r.PathValue("ref"),
		req.Cols,
		req.Rows,
	); err != nil {
		writeTerminalErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleStopTerminal(w http.ResponseWriter, r *http.Request) {
	if err := s.backend.StopTerminal(r.PathValue("id"), r.PathValue("ref")); err != nil {
		writeTerminalErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleTerminalEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, "no_flush", "streaming unsupported")
		return
	}
	after, _ := strconv.ParseUint(r.URL.Query().Get("after"), 10, 64)
	ch, err := s.backend.SubscribeTerminal(
		r.Context(),
		r.PathValue("id"),
		r.PathValue("ref"),
		after,
	)
	if err != nil {
		writeTerminalErr(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher.Flush()

	for {
		select {
		case <-r.Context().Done():
			return
		case ev, ok := <-ch:
			if !ok {
				return
			}
			data, _ := json.Marshal(ev)
			fmt.Fprintf(w, "id: %d\nevent: terminal\ndata: %s\n\n", ev.Seq, data)
			flusher.Flush()
		}
	}
}

// handleEvents 以 SSE 推送某会话的事件流。
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, "no_flush", "streaming unsupported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ctx := r.Context()
	ch := s.backend.Subscribe(ctx, id)
	flusher.Flush() // 立即回响应头,让客户端确认订阅就绪

	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-ch:
			if !ok {
				return
			}
			data, _ := json.Marshal(ev)
			// SSE 帧:id 用事件序号(支持 Last-Event-ID 补发)。
			fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", ev.Seq, ev.Kind, data)
			flusher.Flush()
		}
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, errCode, msg string) {
	writeJSON(w, code, protocol.ErrorResponse{Code: errCode, Message: msg})
}

func writeQueueErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, session.ErrNotFound):
		writeErr(w, http.StatusNotFound, "not_found", err.Error())
	case errors.Is(err, backend.ErrQueuedMessageNotFound):
		writeErr(w, http.StatusNotFound, "queued_message_not_found", err.Error())
	case errors.Is(err, backend.ErrEmptyMessage),
		errors.Is(err, backend.ErrInvalidQueuePosition):
		writeErr(w, http.StatusBadRequest, "invalid_queue_message", err.Error())
	default:
		writeErr(w, http.StatusInternalServerError, "queue_failed", err.Error())
	}
}

func writeTerminalErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, session.ErrNotFound), errors.Is(err, terminal.ErrNotFound):
		writeErr(w, http.StatusNotFound, "terminal_not_found", err.Error())
	case errors.Is(err, terminal.ErrUnavailable):
		writeErr(w, http.StatusNotImplemented, "terminal_unavailable", err.Error())
	default:
		writeErr(w, http.StatusInternalServerError, "terminal_failed", err.Error())
	}
}

// Handler 返回 http.Handler,便于挂到任意监听器(Unix socket / TCP)。
func (s *Server) Handler() http.Handler { return s.mux }
