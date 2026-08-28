// Package server 暴露内核的 REST + SSE 接口。
//
// 指令走 REST,事件流走 SSE。传输管道由 config 决定(本地 Unix socket /
// 远端 TCP+TLS),协议不变。
package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/freesoulcode/foya/internal/backend"
	"github.com/freesoulcode/foya/internal/config"
	"github.com/freesoulcode/foya/internal/protocol"
	"github.com/freesoulcode/foya/internal/session"
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
	s.mux.HandleFunc("POST /sessions", s.handleCreateSession)
	s.mux.HandleFunc("GET /sessions", s.handleListSessions)
	s.mux.HandleFunc("PATCH /sessions/{id}", s.handleUpdateSession)
	s.mux.HandleFunc("DELETE /sessions/{id}", s.handleDeleteSession)
	s.mux.HandleFunc("GET /sessions/{id}/events", s.handleEvents)
	s.mux.HandleFunc("GET /sessions/{id}/history", s.handleHistory)
	s.mux.HandleFunc("GET /sessions/{id}/usage", s.handleUsage)
	s.mux.HandleFunc("POST /sessions/{id}/turns", s.handleSubmitTurn)
	s.mux.HandleFunc("POST /sessions/{id}/cancel", s.handleCancelTurn)
	s.mux.HandleFunc("GET /sessions/{id}/queue", s.handleListQueue)
	s.mux.HandleFunc("POST /sessions/{id}/queue", s.handleEnqueueMessage)
	s.mux.HandleFunc("PATCH /sessions/{id}/queue/{message_id}", s.handleUpdateQueuedMessage)
	s.mux.HandleFunc("DELETE /sessions/{id}/queue/{message_id}", s.handleDeleteQueuedMessage)
	s.mux.HandleFunc("POST /sessions/{id}/queue/{message_id}/dispatch", s.handleDispatchQueuedMessage)
	s.mux.HandleFunc("POST /sessions/{id}/approvals/{request_id}", s.handleResolveApproval)
	s.mux.HandleFunc("GET /config/provider", s.handleGetProvider)
	s.mux.HandleFunc("PUT /config/provider", s.handleSetProvider)
	s.mux.HandleFunc("GET /config/models", s.handleListModels)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

// handleCreateSession 新建会话。可在请求体中指定模型、工作目录、审批档位;
// 未指定模型时回退到当前 provider 的默认模型,未指定审批档位时默认为 ask。
func (s *Server) handleCreateSession(w http.ResponseWriter, r *http.Request) {
	var req protocol.CreateSessionRequest
	// 兼容空 body(旧客户端):忽略解码错误。
	_ = json.NewDecoder(r.Body).Decode(&req)

	model := req.Model
	if model == "" {
		model = s.backend.ProviderConfig().Model
	}
	approval := req.ApprovalMode
	if approval == "" {
		approval = "ask"
	}

	sess, err := s.backend.CreateSession(session.CreateOptions{
		Model:        model,
		Workspace:    req.Workspace,
		ApprovalMode: approval,
	})
	if err != nil {
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
	sess, err := s.backend.UpdateSession(id, req.Model, req.Workspace, req.ApprovalMode)
	if err != nil {
		if errors.Is(err, session.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "not_found", err.Error())
			return
		}
		writeErr(w, http.StatusInternalServerError, "update_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, sess)
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

// handleGetProvider 返回当前 provider 配置(key 脱敏)。
func (s *Server) handleGetProvider(w http.ResponseWriter, r *http.Request) {
	pc := s.backend.ProviderConfig()
	writeJSON(w, http.StatusOK, protocol.ProviderConfig{
		Kind:      pc.Kind,
		BaseURL:   pc.BaseURL,
		Model:     pc.Model,
		HasAPIKey: pc.APIKey != "",
	})
}

// handleSetProvider 热替换 provider 配置。
// 未携带 api_key 时保留原有 key(避免脱敏读取后回写清空)。
func (s *Server) handleSetProvider(w http.ResponseWriter, r *http.Request) {
	var req protocol.ProviderConfig
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	cur := s.backend.ProviderConfig()
	key := req.APIKey
	if key == "" {
		key = cur.APIKey // 保留原 key
	}
	kind := "openai"
	s.backend.SetProviderConfig(config.Provider{
		Kind:    kind,
		BaseURL: req.BaseURL,
		APIKey:  key,
		Model:   req.Model,
	})
	writeJSON(w, http.StatusOK, protocol.ProviderConfig{
		Kind: kind, BaseURL: req.BaseURL, Model: req.Model, HasAPIKey: key != "",
	})
}

// handleListModels 用当前配置的 base_url + api_key 代求 provider 的 /models 接口,
// 返回统一的模型 ID 列表。失败时回传明确错误(如端点不支持、未配置 key)。
func (s *Server) handleListModels(w http.ResponseWriter, r *http.Request) {
	models, err := s.backend.ListModels(r.Context())
	if err != nil {
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
	writeJSON(w, http.StatusOK, protocol.ModelsResponse{
		Models:         ids,
		ContextWindows: contextWindows,
	})
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

// Handler 返回 http.Handler,便于挂到任意监听器(Unix socket / TCP)。
func (s *Server) Handler() http.Handler { return s.mux }
