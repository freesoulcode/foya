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
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/freesoulcode/foya/internal/agent"
	approvalpkg "github.com/freesoulcode/foya/internal/approval"
	"github.com/freesoulcode/foya/internal/artifact"
	"github.com/freesoulcode/foya/internal/backend"
	"github.com/freesoulcode/foya/internal/config"
	"github.com/freesoulcode/foya/internal/contextdata"
	"github.com/freesoulcode/foya/internal/event"
	"github.com/freesoulcode/foya/internal/hooks"
	"github.com/freesoulcode/foya/internal/mcpclient"
	"github.com/freesoulcode/foya/internal/message"
	"github.com/freesoulcode/foya/internal/project"
	"github.com/freesoulcode/foya/internal/protocol"
	"github.com/freesoulcode/foya/internal/provider"
	"github.com/freesoulcode/foya/internal/session"
	"github.com/freesoulcode/foya/internal/subagent"
	"github.com/freesoulcode/foya/internal/terminal"
	"github.com/freesoulcode/foya/internal/websearch"
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
	s.mux.HandleFunc("POST /sessions/{id}/artifacts", s.handleUploadArtifact)
	s.mux.HandleFunc("GET /sessions/{id}/artifacts/{artifact_id}", s.handleReadArtifact)
	s.mux.HandleFunc("DELETE /sessions/{id}/artifacts/{artifact_id}", s.handleDeleteArtifact)
	s.mux.HandleFunc("GET /sessions/{id}/children", s.handleChildSessions)
	s.mux.HandleFunc("POST /sessions/{id}/agents", s.handleStartAgent)
	s.mux.HandleFunc("GET /sessions/{id}/agents", s.handleListAgentRuns)
	s.mux.HandleFunc("GET /sessions/{id}/agents/{run_id}", s.handleGetAgentRun)
	s.mux.HandleFunc("POST /sessions/{id}/agents/wait", s.handleWaitAgents)
	s.mux.HandleFunc("POST /sessions/{id}/agents/{run_id}/cancel", s.handleCancelAgent)
	s.mux.HandleFunc("GET /sessions/{id}/agent-budget", s.handleAgentBudget)
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
	s.mux.HandleFunc("GET /skills", s.handleListSkills)
	s.mux.HandleFunc("PATCH /skills/{ref}", s.handleSetSkillEnabled)
	s.mux.HandleFunc("GET /agents", s.handleListAgents)
	s.mux.HandleFunc("GET /settings/agent-limits", s.handleGetAgentLimits)
	s.mux.HandleFunc("PUT /settings/agent-limits", s.handleUpdateAgentLimits)
	s.mux.HandleFunc("GET /settings/memory", s.handleGetMemorySettings)
	s.mux.HandleFunc("PUT /settings/memory", s.handleUpdateMemorySettings)
	s.mux.HandleFunc("GET /hooks", s.handleGetHooks)
	s.mux.HandleFunc("PUT /hooks", s.handleReplaceHooks)
	s.mux.HandleFunc("GET /projects", s.handleListProjects)
	s.mux.HandleFunc("POST /projects", s.handleCreateProject)
	s.mux.HandleFunc("PATCH /projects/{id}", s.handleUpdateProject)
	s.mux.HandleFunc("DELETE /projects/{id}", s.handleDeleteProject)
	s.mux.HandleFunc("GET /projects/{id}/skills", s.handleProjectSkills)
	s.mux.HandleFunc("GET /projects/{id}/agents", s.handleProjectAgents)
	s.mux.HandleFunc("GET /rules", s.handleListRules)
	s.mux.HandleFunc("POST /rules", s.handleCreateRule)
	s.mux.HandleFunc("PATCH /rules/{id}", s.handleUpdateRule)
	s.mux.HandleFunc("DELETE /rules/{id}", s.handleDeleteRule)
	s.mux.HandleFunc("GET /memories", s.handleListMemories)
	s.mux.HandleFunc("POST /memories", s.handleCreateMemory)
	s.mux.HandleFunc("PATCH /memories/{id}", s.handleUpdateMemory)
	s.mux.HandleFunc("DELETE /memories/{id}", s.handleDeleteMemory)
	s.mux.HandleFunc("GET /context/events", s.handleContextEvents)
	s.mux.HandleFunc("GET /web-search", s.handleGetWebSearch)
	s.mux.HandleFunc("PUT /web-search", s.handleUpdateWebSearch)
	s.mux.HandleFunc("POST /web-search/test", s.handleTestWebSearch)
	s.mux.HandleFunc("GET /mcp", s.handleGetMCP)
	s.mux.HandleFunc("PUT /mcp", s.handleReplaceMCP)
	s.mux.HandleFunc("GET /mcp/status", s.handleMCPStatus)
	s.mux.HandleFunc("GET /mcp/registry", s.handleMCPRegistry)
	s.mux.HandleFunc("GET /mcp/resources", s.handleMCPResources)
	s.mux.HandleFunc("POST /mcp/resources/read", s.handleMCPReadResource)
	s.mux.HandleFunc("GET /mcp/prompts", s.handleMCPPrompts)
	s.mux.HandleFunc("POST /mcp/prompts/get", s.handleMCPGetPrompt)
	s.mux.HandleFunc("POST /sessions/{id}/terminals", s.handleStartTerminal)
	s.mux.HandleFunc("GET /sessions/{id}/terminals/{ref}", s.handleAttachTerminal)
	s.mux.HandleFunc("POST /sessions/{id}/terminals/{ref}/input", s.handleWriteTerminal)
	s.mux.HandleFunc("POST /sessions/{id}/terminals/{ref}/resize", s.handleResizeTerminal)
	s.mux.HandleFunc("DELETE /sessions/{id}/terminals/{ref}", s.handleStopTerminal)
	s.mux.HandleFunc("GET /sessions/{id}/terminals/{ref}/events", s.handleTerminalEvents)
}

