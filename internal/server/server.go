// Package server 暴露内核的 REST + SSE 接口。
//
// 指令走 REST,事件流走 SSE。传输管道由 config 决定(本地 Unix socket /
// 远端 TCP+TLS),协议不变。
package server

import (
	"context"
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
	s.mux.HandleFunc("GET /sessions/{id}/events", s.handleEvents)
	s.mux.HandleFunc("GET /sessions/{id}/history", s.handleHistory)
	s.mux.HandleFunc("POST /sessions/{id}/turns", s.handleSubmitTurn)
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

// handleUpdateSession 局部更新会话可变字段(模型/工作目录/审批档位),
// 供会话进行中实时切换审批档位等场景。更新对后续工具调用立即生效。
func (s *Server) handleUpdateSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req protocol.UpdateSessionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
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
	writeJSON(w, http.StatusOK, protocol.ModelsResponse{Models: models})
}

// handleSubmitTurn 提交一轮对话。回合同步执行,事件从 SSE 流出;
// 这里异步跑 turn,立即返回 RunID,客户端从已订阅的 SSE 收事件。
func (s *Server) handleSubmitTurn(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req protocol.SubmitTurnRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	// 用后台 context,使 turn 不随此 HTTP 请求结束而取消。
	go func() {
		_ = s.backend.SubmitTurn(context.Background(), id, req.Message)
	}()
	writeJSON(w, http.StatusOK, protocol.SubmitTurnResponse{RunID: id})
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

// Handler 返回 http.Handler,便于挂到任意监听器(Unix socket / TCP)。
func (s *Server) Handler() http.Handler { return s.mux }
