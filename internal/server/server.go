// Package server 暴露内核的 REST + SSE 接口。
//
// 指令走 REST,事件流走 SSE。传输管道由 config 决定(本地 Unix socket /
// 远端 TCP+TLS),协议不变。
package server

import (
	"context"
	"encoding/base64"
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
	"github.com/freesoulcode/foya/internal/automation"
	"github.com/freesoulcode/foya/internal/backend"
	"github.com/freesoulcode/foya/internal/browseruse"
	"github.com/freesoulcode/foya/internal/canvas"
	"github.com/freesoulcode/foya/internal/channel/feishu"
	"github.com/freesoulcode/foya/internal/command"
	"github.com/freesoulcode/foya/internal/config"
	"github.com/freesoulcode/foya/internal/contextdata"
	"github.com/freesoulcode/foya/internal/event"
	"github.com/freesoulcode/foya/internal/hooks"
	"github.com/freesoulcode/foya/internal/mcpclient"
	"github.com/freesoulcode/foya/internal/message"
	"github.com/freesoulcode/foya/internal/plugin"
	"github.com/freesoulcode/foya/internal/project"
	"github.com/freesoulcode/foya/internal/protocol"
	"github.com/freesoulcode/foya/internal/provider"
	"github.com/freesoulcode/foya/internal/question"
	"github.com/freesoulcode/foya/internal/session"
	"github.com/freesoulcode/foya/internal/subagent"
	"github.com/freesoulcode/foya/internal/terminal"
	"github.com/freesoulcode/foya/internal/tool"
	"github.com/freesoulcode/foya/internal/websearch"
	"github.com/freesoulcode/foya/internal/workflow"
)

// Server 承载 REST + SSE 路由。
type Server struct {
	cfg         config.Config
	backend     *backend.Backend
	channels    ChannelManager
	automations AutomationManager
	mux         *http.ServeMux
}

type ChannelManager interface {
	List() []feishu.State
	Get(string) (feishu.State, bool)
	Create(feishu.UpdateInput) (feishu.State, error)
	Update(string, feishu.UpdateInput) (feishu.State, error)
	Delete(string) error
	StartRegistration(feishu.RegistrationInput) (feishu.RegistrationState, error)
	GetRegistration(string) (feishu.RegistrationState, bool)
	CancelRegistration(string) error
}