type contextCreateRequest struct {
	Scope       contextdata.Scope       `json:"scope"`
	ProjectID   string                  `json:"project_id,omitempty"`
	Content     string                  `json:"content"`
	Name        string                  `json:"name,omitempty"`
	Description string                  `json:"description,omitempty"`
	Trigger     contextdata.RuleTrigger `json:"trigger,omitempty"`
	Globs       []string                `json:"globs,omitempty"`
	Path        string                  `json:"path,omitempty"`
}

type contextUpdateRequest struct {
	Content     string                   `json:"content"`
	Name        *string                  `json:"name,omitempty"`
	Description *string                  `json:"description,omitempty"`
	Trigger     *contextdata.RuleTrigger `json:"trigger,omitempty"`
	Globs       *[]string                `json:"globs,omitempty"`
	Path        *string                  `json:"path,omitempty"`
}

type hooksRequest struct {
	Scope     string         `json:"scope"`
	ProjectID string         `json:"project_id,omitempty"`
	Hooks     []hooks.Config `json:"hooks"`
}

func (s *Server) handleGetHooks(w http.ResponseWriter, r *http.Request) {
	hooks, err := s.backend.Hooks(r.URL.Query().Get("scope"), r.URL.Query().Get("project_id"))
	if err != nil {
		writeContextErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, hooks)
}

