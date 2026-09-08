// Package server exposes the kernel through REST and SSE.
//
// Commands use REST and events use SSE over the configured local or remote transport.
package server

import (
	"crypto/sha256"
	"crypto/subtle"

	"encoding/hex"
	"encoding/json"
	"errors"

	"net/http"

	"strings"

	"github.com/freesoulcode/foya/internal/artifact"
	"github.com/freesoulcode/foya/internal/automation"
	kernel "github.com/freesoulcode/foya/internal/kernel"

	"github.com/freesoulcode/foya/internal/channel/feishu"

	"github.com/freesoulcode/foya/internal/config"

	conversation "github.com/freesoulcode/foya/internal/conversation"

	"github.com/freesoulcode/foya/internal/terminal"
)

// Server hosts REST and SSE routes.
type Server struct {
	cfg         config.Config
	service     *kernel.Service
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

// New creates a Server.
func New(cfg config.Config, service *kernel.Service) *Server {
	s := &Server{cfg: cfg, service: service, mux: http.NewServeMux()}
	s.routes()
	return s
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /healthz", s.handleHealth)
	s.mux.HandleFunc("GET /readyz", s.handleHealth)
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
	s.mux.HandleFunc("DELETE /sessions/{id}/workflow/{workflow_id}", s.handleCloseWorkflow)
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

func (s *Server) authenticate(next http.Handler) http.Handler {
	if s.cfg.Transport != config.TransportTCP {
		return next
	}
	expected := []byte(strings.ToLower(s.cfg.AuthTokenSHA256))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			next.ServeHTTP(w, r)
			return
		}
		scheme, token, ok := strings.Cut(r.Header.Get("Authorization"), " ")
		digest := sha256.Sum256([]byte(token))
		actual := make([]byte, hex.EncodedLen(len(digest)))
		hex.Encode(actual, digest[:])
		if !ok || !strings.EqualFold(scheme, "Bearer") || len(token) < 32 ||
			subtle.ConstantTimeCompare(actual, expected) != 1 {
			w.Header().Set("WWW-Authenticate", `Bearer realm="foya"`)
			writeErr(w, http.StatusUnauthorized, "unauthorized", "valid bearer token required")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) SetChannelManager(manager ChannelManager) {
	s.channels = manager
}

func (s *Server) SetAutomationManager(manager AutomationManager) {
	s.automations = manager
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, errCode, msg string) {
	writeJSON(w, code, ErrorResponse{Code: errCode, Message: msg})
}

func writeQueueErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, conversation.ErrNotFound):
		writeErr(w, http.StatusNotFound, "not_found", err.Error())
	case errors.Is(err, kernel.ErrQueuedMessageNotFound):
		writeErr(w, http.StatusNotFound, "queued_message_not_found", err.Error())
	case errors.Is(err, kernel.ErrEmptyMessage),
		errors.Is(err, kernel.ErrInvalidQueuePosition),
		errors.Is(err, artifact.ErrInvalidID),
		errors.Is(err, artifact.ErrUnsupportedType):
		writeErr(w, http.StatusBadRequest, "invalid_queue_message", err.Error())
	case errors.Is(err, kernel.ErrImageInputUnsupported):
		writeErr(w, http.StatusBadRequest, "image_input_unsupported", err.Error())
	default:
		writeErr(w, http.StatusInternalServerError, "queue_failed", err.Error())
	}
}

func writeTerminalErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, conversation.ErrNotFound), errors.Is(err, terminal.ErrNotFound):
		writeErr(w, http.StatusNotFound, "terminal_not_found", err.Error())
	case errors.Is(err, terminal.ErrUnavailable):
		writeErr(w, http.StatusNotImplemented, "terminal_unavailable", err.Error())
	default:
		writeErr(w, http.StatusInternalServerError, "terminal_failed", err.Error())
	}
}

// Handler returns the HTTP handler for any configured listener.
func (s *Server) Handler() http.Handler {
	return securityHeaders(s.authenticate(s.mux))
}
