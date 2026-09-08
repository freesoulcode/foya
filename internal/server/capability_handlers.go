package server

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/freesoulcode/foya/internal/browseruse"
	conversation "github.com/freesoulcode/foya/internal/conversation"
	"github.com/freesoulcode/foya/internal/mcpclient"
	"github.com/freesoulcode/foya/internal/plugin"
	"github.com/freesoulcode/foya/internal/project"

	"github.com/freesoulcode/foya/internal/websearch"
)

func (s *Server) handleGetMCP(w http.ResponseWriter, _ *http.Request) {
	config, err := s.service.MCPConfig()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "mcp_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, config)
}

func (s *Server) handlePlugins(w http.ResponseWriter, _ *http.Request) {
	items, err := s.service.Plugins()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "plugins_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) handleInstallPlugin(w http.ResponseWriter, r *http.Request) {
	var input PluginInstallRequest
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
	item, err := s.service.InstallPlugin(r.Context(), input.Source, input.Replace)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "plugin_install_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func (s *Server) handlePluginMarketplaces(w http.ResponseWriter, _ *http.Request) {
	items, err := s.service.PluginMarketplaces()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "plugin_marketplaces_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) handleAddPluginMarketplace(w http.ResponseWriter, r *http.Request) {
	var input MarketplaceAddRequest
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
	item, err := s.service.AddPluginMarketplace(
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
	item, err := s.service.BrowsePluginMarketplace(r.Context(), r.PathValue("name"))
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
	var input MarketplaceEnabledRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if err := s.service.SetPluginMarketplaceEnabled(
		r.PathValue("name"),
		input.Enabled,
	); err != nil {
		writeErr(w, http.StatusBadRequest, "plugin_marketplace_update_failed", err.Error())
		return
	}
	items, err := s.service.PluginMarketplaces()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "plugin_marketplaces_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) handleRefreshPluginMarketplace(w http.ResponseWriter, r *http.Request) {
	item, err := s.service.RefreshPluginMarketplace(r.Context(), r.PathValue("name"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "plugin_marketplace_refresh_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) handleRemovePluginMarketplace(w http.ResponseWriter, r *http.Request) {
	if err := s.service.RemovePluginMarketplace(r.PathValue("name")); err != nil {
		writeErr(w, http.StatusBadRequest, "plugin_marketplace_remove_failed", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handlePreviewMarketplacePlugin(w http.ResponseWriter, r *http.Request) {
	item, err := s.service.PreviewMarketplacePlugin(
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
	var input MarketplacePluginInstallRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	item, err := s.service.InstallMarketplacePlugin(
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
	var input PluginEnabledRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if err := s.service.SetPluginEnabled(r.PathValue("name"), input.Enabled); err != nil {
		writeErr(w, http.StatusBadRequest, "plugin_update_failed", err.Error())
		return
	}
	items, err := s.service.Plugins()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "plugins_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) handleRemovePlugin(w http.ResponseWriter, r *http.Request) {
	if err := s.service.RemovePlugin(r.PathValue("name")); err != nil {
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
	if err := s.service.ReplaceMCPConfig(r.Context(), config); err != nil {
		writeErr(w, http.StatusBadRequest, "mcp_update_failed", err.Error())
		return
	}
	updated, _ := s.service.MCPConfig()
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) handleMCPStatus(w http.ResponseWriter, _ *http.Request) {
	statuses, err := s.service.MCPStatuses()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "mcp_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, statuses)
}

func (s *Server) handleMCPRegistry(w http.ResponseWriter, r *http.Request) {
	items, err := s.service.SearchMCPRegistry(r.Context(), r.URL.Query().Get("search"))
	if err != nil {
		writeErr(w, http.StatusBadGateway, "mcp_registry_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) handleMCPResources(w http.ResponseWriter, r *http.Request) {
	resources, err := s.service.MCPResources(r.Context(), r.URL.Query().Get("server_id"))
	if err != nil {
		writeErr(w, http.StatusBadGateway, "mcp_resources_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resources)
}

func (s *Server) handleMCPReadResource(w http.ResponseWriter, r *http.Request) {
	var input MCPResourceReadRequest
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	result, err := s.service.MCPReadResource(r.Context(), input.ServerID, input.URI)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "mcp_resource_read_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleMCPPrompts(w http.ResponseWriter, r *http.Request) {
	prompts, err := s.service.MCPPrompts(r.Context(), r.URL.Query().Get("server_id"))
	if err != nil {
		writeErr(w, http.StatusBadGateway, "mcp_prompts_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, prompts)
}

func (s *Server) handleMCPGetPrompt(w http.ResponseWriter, r *http.Request) {
	var input MCPPromptGetRequest
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	result, err := s.service.MCPGetPrompt(r.Context(), input.ServerID, input.Name, input.Args)
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
		items, err = s.service.AllSkills(r.Context())
	} else if r.URL.Query().Get("invocable") == "true" {
		items, err = s.service.InvocableSkills(r.Context())
	} else {
		items, err = s.service.Skills(r.Context())
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "skills_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) handleInspectSkills(w http.ResponseWriter, r *http.Request) {
	items, err := s.service.InspectSkills(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "skills_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) handleListProjects(w http.ResponseWriter, _ *http.Request) {
	items, err := s.service.Projects()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "projects_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) handleCreateProject(w http.ResponseWriter, r *http.Request) {
	var input ProjectCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	item, err := s.service.RegisterProject(input.Path, input.Name)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "project_create_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func (s *Server) handleUpdateProject(w http.ResponseWriter, r *http.Request) {
	var input ProjectUpdateRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	item, err := s.service.UpdateProject(r.PathValue("id"), input.Name, input.Pinned)
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
	err := s.service.DeleteProject(r.Context(), r.PathValue("id"))
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
	var (
		items any
		err   error
	)
	if r.URL.Query().Get("all") == "true" {
		items, err = s.service.ProjectSkills(r.Context(), r.PathValue("id"))
	} else {
		items, err = s.service.InvocableProjectSkills(r.Context(), r.PathValue("id"))
	}
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
	items, err := s.service.InspectProjectSkills(r.Context(), r.PathValue("id"))
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
	var input SkillEnableRequest
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	ref := strings.TrimSpace(r.PathValue("ref"))
	if ref == "" {
		writeErr(w, http.StatusBadRequest, "bad_request", "skill ref is required")
		return
	}
	if err := s.service.SetSkillEnabled(ref, input.Enabled); err != nil {
		writeErr(w, http.StatusInternalServerError, "skill_update_failed", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleSetSkillPinned(w http.ResponseWriter, r *http.Request) {
	var input SkillPinnedRequest
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	ref := strings.TrimSpace(r.PathValue("ref"))
	if ref == "" {
		writeErr(w, http.StatusBadRequest, "bad_request", "skill ref is required")
		return
	}
	if err := s.service.SetSkillPinned(ref, input.Pinned); err != nil {
		writeErr(w, http.StatusInternalServerError, "skill_update_failed", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleGetWebSearch(w http.ResponseWriter, _ *http.Request) {
	settings, err := s.service.WebSearchSettings()
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
	if err := s.service.UpdateWebSearchSettings(settings); err != nil {
		writeErr(w, http.StatusBadRequest, "web_search_update_failed", err.Error())
		return
	}
	updated, _ := s.service.WebSearchSettings()
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) handleTestWebSearch(w http.ResponseWriter, r *http.Request) {
	var input WebSearchTestRequest
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	results, err := s.service.TestWebSearch(r.Context(), input.ProviderID, input.Query)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "web_search_test_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, results)
}

func (s *Server) handleResolveBrowserAction(w http.ResponseWriter, r *http.Request) {
	var input BrowserActionResultRequest
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
	err := s.service.ResolveBrowserAction(
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
		if errors.Is(err, browseruse.ErrNotFound) || errors.Is(err, conversation.ErrNotFound) {
			status = http.StatusNotFound
		}
		writeErr(w, status, "browser_action_failed", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