type AutomationManager interface {
	List() []automation.Task
	Get(string) (automation.Task, bool)
	Create(automation.Input) (automation.Task, error)
	Update(string, automation.Input) (automation.Task, error)
	Delete(string) error
	RunNow(string) (automation.Task, error)
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
	s.mux.HandleFunc("GET /settings/default-models", s.handleGetDefaultModels)
	s.mux.HandleFunc("PUT /settings/default-models", s.handleUpdateDefaultModels)
	s.mux.HandleFunc("GET /canvases", s.handleListCanvases)
	s.mux.HandleFunc("POST /canvases", s.handleCreateCanvas)
	s.mux.HandleFunc("GET /canvases/{id}", s.handleGetCanvas)
	s.mux.HandleFunc("PATCH /canvases/{id}", s.handleUpdateCanvas)
	s.mux.HandleFunc("DELETE /canvases/{id}", s.handleDeleteCanvas)
	s.mux.HandleFunc("GET /canvases/{id}/events", s.handleCanvasEvents)
	s.mux.HandleFunc("POST /canvases/{id}/assets", s.handleUploadCanvasAsset)
	s.mux.HandleFunc("GET /canvases/{id}/assets/{asset_id}", s.handleReadCanvasAsset)
	s.mux.HandleFunc("POST /canvases/{id}/generate-image", s.handleGenerateCanvasImage)
	s.mux.HandleFunc("POST /canvases/{id}/generate-video", s.handleGenerateCanvasVideo)
	s.mux.HandleFunc("GET /usage", s.handleUsageStatistics)
	s.mux.HandleFunc("POST /sessions", s.handleCreateSession)
	s.mux.HandleFunc("GET /sessions", s.handleListSessions)
	s.mux.HandleFunc("PATCH /sessions/{id}", s.handleUpdateSession)
	s.mux.HandleFunc("POST /sessions/{id}/fork", s.handleForkSession)
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
	s.mux.HandleFunc("GET /sessions/{id}/file-review", s.handleGetFileReview)
	s.mux.HandleFunc("POST /sessions/{id}/file-review", s.handleResolveFileReview)
	s.mux.HandleFunc("POST /sessions/{id}/turns", s.handleSubmitTurn)
	s.mux.HandleFunc("POST /sessions/{id}/turns/{message_seq}/rewind", s.handleRewindTurn)
	s.mux.HandleFunc("POST /sessions/{id}/compact", s.handleCompactSession)
	s.mux.HandleFunc("POST /sessions/{id}/cancel", s.handleCancelTurn)
	s.mux.HandleFunc("POST /sessions/{id}/tools/{tool_call_id}/cancel", s.handleCancelTool)
	s.mux.HandleFunc("POST /sessions/{id}/tools/{tool_call_id}/background", s.handleBackgroundTool)
	s.mux.HandleFunc("POST /sessions/{id}/tools/{tool_call_id}/reveal", s.handleRevealToolCommand)
	s.mux.HandleFunc("GET /sessions/{id}/background-commands", s.handleListBackgroundCommands)
	s.mux.HandleFunc("GET /sessions/{id}/background-commands/{command_id}", s.handleGetBackgroundCommand)
	s.mux.HandleFunc("POST /sessions/{id}/background-commands/{command_id}/cancel", s.handleStopBackgroundCommand)
	s.mux.HandleFunc("GET /sessions/{id}/queue", s.handleListQueue)
	s.mux.HandleFunc("POST /sessions/{id}/queue", s.handleEnqueueMessage)
	s.mux.HandleFunc("PATCH /sessions/{id}/queue/{message_id}", s.handleUpdateQueuedMessage)
	s.mux.HandleFunc("DELETE /sessions/{id}/queue/{message_id}", s.handleDeleteQueuedMessage)
	s.mux.HandleFunc("POST /sessions/{id}/queue/{message_id}/dispatch", s.handleDispatchQueuedMessage)
	s.mux.HandleFunc("POST /sessions/{id}/approvals/{request_id}", s.handleResolveApproval)
	s.mux.HandleFunc("POST /sessions/{id}/questions/{batch_id}/answer", s.handleAnswerQuestions)
	s.mux.HandleFunc("POST /sessions/{id}/questions/{batch_id}/cancel", s.handleCancelQuestions)
	s.mux.HandleFunc("GET /skills", s.handleListSkills)
	s.mux.HandleFunc("GET /skills/inspect", s.handleInspectSkills)
	s.mux.HandleFunc("PATCH /skills/{ref}", s.handleSetSkillEnabled)
	s.mux.HandleFunc("PATCH /skills/{ref}/pinned", s.handleSetSkillPinned)
	s.mux.HandleFunc("GET /agents", s.handleListAgents)
	s.mux.HandleFunc("GET /settings/agent-limits", s.handleGetAgentLimits)
	s.mux.HandleFunc("PUT /settings/agent-limits", s.handleUpdateAgentLimits)
	s.mux.HandleFunc("GET /settings/memory", s.handleGetMemorySettings)
	s.mux.HandleFunc("PUT /settings/memory", s.handleUpdateMemorySettings)
	s.mux.HandleFunc("GET /channels", s.handleListChannels)
	s.mux.HandleFunc("POST /channels", s.handleCreateChannel)
	s.mux.HandleFunc("POST /channels/feishu/registrations", s.handleStartFeishuRegistration)
	s.mux.HandleFunc("GET /channels/feishu/registrations/{id}", s.handleGetFeishuRegistration)
	s.mux.HandleFunc("DELETE /channels/feishu/registrations/{id}", s.handleCancelFeishuRegistration)
	s.mux.HandleFunc("GET /channels/{id}", s.handleGetChannel)
	s.mux.HandleFunc("PUT /channels/{id}", s.handleUpdateChannel)
	s.mux.HandleFunc("DELETE /channels/{id}", s.handleDeleteChannel)
	s.mux.HandleFunc("GET /automations", s.handleListAutomations)
	s.mux.HandleFunc("POST /automations", s.handleCreateAutomation)
	s.mux.HandleFunc("GET /automations/{id}", s.handleGetAutomation)
	s.mux.HandleFunc("PUT /automations/{id}", s.handleUpdateAutomation)
	s.mux.HandleFunc("DELETE /automations/{id}", s.handleDeleteAutomation)
	s.mux.HandleFunc("POST /automations/{id}/run", s.handleRunAutomation)
	s.mux.HandleFunc("GET /settings/feishu-bot", s.handleGetFeishuBot)
	s.mux.HandleFunc("PUT /settings/feishu-bot", s.handleUpdateFeishuBot)
	s.mux.HandleFunc("GET /hooks", s.handleGetHooks)
	s.mux.HandleFunc("PUT /hooks", s.handleReplaceHooks)
	s.mux.HandleFunc("GET /commands", s.handleListCommands)
	s.mux.HandleFunc("POST /commands", s.handleCreateCommand)
	s.mux.HandleFunc("PATCH /commands/{ref}", s.handleUpdateCommand)
	s.mux.HandleFunc("DELETE /commands/{ref}", s.handleDeleteCommand)
	s.mux.HandleFunc("GET /sessions/{id}/commands", s.handleSessionCommands)
	s.mux.HandleFunc("POST /sessions/{id}/commands/{name}", s.handleExecuteCommand)
	s.mux.HandleFunc("GET /sessions/{id}/workflow", s.handleGetWorkflow)
	s.mux.HandleFunc("POST /sessions/{id}/workflow/{workflow_id}/approve", s.handleApproveWorkflow)
	s.mux.HandleFunc("GET /projects", s.handleListProjects)
	s.mux.HandleFunc("POST /projects", s.handleCreateProject)
	s.mux.HandleFunc("PATCH /projects/{id}", s.handleUpdateProject)
	s.mux.HandleFunc("DELETE /projects/{id}", s.handleDeleteProject)
	s.mux.HandleFunc("GET /projects/{id}/skills", s.handleProjectSkills)
	s.mux.HandleFunc("GET /projects/{id}/skills/inspect", s.handleInspectProjectSkills)
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
	s.mux.HandleFunc("POST /sessions/{id}/browser-actions/{request_id}", s.handleResolveBrowserAction)
	s.mux.HandleFunc("GET /plugins", s.handlePlugins)
	s.mux.HandleFunc("POST /plugins/install", s.handleInstallPlugin)
	s.mux.HandleFunc("GET /plugin-marketplaces", s.handlePluginMarketplaces)
	s.mux.HandleFunc("POST /plugin-marketplaces", s.handleAddPluginMarketplace)
	s.mux.HandleFunc("GET /plugin-marketplaces/{name}", s.handleBrowsePluginMarketplace)
	s.mux.HandleFunc("PATCH /plugin-marketplaces/{name}", s.handleSetPluginMarketplaceEnabled)
	s.mux.HandleFunc("DELETE /plugin-marketplaces/{name}", s.handleRemovePluginMarketplace)
	s.mux.HandleFunc("POST /plugin-marketplaces/{name}/refresh", s.handleRefreshPluginMarketplace)
	s.mux.HandleFunc("GET /plugin-marketplaces/{name}/plugins/{plugin}", s.handlePreviewMarketplacePlugin)
	s.mux.HandleFunc("POST /plugin-marketplaces/{name}/plugins/{plugin}/install", s.handleInstallMarketplacePlugin)
	s.mux.HandleFunc("PATCH /plugins/{name}", s.handleSetPluginEnabled)
	s.mux.HandleFunc("DELETE /plugins/{name}", s.handleRemovePlugin)
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

func (s *Server) SetChannelManager(manager ChannelManager) {
	s.channels = manager
}

func (s *Server) SetAutomationManager(manager AutomationManager) {
	s.automations = manager
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

type commandCreateRequest struct {
	Scope     command.Scope `json:"scope"`
	ProjectID string        `json:"project_id,omitempty"`
	Name      string        `json:"name"`
}

type commandUpdateRequest struct {
	Scope       command.Scope `json:"scope"`
	ProjectID   string        `json:"project_id,omitempty"`
	Name        string        `json:"name"`
	Description string        `json:"description,omitempty"`
	Body        string        `json:"body"`
}

type commandExecuteRequest struct {
	Args string `json:"args,omitempty"`
}

func (s *Server) handleListCommands(w http.ResponseWriter, r *http.Request) {
	items, err := s.backend.Commands(r.Context(), r.URL.Query().Get("scope"), r.URL.Query().Get("project_id"))
	if err != nil {
		writeCommandErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) handleCreateCommand(w http.ResponseWriter, r *http.Request) {
	var input commandCreateRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	item, err := s.backend.CreateCommand(r.Context(), command.CreateInput{
		Scope: input.Scope, ProjectID: input.ProjectID, Name: input.Name,
	})
	if err != nil {
		writeCommandErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func (s *Server) handleUpdateCommand(w http.ResponseWriter, r *http.Request) {
	var input commandUpdateRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	item, err := s.backend.UpdateCommand(r.Context(), r.PathValue("ref"), command.UpdateInput{
		Scope: input.Scope, ProjectID: input.ProjectID, Name: input.Name,
		Description: input.Description, Body: input.Body,
	})
	if err != nil {
		writeCommandErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) handleDeleteCommand(w http.ResponseWriter, r *http.Request) {
	if err := s.backend.DeleteCommand(
		r.Context(),
		r.PathValue("ref"),
		command.Scope(r.URL.Query().Get("scope")),
		r.URL.Query().Get("project_id"),
	); err != nil {
		writeCommandErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleSessionCommands(w http.ResponseWriter, r *http.Request) {
	items, err := s.backend.SessionCommands(r.Context(), r.PathValue("id"))
	if err != nil {
		writeCommandErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) handleExecuteCommand(w http.ResponseWriter, r *http.Request) {
	var input commandExecuteRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil && !errors.Is(err, io.EOF) {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	result, err := s.backend.ExecuteCommand(r.Context(), r.PathValue("id"), r.PathValue("name"), input.Args)
	if err != nil {
		writeCommandErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleGetWorkflow(w http.ResponseWriter, r *http.Request) {
	record, ok, err := s.backend.Workflow(r.PathValue("id"))
	if err != nil {
		writeCommandErr(w, err)
		return
	}
	if !ok {
		writeJSON(w, http.StatusOK, nil)
		return
	}
	writeJSON(w, http.StatusOK, record)
}

func (s *Server) handleApproveWorkflow(w http.ResponseWriter, r *http.Request) {
	result, err := s.backend.ApproveWorkflow(r.Context(), r.PathValue("id"), r.PathValue("workflow_id"))
	if err != nil {
		writeCommandErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func writeCommandErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, command.ErrNotFound), errors.Is(err, workflow.ErrNotFound), errors.Is(err, session.ErrNotFound):
		writeErr(w, http.StatusNotFound, "not_found", err.Error())
	case errors.Is(err, command.ErrInvalidScope),
		errors.Is(err, command.ErrInvalidName),
		errors.Is(err, command.ErrInvalidContent),
		errors.Is(err, command.ErrReservedName),
		errors.Is(err, command.ErrDuplicateName),
		errors.Is(err, workflow.ErrInvalidKind),
		errors.Is(err, workflow.ErrInvalidGoal),
		errors.Is(err, workflow.ErrInvalidStatus):
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
	default:
		writeErr(w, http.StatusInternalServerError, "internal_error", err.Error())
	}
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

type channelCreateRequest struct {
	Kind string `json:"kind"`
	feishu.UpdateInput
}

func (s *Server) handleListChannels(w http.ResponseWriter, _ *http.Request) {
	if s.channels == nil {
		writeErr(w, http.StatusServiceUnavailable, "channels_unavailable", "channels are unavailable")
		return
	}
	writeJSON(w, http.StatusOK, s.channels.List())
}

func (s *Server) handleCreateChannel(w http.ResponseWriter, r *http.Request) {
	if s.channels == nil {
		writeErr(w, http.StatusServiceUnavailable, "channels_unavailable", "channels are unavailable")
		return
	}
	var input channelCreateRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if input.Kind != "feishu" {
		writeErr(w, http.StatusBadRequest, "unsupported_channel", "unsupported channel kind")
		return
	}
	state, err := s.channels.Create(input.UpdateInput)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "channel_create_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, state)
}

func (s *Server) handleStartFeishuRegistration(w http.ResponseWriter, r *http.Request) {
	if s.channels == nil {
		writeErr(w, http.StatusServiceUnavailable, "channels_unavailable", "channels are unavailable")
		return
	}
	var input feishu.RegistrationInput
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	state, err := s.channels.StartRegistration(input)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "feishu_registration_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, state)
}

func (s *Server) handleGetFeishuRegistration(w http.ResponseWriter, r *http.Request) {
	if s.channels == nil {
		writeErr(w, http.StatusServiceUnavailable, "channels_unavailable", "channels are unavailable")
		return
	}
	state, ok := s.channels.GetRegistration(r.PathValue("id"))
	if !ok {
		writeErr(w, http.StatusNotFound, "feishu_registration_not_found", "registration not found")
		return
	}
	writeJSON(w, http.StatusOK, state)
}

func (s *Server) handleCancelFeishuRegistration(w http.ResponseWriter, r *http.Request) {
	if s.channels == nil {
		writeErr(w, http.StatusServiceUnavailable, "channels_unavailable", "channels are unavailable")
		return
	}
	if err := s.channels.CancelRegistration(r.PathValue("id")); err != nil {
		writeErr(w, http.StatusNotFound, "feishu_registration_not_found", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleGetChannel(w http.ResponseWriter, r *http.Request) {
	if s.channels == nil {
		writeErr(w, http.StatusServiceUnavailable, "channels_unavailable", "channels are unavailable")
		return
	}
	state, ok := s.channels.Get(r.PathValue("id"))
	if !ok {
		writeErr(w, http.StatusNotFound, "channel_not_found", "channel not found")
		return
	}
	writeJSON(w, http.StatusOK, state)
}

func (s *Server) handleUpdateChannel(w http.ResponseWriter, r *http.Request) {
	if s.channels == nil {
		writeErr(w, http.StatusServiceUnavailable, "channels_unavailable", "channels are unavailable")
		return
	}
	var input feishu.UpdateInput
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	state, err := s.channels.Update(r.PathValue("id"), input)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			writeErr(w, http.StatusNotFound, "channel_not_found", err.Error())
			return
		}
		writeErr(w, http.StatusBadRequest, "channel_update_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, state)
}

func (s *Server) handleDeleteChannel(w http.ResponseWriter, r *http.Request) {
	if s.channels == nil {
		writeErr(w, http.StatusServiceUnavailable, "channels_unavailable", "channels are unavailable")
		return
	}
	if err := s.channels.Delete(r.PathValue("id")); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			writeErr(w, http.StatusNotFound, "channel_not_found", err.Error())
			return
		}
		writeErr(w, http.StatusInternalServerError, "channel_delete_failed", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleListAutomations(w http.ResponseWriter, _ *http.Request) {
	if s.automations == nil {
		writeErr(w, http.StatusServiceUnavailable, "automations_unavailable", "automations are unavailable")
		return
	}
	writeJSON(w, http.StatusOK, s.automations.List())
}

func (s *Server) handleCreateAutomation(w http.ResponseWriter, r *http.Request) {
	if s.automations == nil {
		writeErr(w, http.StatusServiceUnavailable, "automations_unavailable", "automations are unavailable")
		return
	}
	var input automation.Input
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	task, err := s.automations.Create(input)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "automation_create_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, task)
}

func (s *Server) handleGetAutomation(w http.ResponseWriter, r *http.Request) {
	if s.automations == nil {
		writeErr(w, http.StatusServiceUnavailable, "automations_unavailable", "automations are unavailable")
		return
	}
	task, ok := s.automations.Get(r.PathValue("id"))
	if !ok {
		writeErr(w, http.StatusNotFound, "automation_not_found", "automation not found")
		return
	}
	writeJSON(w, http.StatusOK, task)
}

func (s *Server) handleUpdateAutomation(w http.ResponseWriter, r *http.Request) {
	if s.automations == nil {
		writeErr(w, http.StatusServiceUnavailable, "automations_unavailable", "automations are unavailable")
		return
	}
	var input automation.Input
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	task, err := s.automations.Update(r.PathValue("id"), input)
	if err != nil {
		if errors.Is(err, automation.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "automation_not_found", err.Error())
			return
		}
		writeErr(w, http.StatusBadRequest, "automation_update_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, task)
}

func (s *Server) handleDeleteAutomation(w http.ResponseWriter, r *http.Request) {
	if s.automations == nil {
		writeErr(w, http.StatusServiceUnavailable, "automations_unavailable", "automations are unavailable")
		return
	}
	if err := s.automations.Delete(r.PathValue("id")); err != nil {
		if errors.Is(err, automation.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "automation_not_found", err.Error())
			return
		}
		writeErr(w, http.StatusInternalServerError, "automation_delete_failed", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleRunAutomation(w http.ResponseWriter, r *http.Request) {
	if s.automations == nil {
		writeErr(w, http.StatusServiceUnavailable, "automations_unavailable", "automations are unavailable")
		return
	}
	task, err := s.automations.RunNow(r.PathValue("id"))
	if err != nil {
		switch {
		case errors.Is(err, automation.ErrNotFound):
			writeErr(w, http.StatusNotFound, "automation_not_found", err.Error())
		case errors.Is(err, automation.ErrAlreadyRunning):
			writeErr(w, http.StatusConflict, "automation_running", err.Error())
		default:
			writeErr(w, http.StatusBadRequest, "automation_run_failed", err.Error())
		}
		return
	}
	writeJSON(w, http.StatusAccepted, task)
}

func (s *Server) handleGetFeishuBot(w http.ResponseWriter, _ *http.Request) {
	if s.channels == nil {
		writeErr(w, http.StatusServiceUnavailable, "channels_unavailable", "channels are unavailable")
		return
	}
	items := s.channels.List()
	if len(items) == 0 {
		writeJSON(w, http.StatusOK, feishu.State{
			Kind: "feishu", Name: "飞书 Bot", ApprovalMode: approvalpkg.ModeAuto,
			Status: feishu.StatusStopped,
		})
		return
	}
	writeJSON(w, http.StatusOK, items[0])
}

func (s *Server) handleUpdateFeishuBot(w http.ResponseWriter, r *http.Request) {
	if s.channels == nil {
		writeErr(w, http.StatusServiceUnavailable, "channels_unavailable", "channels are unavailable")
		return
	}
	var input feishu.UpdateInput
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if input.Name == "" {
		input.Name = "飞书 Bot"
	}
	items := s.channels.List()
	var (
		state feishu.State
		err   error
	)
	if len(items) == 0 {
		state, err = s.channels.Create(input)
	} else {
		state, err = s.channels.Update(items[0].ID, input)
	}
	if err != nil {
		writeErr(w, http.StatusBadRequest, "channel_update_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, state)
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

func (s *Server) handlePlugins(w http.ResponseWriter, _ *http.Request) {
	items, err := s.backend.Plugins()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "plugins_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) handleInstallPlugin(w http.ResponseWriter, r *http.Request) {
	var input protocol.PluginInstallRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if strings.TrimSpace(input.Source) == "" {
		writeErr(w, http.StatusBadRequest, "bad_request", "source is required")
		return
	}
	item, err := s.backend.InstallPlugin(r.Context(), input.Source, input.Replace)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "plugin_install_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func (s *Server) handlePluginMarketplaces(w http.ResponseWriter, _ *http.Request) {
	items, err := s.backend.PluginMarketplaces()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "plugin_marketplaces_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) handleAddPluginMarketplace(w http.ResponseWriter, r *http.Request) {
	var input protocol.MarketplaceAddRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if strings.TrimSpace(input.Source) == "" {
		writeErr(w, http.StatusBadRequest, "bad_request", "source is required")
		return
	}
	item, err := s.backend.AddPluginMarketplace(
		r.Context(),
		plugin.MarketplaceRegistrationInput{
			Source: input.Source, Ref: input.Ref, SparsePaths: input.SparsePaths,
		},
	)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "plugin_marketplace_add_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func (s *Server) handleBrowsePluginMarketplace(w http.ResponseWriter, r *http.Request) {
	item, err := s.backend.BrowsePluginMarketplace(r.Context(), r.PathValue("name"))
	if err != nil {
		status := http.StatusBadGateway
		if errors.Is(err, plugin.ErrMarketplaceNotFound) {
			status = http.StatusNotFound
		}
		writeErr(w, status, "plugin_marketplace_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) handleSetPluginMarketplaceEnabled(w http.ResponseWriter, r *http.Request) {
	var input protocol.MarketplaceEnabledRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if err := s.backend.SetPluginMarketplaceEnabled(
		r.PathValue("name"),
		input.Enabled,
	); err != nil {
		writeErr(w, http.StatusBadRequest, "plugin_marketplace_update_failed", err.Error())
		return
	}
	items, err := s.backend.PluginMarketplaces()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "plugin_marketplaces_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) handleRefreshPluginMarketplace(w http.ResponseWriter, r *http.Request) {
	item, err := s.backend.RefreshPluginMarketplace(r.Context(), r.PathValue("name"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "plugin_marketplace_refresh_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) handleRemovePluginMarketplace(w http.ResponseWriter, r *http.Request) {
	if err := s.backend.RemovePluginMarketplace(r.PathValue("name")); err != nil {
		writeErr(w, http.StatusBadRequest, "plugin_marketplace_remove_failed", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handlePreviewMarketplacePlugin(w http.ResponseWriter, r *http.Request) {
	item, err := s.backend.PreviewMarketplacePlugin(
		r.Context(),
		r.PathValue("name"),
		r.PathValue("plugin"),
	)
	if err != nil {
		status := http.StatusBadGateway
		if errors.Is(err, plugin.ErrMarketplaceNotFound) ||
			errors.Is(err, plugin.ErrMarketplacePluginNotFound) {
			status = http.StatusNotFound
		}
		writeErr(w, status, "plugin_preview_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) handleInstallMarketplacePlugin(w http.ResponseWriter, r *http.Request) {
	var input protocol.MarketplacePluginInstallRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	item, err := s.backend.InstallMarketplacePlugin(
		r.Context(),
		r.PathValue("name"),
		r.PathValue("plugin"),
		input.Replace,
	)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, plugin.ErrMarketplaceNotFound) ||
			errors.Is(err, plugin.ErrMarketplacePluginNotFound) {
			status = http.StatusNotFound
		}
		writeErr(w, status, "plugin_install_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func (s *Server) handleSetPluginEnabled(w http.ResponseWriter, r *http.Request) {
	var input protocol.PluginEnabledRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if err := s.backend.SetPluginEnabled(r.PathValue("name"), input.Enabled); err != nil {
		writeErr(w, http.StatusBadRequest, "plugin_update_failed", err.Error())
		return
	}
	items, err := s.backend.Plugins()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "plugins_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) handleRemovePlugin(w http.ResponseWriter, r *http.Request) {
	if err := s.backend.RemovePlugin(r.PathValue("name")); err != nil {
		writeErr(w, http.StatusBadRequest, "plugin_remove_failed", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
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

func (s *Server) handleInspectSkills(w http.ResponseWriter, r *http.Request) {
	items, err := s.backend.InspectSkills(r.Context())
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
	err := s.backend.DeleteProject(r.Context(), r.PathValue("id"))
	if err != nil {
		switch {
		case errors.Is(err, project.ErrNotFound):
			writeErr(w, http.StatusNotFound, "project_not_found", err.Error())
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

func (s *Server) handleInspectProjectSkills(w http.ResponseWriter, r *http.Request) {
	items, err := s.backend.InspectProjectSkills(r.Context(), r.PathValue("id"))
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

func (s *Server) handleSetSkillPinned(w http.ResponseWriter, r *http.Request) {
	var input protocol.SkillPinnedRequest
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	ref := strings.TrimSpace(r.PathValue("ref"))
	if ref == "" {
		writeErr(w, http.StatusBadRequest, "bad_request", "skill ref is required")
		return
	}
	if err := s.backend.SetSkillPinned(ref, input.Pinned); err != nil {
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

func (s *Server) handleResolveBrowserAction(w http.ResponseWriter, r *http.Request) {
	var input protocol.BrowserActionResultRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	requestID := r.PathValue("request_id")
	if input.RequestID != "" && input.RequestID != requestID {
		writeErr(w, http.StatusBadRequest, "bad_request", "browser action request ID mismatch")
		return
	}
	var screenshot []byte
	if input.ScreenshotBase64 != "" {
		if input.MediaType != "image/png" {
			writeErr(w, http.StatusBadRequest, "bad_request", "unsupported browser screenshot type")
			return
		}
		const maxScreenshotBytes = 20 * 1024 * 1024
		if base64.StdEncoding.DecodedLen(len(input.ScreenshotBase64)) > maxScreenshotBytes {
			writeErr(w, http.StatusRequestEntityTooLarge, "browser_screenshot_too_large", "browser screenshot exceeds size limit")
			return
		}
		var err error
		screenshot, err = base64.StdEncoding.DecodeString(input.ScreenshotBase64)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "bad_request", "invalid browser screenshot")
			return
		}
	}
	err := s.backend.ResolveBrowserAction(
		r.PathValue("id"),
		requestID,
		browseruse.ActionResult{
			URL: input.URL, Title: input.Title, Revision: input.Revision,
			ObservationID: input.ObservationID,
			Snapshot:      input.Snapshot, Screenshot: screenshot,
			MediaType: input.MediaType,
			Code:      input.Code, Message: input.Message,
			PreURL: input.PreURL, PostURL: input.PostURL,
			Verified: input.Verified, ActualText: input.ActualText,
			Trace: input.Trace, Error: input.Error,
		},
	)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, browseruse.ErrNotFound) || errors.Is(err, session.ErrNotFound) {
			status = http.StatusNotFound
		}
		writeErr(w, status, "browser_action_failed", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

// handleCreateSession 新建会话。模型由调用方显式指定。
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

func (s *Server) handleForkSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req protocol.ForkSessionRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}

	sess, err := s.backend.ForkSession(r.Context(), id, backend.ForkSessionOptions{
		Title:      req.Title,
		ThroughSeq: event.Seq(req.ThroughSeq),
	})
	if err != nil {
		switch {
		case errors.Is(err, session.ErrNotFound):
			writeErr(w, http.StatusNotFound, "not_found", err.Error())
		case errors.Is(err, backend.ErrActiveMessageNotFound):
			writeErr(w, http.StatusNotFound, "message_not_found", err.Error())
		case errors.Is(err, backend.ErrInvalidForkBoundary):
			writeErr(w, http.StatusBadRequest, "invalid_fork_boundary", err.Error())
		case errors.Is(err, backend.ErrSessionBusy):
			writeErr(w, http.StatusConflict, "session_busy", err.Error())
		default:
			writeErr(w, http.StatusInternalServerError, "fork_failed", err.Error())
		}
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
	modelSettings := make(map[string]protocol.ModelSettings, len(connection.ModelSettings))
	for model, settings := range connection.ModelSettings {
		modelSettings[model] = protocol.ModelSettings{
			ContextWindow:    settings.ContextWindow,
			MaxInputTokens:   settings.MaxInputTokens,
			MaxOutputTokens:  settings.MaxOutputTokens,
			ImageInput:       settings.ImageInput,
			ImageGeneration:  settings.ImageGeneration,
			VideoGeneration:  settings.VideoGeneration,
			AudioGeneration:  settings.AudioGeneration,
			ToolCalling:      settings.ToolCalling,
			WebSearch:        settings.WebSearch,
			ReasoningEfforts: append([]string(nil), settings.ReasoningEfforts...),
		}
	}
	return protocol.ConnectionConfig{
		ID:            connection.ID,
		Name:          connection.Name,
		Type:          connection.Type,
		VideoProtocol: connection.VideoProtocol,
		Kind:          connection.Kind,
		AuthKind:      connection.AuthKind,
		BaseURL:       connection.BaseURL,
		APIKey:        connection.APIKey,
		HasAPIKey:     connection.APIKey != "",
		ModelSettings: modelSettings,
		Models:        append([]string(nil), connection.Models...),
		ModelsCached:  connection.ModelsCached,
		SortOrder:     connection.SortOrder,
	}
}

func toConnection(input protocol.ConnectionConfig) config.Connection {
	modelSettings := make(map[string]config.ModelSettings, len(input.ModelSettings))
	for model, settings := range input.ModelSettings {
		modelSettings[model] = config.ModelSettings{
			ContextWindow:    settings.ContextWindow,
			MaxInputTokens:   settings.MaxInputTokens,
			MaxOutputTokens:  settings.MaxOutputTokens,
			ImageInput:       settings.ImageInput,
			ImageGeneration:  settings.ImageGeneration,
			VideoGeneration:  settings.VideoGeneration,
			AudioGeneration:  settings.AudioGeneration,
			ToolCalling:      settings.ToolCalling,
			WebSearch:        settings.WebSearch,
			ReasoningEfforts: append([]string(nil), settings.ReasoningEfforts...),
		}
	}
	return config.Connection{
		ID:            input.ID,
		Name:          input.Name,
		Type:          input.Type,
		VideoProtocol: input.VideoProtocol,
		Kind:          input.Kind,
		AuthKind:      input.AuthKind,
		BaseURL:       input.BaseURL,
		APIKey:        input.APIKey,
		ModelSettings: modelSettings,
		Models:        append([]string(nil), input.Models...),
		ModelsCached:  input.ModelsCached,
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
	models, err := s.backend.ListModels(r.Context(), r.PathValue("id"), r.URL.Query().Get("refresh") == "true")
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

func (s *Server) handleGetDefaultModels(w http.ResponseWriter, _ *http.Request) {
	defaults, err := s.backend.DefaultModels()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "default_models_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, defaults)
}

func (s *Server) handleUpdateDefaultModels(w http.ResponseWriter, r *http.Request) {
	var defaults config.DefaultModels
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&defaults); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	updated, err := s.backend.UpdateDefaultModels(defaults)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "default_models_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

type canvasCreateRequest struct {
	Title     string `json:"title,omitempty"`
	SessionID string `json:"session_id,omitempty"`
	ProjectID string `json:"project_id,omitempty"`
}

type canvasUpdateRequest struct {
	ExpectedRevision uint64           `json:"expected_revision"`
	Title            *string          `json:"title,omitempty"`
	Nodes            *[]canvas.Node   `json:"nodes,omitempty"`
	Edges            *[]canvas.Edge   `json:"edges,omitempty"`
	Viewport         *canvas.Viewport `json:"viewport,omitempty"`
	Background       *string          `json:"background,omitempty"`
}

type canvasGenerateImageRequest struct {
	ExpectedRevision uint64 `json:"expected_revision"`
	ConfigNodeID     string `json:"config_node_id"`
	OutputNodeID     string `json:"output_node_id"`
	ConnectionID     string `json:"connection_id,omitempty"`
}

type canvasGenerateVideoRequest struct {
	ExpectedRevision uint64 `json:"expected_revision"`
	ConfigNodeID     string `json:"config_node_id"`
	OutputNodeID     string `json:"output_node_id"`
	ConnectionID     string `json:"connection_id,omitempty"`
}

func (s *Server) handleListCanvases(w http.ResponseWriter, r *http.Request) {
	items, err := s.backend.ListCanvases(r.URL.Query().Get("session_id"))
	if err != nil {
		writeCanvasErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) handleCreateCanvas(w http.ResponseWriter, r *http.Request) {
	var input canvasCreateRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil && !errors.Is(err, io.EOF) {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	doc, err := s.backend.CreateCanvas(r.Context(), canvas.CreateInput{
		Title: input.Title, SessionID: input.SessionID, ProjectID: input.ProjectID,
	})
	if err != nil {
		writeCanvasErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, doc)
}

func (s *Server) handleGetCanvas(w http.ResponseWriter, r *http.Request) {
	doc, err := s.backend.Canvas(r.PathValue("id"))
	if err != nil {
		writeCanvasErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, doc)
}

func (s *Server) handleUpdateCanvas(w http.ResponseWriter, r *http.Request) {
	var input canvasUpdateRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	doc, err := s.backend.UpdateCanvas(r.Context(), r.PathValue("id"), canvas.UpdateInput{
		ExpectedRevision: input.ExpectedRevision,
		Title:            input.Title,
		Nodes:            input.Nodes,
		Edges:            input.Edges,
		Viewport:         input.Viewport,
		Background:       input.Background,
	})
	if err != nil {
		if errors.Is(err, canvas.ErrRevisionConflict) {
			writeJSON(w, http.StatusConflict, doc)
			return
		}
		writeCanvasErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, doc)
}

func (s *Server) handleDeleteCanvas(w http.ResponseWriter, r *http.Request) {
	if err := s.backend.DeleteCanvas(r.Context(), r.PathValue("id")); err != nil {
		writeCanvasErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleUploadCanvasAsset(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, canvas.MaxAssetBytes+(1<<20))
	if err := r.ParseMultipartForm(canvas.MaxAssetBytes); err != nil {
		writeErr(w, http.StatusRequestEntityTooLarge, "asset_too_large", err.Error())
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "missing_asset", "multipart field \"file\" is required")
		return
	}
	defer file.Close()
	asset, doc, err := s.backend.PutCanvasAsset(r.Context(), r.PathValue("id"), header.Filename, header.Header.Get("Content-Type"), file)
	if err != nil {
		writeCanvasErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"asset": asset, "canvas": doc})
}

func (s *Server) handleReadCanvasAsset(w http.ResponseWriter, r *http.Request) {
	data, asset, err := s.backend.ReadCanvasAsset(r.Context(), r.PathValue("id"), r.PathValue("asset_id"))
	if err != nil {
		writeCanvasErr(w, err)
		return
	}
	w.Header().Set("Content-Type", asset.MediaType)
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func (s *Server) handleGenerateCanvasImage(w http.ResponseWriter, r *http.Request) {
	var input canvasGenerateImageRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	doc, err := s.backend.GenerateCanvasImage(r.Context(), r.PathValue("id"), canvas.GenerateImageInput{
		ExpectedRevision: input.ExpectedRevision,
		ConfigNodeID:     input.ConfigNodeID,
		OutputNodeID:     input.OutputNodeID,
		ConnectionID:     input.ConnectionID,
	})
	if err != nil {
		if errors.Is(err, canvas.ErrRevisionConflict) {
			writeJSON(w, http.StatusConflict, doc)
			return
		}
		writeCanvasErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, doc)
}

func (s *Server) handleGenerateCanvasVideo(w http.ResponseWriter, r *http.Request) {
	var input canvasGenerateVideoRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	doc, err := s.backend.GenerateCanvasVideo(r.Context(), r.PathValue("id"), canvas.GenerateVideoInput{
		ExpectedRevision: input.ExpectedRevision,
		ConfigNodeID:     input.ConfigNodeID,
		OutputNodeID:     input.OutputNodeID,
		ConnectionID:     input.ConnectionID,
	})
	if err != nil {
		if errors.Is(err, canvas.ErrRevisionConflict) {
			writeJSON(w, http.StatusConflict, doc)
			return
		}
		writeCanvasErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, doc)
}

func (s *Server) handleCanvasEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, "no_flush", "streaming unsupported")
		return
	}
	ch, err := s.backend.SubscribeCanvas(r.Context(), r.PathValue("id"))
	if err != nil {
		writeCanvasErr(w, err)
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
			fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", ev.Seq, ev.Kind, data)
			flusher.Flush()
		}
	}
}

func writeCanvasErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, canvas.ErrNotFound), errors.Is(err, canvas.ErrAssetNotFound), errors.Is(err, os.ErrNotExist), errors.Is(err, session.ErrNotFound):
		writeErr(w, http.StatusNotFound, "canvas_not_found", err.Error())
	case errors.Is(err, canvas.ErrRevisionConflict):
		writeErr(w, http.StatusConflict, "canvas_revision_conflict", err.Error())
	case errors.Is(err, canvas.ErrUnsupportedMedia):
		writeErr(w, http.StatusUnsupportedMediaType, "unsupported_canvas_media", err.Error())
	case errors.Is(err, canvas.ErrAssetTooLarge):
		writeErr(w, http.StatusRequestEntityTooLarge, "canvas_asset_too_large", err.Error())
	case errors.Is(err, backend.ErrGenerationProvider):
		writeErr(w, http.StatusBadGateway, "generation_provider_failed", err.Error())
	default:
		writeErr(w, http.StatusBadRequest, "canvas_failed", err.Error())
	}
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

// handleUsageStatistics returns application-wide usage for a supported range.
func (s *Server) handleUsageStatistics(w http.ResponseWriter, r *http.Request) {
	days := 30
	if value := r.URL.Query().Get("days"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || (parsed != 7 && parsed != 30) {
			writeErr(w, http.StatusBadRequest, "invalid_range", "days must be 7 or 30")
			return
		}
		days = parsed
	}
	statistics, err := s.backend.UsageStatistics(r.Context(), days)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "usage_statistics_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, statistics)
}

func (s *Server) handleGetFileReview(w http.ResponseWriter, r *http.Request) {
	review, err := s.backend.PendingFileReview(r.Context(), r.PathValue("id"))
	if err != nil {
		if errors.Is(err, session.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "not_found", err.Error())
		} else {
			writeErr(w, http.StatusInternalServerError, "file_review_failed", err.Error())
		}
		return
	}
	files := make([]protocol.RewindFilePreview, 0, len(review.Files))
	for _, file := range review.Files {
		files = append(files, protocol.RewindFilePreview{
			Key:       file.Key,
			Path:      file.Path,
			Status:    file.Status,
			Additions: file.Additions,
			Deletions: file.Deletions,
			Diff:      file.Diff,
		})
	}
	writeJSON(w, http.StatusOK, protocol.FileReviewResponse{
		Files:          files,
		FileStateToken: review.FileStateToken,
		ThroughSeq:     uint64(review.ThroughSeq),
	})
}

func (s *Server) handleResolveFileReview(w http.ResponseWriter, r *http.Request) {
	var req protocol.ResolveFileReviewRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	sessionID := r.PathValue("id")
	var err error
	switch req.Action {
	case "keep":
		err = s.backend.KeepFileChanges(
			r.Context(),
			sessionID,
			event.Seq(req.ExpectedThroughSeq),
		)
	case "undo":
		err = s.backend.UndoFileChanges(
			r.Context(),
			sessionID,
			event.Seq(req.ExpectedThroughSeq),
			req.ExpectedFileState,
			req.ForceFileKeys,
		)
	default:
		writeErr(w, http.StatusBadRequest, "invalid_action", "file review action must be keep or undo")
		return
	}
	if err != nil {
		switch {
		case errors.Is(err, session.ErrNotFound):
			writeErr(w, http.StatusNotFound, "not_found", err.Error())
		case errors.Is(err, backend.ErrNoPendingFileChanges):
			writeErr(w, http.StatusConflict, "no_pending_file_changes", err.Error())
		case errors.Is(err, backend.ErrFileReviewChanged):
			writeErr(w, http.StatusConflict, "file_review_changed", err.Error())
		case errors.Is(err, backend.ErrFileStateChanged):
			writeErr(w, http.StatusConflict, "file_state_changed", err.Error())
		case errors.Is(err, backend.ErrFileRewindConflict):
			writeErr(w, http.StatusConflict, "file_changed", err.Error())
		case errors.Is(err, backend.ErrSessionBusy):
			writeErr(w, http.StatusConflict, "session_busy", err.Error())
		case errors.Is(err, backend.ErrSessionQueueNotEmpty):
			writeErr(w, http.StatusConflict, "queue_not_empty", err.Error())
		default:
			writeErr(w, http.StatusInternalServerError, "file_review_failed", err.Error())
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "resolved"})
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
		Text:            req.Message,
		Attachments:     req.Attachments,
		BrowserElements: req.BrowserElements,
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

// handleRewindTurn trims active history from an active user message onward and
// returns that message so the client can place it back in the composer.
func (s *Server) handleRewindTurn(w http.ResponseWriter, r *http.Request) {
	rawSeq := r.PathValue("message_seq")
	seq, err := strconv.ParseUint(rawSeq, 10, 64)
	if err != nil || seq == 0 {
		writeErr(w, http.StatusBadRequest, "invalid_message_seq", "invalid message sequence")
		return
	}
	var req protocol.RewindTurnRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	result, err := s.backend.RewindTurn(
		r.Context(),
		r.PathValue("id"),
		event.Seq(seq),
		req.Confirm,
		event.Seq(req.ExpectedHeadSeq),
		req.ExpectedFileState,
		req.ForceFileKeys,
	)
	if err != nil {
		switch {
		case errors.Is(err, session.ErrNotFound):
			writeErr(w, http.StatusNotFound, "not_found", err.Error())
		case errors.Is(err, backend.ErrActiveUserMessageNotFound):
			writeErr(w, http.StatusNotFound, "message_not_found", err.Error())
		case errors.Is(err, backend.ErrRewindContextUnsupported):
			writeErr(w, http.StatusConflict, "rewind_context_unsupported", err.Error())
		case errors.Is(err, backend.ErrSessionBusy):
			writeErr(w, http.StatusConflict, "session_busy", err.Error())
		case errors.Is(err, backend.ErrSessionQueueNotEmpty):
			writeErr(w, http.StatusConflict, "queue_not_empty", err.Error())
		case errors.Is(err, backend.ErrHistoryChanged):
			writeErr(w, http.StatusConflict, "history_changed", err.Error())
		case errors.Is(err, backend.ErrFileRewindConflict):
			writeErr(w, http.StatusConflict, "file_changed", err.Error())
		case errors.Is(err, backend.ErrFileStateChanged):
			writeErr(w, http.StatusConflict, "file_state_changed", err.Error())
		case errors.Is(err, backend.ErrFileRewindFailed):
			writeErr(w, http.StatusInternalServerError, "file_rewind_failed", err.Error())
		default:
			writeErr(w, http.StatusInternalServerError, "rewind_failed", err.Error())
		}
		return
	}
	files := make([]protocol.RewindFilePreview, 0, len(result.Files))
	for _, file := range result.Files {
		files = append(files, protocol.RewindFilePreview{
			Key:       file.Key,
			Path:      file.Path,
			Status:    file.Status,
			Additions: file.Additions,
			Deletions: file.Deletions,
			Diff:      file.Diff,
		})
	}
	writeJSON(w, http.StatusOK, protocol.RewindTurnResponse{
		Status:         result.Status,
		Message:        result.Message,
		Files:          files,
		FileStateToken: result.FileStateToken,
		HeadSeq:        uint64(result.HeadSeq),
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
		CheckpointID:          checkpoint.CheckpointID,
		Phase:                 string(checkpoint.Phase),
		ProjectionKind:        string(checkpoint.ProjectionKind),
		Level:                 string(checkpoint.Level),
		SegmentCount:          len(checkpoint.Segments),
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

func (s *Server) handleCancelTool(w http.ResponseWriter, r *http.Request) {
	err := s.backend.CancelTool(r.PathValue("id"), r.PathValue("tool_call_id"))
	if err != nil {
		switch {
		case errors.Is(err, session.ErrNotFound):
			writeErr(w, http.StatusNotFound, "session_not_found", err.Error())
		case errors.Is(err, backend.ErrToolCallNotRunning):
			writeErr(w, http.StatusConflict, "tool_not_running", err.Error())
		default:
			writeErr(w, http.StatusInternalServerError, "tool_cancel_failed", err.Error())
		}
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleBackgroundTool(w http.ResponseWriter, r *http.Request) {
	snapshot, err := s.backend.BackgroundTool(
		r.PathValue("id"),
		r.PathValue("tool_call_id"),
	)
	if err != nil {
		switch {
		case errors.Is(err, session.ErrNotFound):
			writeErr(w, http.StatusNotFound, "session_not_found", err.Error())
		case errors.Is(err, tool.ErrBackgroundCommandNotFound):
			writeErr(w, http.StatusConflict, "tool_not_running", err.Error())
		default:
			writeErr(w, http.StatusInternalServerError, "tool_background_failed", err.Error())
		}
		return
	}
	writeJSON(w, http.StatusOK, snapshot)
}

func (s *Server) handleRevealToolCommand(w http.ResponseWriter, r *http.Request) {
	snapshot, err := s.backend.RevealToolCommand(
		r.PathValue("id"),
		r.PathValue("tool_call_id"),
	)
	if err != nil {
		switch {
		case errors.Is(err, session.ErrNotFound):
			writeErr(w, http.StatusNotFound, "session_not_found", err.Error())
		case errors.Is(err, tool.ErrBackgroundCommandNotFound):
			writeErr(w, http.StatusConflict, "tool_not_running", err.Error())
		default:
			writeErr(w, http.StatusInternalServerError, "tool_reveal_failed", err.Error())
		}
		return
	}
	writeJSON(w, http.StatusOK, snapshot)
}

func (s *Server) handleListBackgroundCommands(w http.ResponseWriter, r *http.Request) {
	items, err := s.backend.ListBackgroundCommands(r.PathValue("id"))
	if err != nil {
		if errors.Is(err, session.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "session_not_found", err.Error())
			return
		}
		writeErr(w, http.StatusInternalServerError, "background_commands_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) handleGetBackgroundCommand(w http.ResponseWriter, r *http.Request) {
	snapshot, err := s.backend.GetBackgroundCommand(
		r.PathValue("id"),
		r.PathValue("command_id"),
	)
	if err != nil {
		switch {
		case errors.Is(err, session.ErrNotFound):
			writeErr(w, http.StatusNotFound, "session_not_found", err.Error())
		case errors.Is(err, tool.ErrBackgroundCommandNotFound):
			writeErr(w, http.StatusNotFound, "background_command_not_found", err.Error())
		default:
			writeErr(w, http.StatusInternalServerError, "background_command_failed", err.Error())
		}
		return
	}
	writeJSON(w, http.StatusOK, snapshot)
}

func (s *Server) handleStopBackgroundCommand(w http.ResponseWriter, r *http.Request) {
	snapshot, err := s.backend.StopBackgroundCommand(
		r.PathValue("id"),
		r.PathValue("command_id"),
	)
	if err != nil {
		switch {
		case errors.Is(err, session.ErrNotFound):
			writeErr(w, http.StatusNotFound, "session_not_found", err.Error())
		case errors.Is(err, tool.ErrBackgroundCommandNotFound):
			writeErr(w, http.StatusNotFound, "background_command_not_found", err.Error())
		default:
			writeErr(w, http.StatusInternalServerError, "background_command_cancel_failed", err.Error())
		}
		return
	}
	writeJSON(w, http.StatusOK, snapshot)
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
		Text:            req.Message,
		Attachments:     req.Attachments,
		BrowserElements: req.BrowserElements,
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

func (s *Server) handleAnswerQuestions(w http.ResponseWriter, r *http.Request) {
	var req protocol.QuestionAnswerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	err := s.backend.AnswerQuestions(r.PathValue("id"), r.PathValue("batch_id"), req.Answers)
	if err != nil {
		writeQuestionErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleCancelQuestions(w http.ResponseWriter, r *http.Request) {
	err := s.backend.CancelQuestions(r.PathValue("id"), r.PathValue("batch_id"))
	if err != nil {
		writeQuestionErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func writeQuestionErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, session.ErrNotFound):
		writeErr(w, http.StatusNotFound, "session_not_found", err.Error())
	case errors.Is(err, question.ErrNotFound):
		writeErr(w, http.StatusNotFound, "question_not_found", err.Error())
	case errors.Is(err, question.ErrInvalidBatch):
		writeErr(w, http.StatusBadRequest, "invalid_question_answers", err.Error())
	default:
		writeErr(w, http.StatusInternalServerError, "question_unavailable", err.Error())
	}
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
	case errors.Is(err, backend.ErrImageInputUnsupported):
		writeErr(w, http.StatusBadRequest, "image_input_unsupported", err.Error())
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