func (s *Server) handleReplaceHooks(w http.ResponseWriter, r *http.Request) {
	var input hooksRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if err := s.backend.ReplaceHooks(input.Scope, input.ProjectID, input.Hooks); err != nil {
		if errors.Is(err, hooks.ErrInvalidHook) ||
			errors.Is(err, hooks.ErrInvalidHooksConfig) ||
			errors.Is(err, hooks.ErrInvalidEvent) {
			writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
		writeContextErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, input.Hooks)
}

func contextFilter(r *http.Request) (contextdata.Scope, string) {
	return contextdata.Scope(r.URL.Query().Get("scope")), r.URL.Query().Get("project_id")
}

func (s *Server) handleGetMemorySettings(w http.ResponseWriter, _ *http.Request) {
	settings, err := s.backend.MemorySettings()
	if err != nil {
		writeContextErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, settings)
}

func (s *Server) handleUpdateMemorySettings(w http.ResponseWriter, r *http.Request) {
	var settings contextdata.MemorySettings
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&settings); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	updated, err := s.backend.UpdateMemorySettings(settings)
	if err != nil {
		writeContextErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) handleListRules(w http.ResponseWriter, r *http.Request) {
	scope, projectID := contextFilter(r)
	items, err := s.backend.Rules(scope, projectID)
	if err != nil {
		writeContextErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) handleCreateRule(w http.ResponseWriter, r *http.Request) {
	var input contextCreateRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	item, err := s.backend.CreateRule(input.Scope, input.ProjectID, input.Content, contextdata.RuleOptions{
		Name: input.Name, Description: input.Description, Trigger: input.Trigger,
		Globs: input.Globs, Path: input.Path,
	})
	if err != nil {
		writeContextErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func (s *Server) handleUpdateRule(w http.ResponseWriter, r *http.Request) {
	var input contextUpdateRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	options := []contextdata.RuleOptions(nil)
	if input.Name != nil || input.Description != nil || input.Trigger != nil ||
		input.Globs != nil || input.Path != nil {
		option := contextdata.RuleOptions{}
		if input.Name != nil {
			option.Name = *input.Name
		}
		if input.Description != nil {
			option.Description = *input.Description
		}
		if input.Trigger != nil {
			option.Trigger = *input.Trigger
		}
		if input.Globs != nil {
			option.Globs = *input.Globs
		}
		if input.Path != nil {
			option.Path = *input.Path
		}
		options = append(options, option)
	}
	item, err := s.backend.UpdateRule(r.PathValue("id"), input.Content, options...)
	if err != nil {
		writeContextErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) handleDeleteRule(w http.ResponseWriter, r *http.Request) {
	if err := s.backend.DeleteRule(r.PathValue("id")); err != nil {
		writeContextErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleListMemories(w http.ResponseWriter, r *http.Request) {
	scope, projectID := contextFilter(r)
	items, err := s.backend.Memories(scope, projectID)
	if err != nil {
		writeContextErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) handleCreateMemory(w http.ResponseWriter, r *http.Request) {
	var input contextCreateRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	item, err := s.backend.CreateMemory(input.Scope, input.ProjectID, input.Content)
	if err != nil {
		writeContextErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func (s *Server) handleUpdateMemory(w http.ResponseWriter, r *http.Request) {
	var input contextUpdateRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	item, err := s.backend.UpdateMemory(r.PathValue("id"), input.Content)
	if err != nil {
		writeContextErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) handleDeleteMemory(w http.ResponseWriter, r *http.Request) {
	if err := s.backend.DeleteMemory(r.PathValue("id")); err != nil {
		writeContextErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleContextEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, "no_flush", "streaming unsupported")
		return
	}
	after, _ := strconv.ParseUint(r.Header.Get("Last-Event-ID"), 10, 64)
	ch, err := s.backend.SubscribeContext(r.Context())
	if err != nil {
		writeContextErr(w, err)
		return
	}
	replay, err := s.backend.ReplayContext(after)
	if err != nil {
		writeContextErr(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher.Flush()
	last := after
	writeEvent := func(ev contextdata.Event) {
		if ev.Seq <= last {
			return
		}
		data, _ := json.Marshal(ev)
		fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", ev.Seq, ev.Kind, data)
		flusher.Flush()
		last = ev.Seq
	}
	for _, ev := range replay {
		writeEvent(ev)
	}
	for {
		select {
		case <-r.Context().Done():
			return
		case ev, ok := <-ch:
			if !ok {
				return
			}
			writeEvent(ev)
		}
	}
}

func writeContextErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, contextdata.ErrNotFound), errors.Is(err, project.ErrNotFound):
		writeErr(w, http.StatusNotFound, "not_found", err.Error())
	case errors.Is(err, contextdata.ErrInvalidScope),
		errors.Is(err, contextdata.ErrEmptyContent),
		errors.Is(err, contextdata.ErrTooLarge),
		errors.Is(err, contextdata.ErrInvalidRule):
		writeErr(w, http.StatusBadRequest, "invalid_context_item", err.Error())
	default:
		writeErr(w, http.StatusInternalServerError, "context_item_failed", err.Error())
	}
}

func (s *Server) handleGetAgentLimits(w http.ResponseWriter, _ *http.Request) {
	limits, err := s.backend.AgentLimits()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "agent_limits_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, limits)
}

func (s *Server) handleUpdateAgentLimits(w http.ResponseWriter, r *http.Request) {
	var limits config.AgentLimits
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&limits); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if err := s.backend.UpdateAgentLimits(limits); err != nil {
		writeErr(w, http.StatusBadRequest, "agent_limits_update_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, limits)
}

func (s *Server) handleStartAgent(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Task             string                    `json:"task"`
		AgentRef         string                    `json:"agent_ref,omitempty"`
		RootRunID        string                    `json:"root_run_id,omitempty"`
		ParentToolCallID string                    `json:"parent_tool_call_id,omitempty"`
		Context          subagent.ContextSelection `json:"context,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	item, err := s.backend.StartAgent(r.Context(), subagent.SpawnRequest{
		ParentSessionID: r.PathValue("id"), ParentToolCallID: req.ParentToolCallID,
		RootRunID: req.RootRunID, Task: req.Task, AgentRef: req.AgentRef, Context: req.Context,
	})
	if err != nil {
		writeAgentRunErr(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, item)
}

func (s *Server) handleListAgentRuns(w http.ResponseWriter, r *http.Request) {
	items, err := s.backend.AgentRuns(r.PathValue("id"))
	if err != nil {
		writeAgentRunErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) handleGetAgentRun(w http.ResponseWriter, r *http.Request) {
	item, err := s.backend.AgentRun(r.PathValue("id"), r.PathValue("run_id"))
	if err != nil {
		writeAgentRunErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) handleWaitAgents(w http.ResponseWriter, r *http.Request) {
	var req struct {
		IDs  []string `json:"ids"`
		Mode string   `json:"mode,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	items, err := s.backend.WaitAgents(r.Context(), r.PathValue("id"), req.IDs, req.Mode != "any")
	if err != nil {
		writeAgentRunErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) handleCancelAgent(w http.ResponseWriter, r *http.Request) {
	if err := s.backend.CancelAgent(r.PathValue("id"), r.PathValue("run_id")); err != nil {
		writeAgentRunErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleAgentBudget(w http.ResponseWriter, r *http.Request) {
	item, err := s.backend.AgentBudget(r.PathValue("id"))
	if err != nil {
		writeAgentRunErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func writeAgentRunErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, session.ErrNotFound), errors.Is(err, os.ErrNotExist):
		writeErr(w, http.StatusNotFound, "not_found", err.Error())
	case errors.Is(err, context.Canceled):
		writeErr(w, http.StatusRequestTimeout, "cancelled", err.Error())
	default:
		writeErr(w, http.StatusBadRequest, "agent_run_failed", err.Error())
	}
}

func (s *Server) handleListAgents(w http.ResponseWriter, r *http.Request) {
	items, err := s.backend.Agents(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "agents_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) handleProjectAgents(w http.ResponseWriter, r *http.Request) {
	items, err := s.backend.ProjectAgents(r.Context(), r.PathValue("id"))
	if err != nil {
		if errors.Is(err, project.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "project_not_found", err.Error())
			return
		}
		writeErr(w, http.StatusInternalServerError, "agents_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) handleGetMCP(w http.ResponseWriter, _ *http.Request) {
	config, err := s.backend.MCPConfig()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "mcp_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, config)
}

func (s *Server) handleReplaceMCP(w http.ResponseWriter, r *http.Request) {
	var config mcpclient.Config
	if err := json.NewDecoder(r.Body).Decode(&config); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if err := s.backend.ReplaceMCPConfig(r.Context(), config); err != nil {
		writeErr(w, http.StatusBadRequest, "mcp_update_failed", err.Error())
		return
	}
	updated, _ := s.backend.MCPConfig()
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) handleMCPStatus(w http.ResponseWriter, _ *http.Request) {
	statuses, err := s.backend.MCPStatuses()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "mcp_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, statuses)
}

func (s *Server) handleMCPRegistry(w http.ResponseWriter, r *http.Request) {
	items, err := s.backend.SearchMCPRegistry(r.Context(), r.URL.Query().Get("search"))
	if err != nil {
		writeErr(w, http.StatusBadGateway, "mcp_registry_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) handleMCPResources(w http.ResponseWriter, r *http.Request) {
	resources, err := s.backend.MCPResources(r.Context(), r.URL.Query().Get("server_id"))
	if err != nil {
		writeErr(w, http.StatusBadGateway, "mcp_resources_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resources)
}

func (s *Server) handleMCPReadResource(w http.ResponseWriter, r *http.Request) {
	var input protocol.MCPResourceReadRequest
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	result, err := s.backend.MCPReadResource(r.Context(), input.ServerID, input.URI)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "mcp_resource_read_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleMCPPrompts(w http.ResponseWriter, r *http.Request) {
	prompts, err := s.backend.MCPPrompts(r.Context(), r.URL.Query().Get("server_id"))
	if err != nil {
		writeErr(w, http.StatusBadGateway, "mcp_prompts_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, prompts)
}

func (s *Server) handleMCPGetPrompt(w http.ResponseWriter, r *http.Request) {
	var input protocol.MCPPromptGetRequest
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	result, err := s.backend.MCPGetPrompt(r.Context(), input.ServerID, input.Name, input.Args)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "mcp_prompt_get_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleListSkills(w http.ResponseWriter, r *http.Request) {
	var (
		items any
		err   error
	)
	if r.URL.Query().Get("all") == "true" {
		items, err = s.backend.AllSkills(r.Context())
	} else {
		items, err = s.backend.Skills(r.Context())
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "skills_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) handleListProjects(w http.ResponseWriter, _ *http.Request) {
	items, err := s.backend.Projects()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "projects_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) handleCreateProject(w http.ResponseWriter, r *http.Request) {
	var input protocol.ProjectCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	item, err := s.backend.RegisterProject(input.Path, input.Name)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "project_create_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func (s *Server) handleUpdateProject(w http.ResponseWriter, r *http.Request) {
	var input protocol.ProjectUpdateRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	item, err := s.backend.UpdateProject(r.PathValue("id"), input.Name, input.Pinned)
	if err != nil {
		if errors.Is(err, project.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "project_not_found", err.Error())
			return
		}
		writeErr(w, http.StatusBadRequest, "project_update_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) handleDeleteProject(w http.ResponseWriter, r *http.Request) {
	err := s.backend.DeleteProject(r.PathValue("id"))
	if err != nil {
		switch {
		case errors.Is(err, project.ErrNotFound):
			writeErr(w, http.StatusNotFound, "project_not_found", err.Error())
		case errors.Is(err, backend.ErrProjectInUse):
			writeErr(w, http.StatusConflict, "project_in_use", err.Error())
		default:
			writeErr(w, http.StatusInternalServerError, "project_delete_failed", err.Error())
		}
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleProjectSkills(w http.ResponseWriter, r *http.Request) {
	items, err := s.backend.ProjectSkills(r.Context(), r.PathValue("id"))
	if err != nil {
		if errors.Is(err, project.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "project_not_found", err.Error())
			return
		}
		writeErr(w, http.StatusInternalServerError, "skills_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) handleSetSkillEnabled(w http.ResponseWriter, r *http.Request) {
	var input protocol.SkillEnableRequest
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	ref := strings.TrimSpace(r.PathValue("ref"))
	if ref == "" {
		writeErr(w, http.StatusBadRequest, "bad_request", "skill ref is required")
		return
	}
	if err := s.backend.SetSkillEnabled(ref, input.Enabled); err != nil {
		writeErr(w, http.StatusInternalServerError, "skill_update_failed", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleGetWebSearch(w http.ResponseWriter, _ *http.Request) {
	settings, err := s.backend.WebSearchSettings()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "web_search_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, settings)
}

func (s *Server) handleUpdateWebSearch(w http.ResponseWriter, r *http.Request) {
	var settings websearch.Settings
	if err := json.NewDecoder(r.Body).Decode(&settings); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if err := s.backend.UpdateWebSearchSettings(settings); err != nil {
		writeErr(w, http.StatusBadRequest, "web_search_update_failed", err.Error())
		return
	}
	updated, _ := s.backend.WebSearchSettings()
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) handleTestWebSearch(w http.ResponseWriter, r *http.Request) {
	var input protocol.WebSearchTestRequest
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	results, err := s.backend.TestWebSearch(r.Context(), input.ProviderID, input.Query)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "web_search_test_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, results)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

// handleCreateSession 新建会话。未指定模型时回退到默认 Connection 的模型。
func (s *Server) handleCreateSession(w http.ResponseWriter, r *http.Request) {
	var req protocol.CreateSessionRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}

	approval := req.ApprovalMode
	if approval == "" {
		approval = string(approvalpkg.ModeManual)
	}

	sess, err := s.backend.CreateSession(session.CreateOptions{
		ConnectionID:    req.ConnectionID,
		Model:           req.Model,
		ReasoningEffort: session.ReasoningEffort(req.ReasoningEffort),
		ProjectID:       req.ProjectID,
		ApprovalMode:    approval,
	})
	if err != nil {
		if errors.Is(err, session.ErrInvalidReasoningEffort) {
			writeErr(w, http.StatusBadRequest, "invalid_reasoning_effort", err.Error())
			return
		}
		if errors.Is(err, session.ErrInvalidApprovalMode) {
			writeErr(w, http.StatusBadRequest, "invalid_approval_mode", err.Error())
			return
		}
		if errors.Is(err, backend.ErrConnectionNotFound) {
			writeErr(w, http.StatusBadRequest, "connection_not_found", err.Error())
			return
		}
		if errors.Is(err, project.ErrNotFound) {
			writeErr(w, http.StatusBadRequest, "project_not_found", err.Error())
			return
		}
		writeErr(w, http.StatusInternalServerError, "create_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, sess)
}

// handleUpdateSession 局部更新会话可变字段(模型/项目/审批档位/标题),
// 供会话进行中实时切换审批档位、手动改名等场景。更新对后续工具调用立即生效。
func (s *Server) handleUpdateSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req protocol.UpdateSessionRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
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
		req.ProjectID,
		req.ApprovalMode,
	)
	if err != nil {
		if errors.Is(err, session.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "not_found", err.Error())
			return
		}
		if errors.Is(err, session.ErrProjectLocked) {
			writeErr(w, http.StatusConflict, "project_locked", err.Error())
			return
		}
		if errors.Is(err, project.ErrNotFound) {
			writeErr(w, http.StatusBadRequest, "project_not_found", err.Error())
			return
		}
		if errors.Is(err, session.ErrInvalidReasoningEffort) {
			writeErr(w, http.StatusBadRequest, "invalid_reasoning_effort", err.Error())
			return
		}
		if errors.Is(err, session.ErrInvalidApprovalMode) {
			writeErr(w, http.StatusBadRequest, "invalid_approval_mode", err.Error())
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
		ID:            connection.ID,
		Name:          connection.Name,
		Kind:          connection.Kind,
		AuthKind:      connection.AuthKind,
		BaseURL:       connection.BaseURL,
		HasAPIKey:     connection.APIKey != "",
		DefaultModel:  connection.DefaultModel,
		ContextWindow: connection.ContextWindow,
		SortOrder:     connection.SortOrder,
	}
}

func toConnection(input protocol.ConnectionConfig) config.Connection {
	return config.Connection{
		ID:            input.ID,
		Name:          input.Name,
		Kind:          input.Kind,
		AuthKind:      input.AuthKind,
		BaseURL:       input.BaseURL,
		APIKey:        input.APIKey,
		DefaultModel:  input.DefaultModel,
		ContextWindow: input.ContextWindow,
		SortOrder:     input.SortOrder,
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
	capabilities := make(map[string]provider.ModelCapabilities)
	for _, model := range models {
		ids = append(ids, model.ID)
		if model.ContextWindow > 0 {
			contextWindows[model.ID] = model.ContextWindow
		}
		if model.Capabilities.ImageInput != nil {
			capabilities[model.ID] = model.Capabilities
		}
	}
	writeJSON(w, http.StatusOK, protocol.ConnectionModelsResponse{
		Models:         ids,
		ContextWindows: contextWindows,
		Capabilities:   capabilities,
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

func (s *Server) handleChildSessions(w http.ResponseWriter, r *http.Request) {
	items, err := s.backend.ChildSessions(r.PathValue("id"))
	if err != nil {
		if errors.Is(err, session.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "not_found", err.Error())
			return
		}
		writeErr(w, http.StatusInternalServerError, "children_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, items)
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

func (s *Server) handleUploadArtifact(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, artifact.MaxImageBytes+(1<<20))
	if err := r.ParseMultipartForm(artifact.MaxImageBytes); err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			writeErr(w, http.StatusRequestEntityTooLarge, "image_too_large", err.Error())
		} else {
			writeErr(w, http.StatusBadRequest, "invalid_artifact", err.Error())
		}
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "missing_artifact", "multipart field \"file\" is required")
		return
	}
	defer file.Close()
	ref, err := s.backend.PutImage(r.Context(), r.PathValue("id"), header.Filename, file)
	if err != nil {
		switch {
		case errors.Is(err, session.ErrNotFound):
			writeErr(w, http.StatusNotFound, "not_found", err.Error())
		case errors.Is(err, artifact.ErrUnsupportedType):
			writeErr(w, http.StatusUnsupportedMediaType, "unsupported_image", err.Error())
		case errors.Is(err, artifact.ErrImageTooLarge), errors.Is(err, artifact.ErrTooManyPixels):
			writeErr(w, http.StatusRequestEntityTooLarge, "image_too_large", err.Error())
		default:
			writeErr(w, http.StatusInternalServerError, "artifact_store_failed", err.Error())
		}
		return
	}
	writeJSON(w, http.StatusCreated, protocol.ArtifactResponse{Attachment: ref})
}

func (s *Server) handleReadArtifact(w http.ResponseWriter, r *http.Request) {
	data, ref, err := s.backend.ReadArtifact(
		r.Context(),
		r.PathValue("id"),
		r.PathValue("artifact_id"),
	)
	if err != nil {
		if errors.Is(err, session.ErrNotFound) || errors.Is(err, os.ErrNotExist) {
			writeErr(w, http.StatusNotFound, "artifact_not_found", err.Error())
		} else {
			writeErr(w, http.StatusInternalServerError, "artifact_read_failed", err.Error())
		}
		return
	}
	w.Header().Set("Content-Type", ref.MediaType)
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func (s *Server) handleDeleteArtifact(w http.ResponseWriter, r *http.Request) {
	err := s.backend.DeleteArtifact(
		r.Context(),
		r.PathValue("id"),
		r.PathValue("artifact_id"),
	)
	if err != nil {
		if errors.Is(err, artifact.ErrCommitted) {
			writeErr(w, http.StatusConflict, "artifact_committed", err.Error())
		} else if errors.Is(err, session.ErrNotFound) || errors.Is(err, os.ErrNotExist) {
			writeErr(w, http.StatusNotFound, "artifact_not_found", err.Error())
		} else {
			writeErr(w, http.StatusInternalServerError, "artifact_delete_failed", err.Error())
		}
		return
	}
	w.WriteHeader(http.StatusNoContent)
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
	result, err := s.backend.SubmitInput(r.Context(), id, message.UserInput{
		Text:        req.Message,
		Attachments: req.Attachments,
	})
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
// replacement. Potential project effects require an explicit second request.
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
		case errors.Is(err, backend.ErrAttachmentEditUnsupported):
			writeErr(w, http.StatusConflict, "attachment_edit_unsupported", err.Error())
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
	item, err := s.backend.EnqueueInput(r.Context(), r.PathValue("id"), message.UserInput{
		Text:        req.Message,
		Attachments: req.Attachments,
	})
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
	if err := s.backend.ResolveApproval(req.RequestID, req.Decision); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_approval_decision", err.Error())
		return
	}
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
		errors.Is(err, backend.ErrInvalidQueuePosition),
		errors.Is(err, artifact.ErrInvalidID),
		errors.Is(err, artifact.ErrUnsupportedType):
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
