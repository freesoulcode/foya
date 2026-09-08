package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"

	"github.com/freesoulcode/foya/internal/config"
	conversation "github.com/freesoulcode/foya/internal/conversation"
	"github.com/freesoulcode/foya/internal/project"
	subagent "github.com/freesoulcode/foya/internal/subagent"
)

func (s *Server) handleGetAgentLimits(w http.ResponseWriter, _ *http.Request) {
	limits, err := s.service.AgentLimits()
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
	if err := s.service.UpdateAgentLimits(limits); err != nil {
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
	item, err := s.service.StartAgent(r.Context(), subagent.SpawnRequest{
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
	items, err := s.service.AgentRuns(r.PathValue("id"))
	if err != nil {
		writeAgentRunErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) handleGetAgentRun(w http.ResponseWriter, r *http.Request) {
	item, err := s.service.AgentRun(r.PathValue("id"), r.PathValue("run_id"))
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
	items, err := s.service.WaitAgents(r.Context(), r.PathValue("id"), req.IDs, req.Mode != "any")
	if err != nil {
		writeAgentRunErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) handleCancelAgent(w http.ResponseWriter, r *http.Request) {
	if err := s.service.CancelAgent(r.PathValue("id"), r.PathValue("run_id")); err != nil {
		writeAgentRunErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleAgentBudget(w http.ResponseWriter, r *http.Request) {
	item, err := s.service.AgentBudget(r.PathValue("id"))
	if err != nil {
		writeAgentRunErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func writeAgentRunErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, conversation.ErrNotFound), errors.Is(err, os.ErrNotExist):
		writeErr(w, http.StatusNotFound, "not_found", err.Error())
	case errors.Is(err, context.Canceled):
		writeErr(w, http.StatusRequestTimeout, "cancelled", err.Error())
	default:
		writeErr(w, http.StatusBadRequest, "agent_run_failed", err.Error())
	}
}

func (s *Server) handleListAgents(w http.ResponseWriter, r *http.Request) {
	items, err := s.service.Agents(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "agents_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) handleProjectAgents(w http.ResponseWriter, r *http.Request) {
	items, err := s.service.ProjectAgents(r.Context(), r.PathValue("id"))
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
