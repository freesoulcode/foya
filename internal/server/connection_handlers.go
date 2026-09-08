package server

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/freesoulcode/foya/internal/config"
	kernel "github.com/freesoulcode/foya/internal/kernel"
	model "github.com/freesoulcode/foya/internal/model"
)

func publicConnection(connection config.Connection) ConnectionConfig {
	modelSettings := make(map[string]ModelSettings, len(connection.ModelSettings))
	for model, settings := range connection.ModelSettings {
		modelSettings[model] = ModelSettings{
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
	return ConnectionConfig{
		ID:            connection.ID,
		Name:          connection.Name,
		Type:          connection.Type,
		VideoProtocol: connection.VideoProtocol,
		Kind:          connection.Kind,
		AuthKind:      connection.AuthKind,
		BaseURL:       connection.BaseURL,
		HasAPIKey:     connection.APIKey != "",
		ModelSettings: modelSettings,
		Models:        append([]string(nil), connection.Models...),
		ModelsCached:  connection.ModelsCached,
		SortOrder:     connection.SortOrder,
	}
}

func toConnection(input ConnectionConfig) config.Connection {
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
	connections := s.service.Connections()
	result := make([]ConnectionConfig, 0, len(connections))
	for _, connection := range connections {
		result = append(result, publicConnection(connection))
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleCreateConnection(w http.ResponseWriter, r *http.Request) {
	var input ConnectionConfig
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	connection, err := s.service.CreateConnection(toConnection(input))
	if err != nil {
		if errors.Is(err, kernel.ErrUnsupportedAuth) {
			writeErr(w, http.StatusBadRequest, "unsupported_auth_kind", err.Error())
			return
		}
		writeErr(w, http.StatusConflict, "connection_create_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, publicConnection(connection))
}

func (s *Server) handleUpdateConnection(w http.ResponseWriter, r *http.Request) {
	var input ConnectionConfig
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	connection, err := s.service.UpdateConnection(r.PathValue("id"), toConnection(input))
	if err != nil {
		if errors.Is(err, kernel.ErrConnectionNotFound) {
			writeErr(w, http.StatusNotFound, "connection_not_found", err.Error())
			return
		}
		if errors.Is(err, kernel.ErrUnsupportedAuth) {
			writeErr(w, http.StatusBadRequest, "unsupported_auth_kind", err.Error())
			return
		}
		writeErr(w, http.StatusBadRequest, "connection_update_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, publicConnection(connection))
}

func (s *Server) handleDeleteConnection(w http.ResponseWriter, r *http.Request) {
	err := s.service.DeleteConnection(r.PathValue("id"))
	if err != nil {
		switch {
		case errors.Is(err, kernel.ErrConnectionNotFound):
			writeErr(w, http.StatusNotFound, "connection_not_found", err.Error())
		case errors.Is(err, kernel.ErrConnectionInUse):
			writeErr(w, http.StatusConflict, "connection_in_use", err.Error())
		default:
			writeErr(w, http.StatusBadRequest, "connection_delete_failed", err.Error())
		}
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleListConnectionModels(w http.ResponseWriter, r *http.Request) {
	models, err := s.service.ListModels(r.Context(), r.PathValue("id"), r.URL.Query().Get("refresh") == "true")
	if err != nil {
		if errors.Is(err, kernel.ErrConnectionNotFound) {
			writeErr(w, http.StatusNotFound, "connection_not_found", err.Error())
			return
		}
		writeErr(w, http.StatusBadGateway, "models_failed", err.Error())
		return
	}
	ids := make([]string, 0, len(models))
	contextWindows := make(map[string]int64)
	capabilities := make(map[string]model.ModelCapabilities)
	for _, model := range models {
		ids = append(ids, model.ID)
		if model.ContextWindow > 0 {
			contextWindows[model.ID] = model.ContextWindow
		}
		if model.Capabilities.ImageInput != nil {
			capabilities[model.ID] = model.Capabilities
		}
	}
	writeJSON(w, http.StatusOK, ConnectionModelsResponse{
		Models:         ids,
		ContextWindows: contextWindows,
		Capabilities:   capabilities,
	})
}

func (s *Server) handleGetDefaultModels(w http.ResponseWriter, _ *http.Request) {
	defaults, err := s.service.DefaultModels()
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
	updated, err := s.service.UpdateDefaultModels(defaults)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "default_models_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, updated)
}
