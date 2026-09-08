package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"

	"github.com/freesoulcode/foya/internal/automation"
	"github.com/freesoulcode/foya/internal/channel/feishu"
	"github.com/freesoulcode/foya/internal/contextdata"
	conversation "github.com/freesoulcode/foya/internal/conversation"
	"github.com/freesoulcode/foya/internal/hooks"
	interaction "github.com/freesoulcode/foya/internal/interaction"
	"github.com/freesoulcode/foya/internal/project"
	workflow "github.com/freesoulcode/foya/internal/workflow"
)

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
	Scope     workflow.Scope `json:"scope"`
	ProjectID string         `json:"project_id,omitempty"`
	Name      string         `json:"name"`
	Body      string         `json:"body,omitempty"`
}

type commandUpdateRequest struct {
	Scope       workflow.Scope `json:"scope"`
	ProjectID   string         `json:"project_id,omitempty"`
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Body        string         `json:"body"`
}

type commandExecuteRequest struct {
	Args string `json:"args,omitempty"`
}

func (s *Server) handleListCommands(w http.ResponseWriter, r *http.Request) {
	items, err := s.service.Commands(r.Context(), r.URL.Query().Get("scope"), r.URL.Query().Get("project_id"))
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
	item, err := s.service.CreateCommand(r.Context(), workflow.CreateInput{
		Scope: input.Scope, ProjectID: input.ProjectID, Name: input.Name,
		Body: input.Body,
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
	item, err := s.service.UpdateCommand(r.Context(), r.PathValue("ref"), workflow.UpdateInput{
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
	if err := s.service.DeleteCommand(
		r.Context(),
		r.PathValue("ref"),
		workflow.Scope(r.URL.Query().Get("scope")),
		r.URL.Query().Get("project_id"),
	); err != nil {
		writeCommandErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleSessionCommands(w http.ResponseWriter, r *http.Request) {
	items, err := s.service.SessionCommands(r.Context(), r.PathValue("id"))
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
	result, err := s.service.ExecuteCommand(r.Context(), r.PathValue("id"), r.PathValue("name"), input.Args)
	if err != nil {
		writeCommandErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleGetWorkflow(w http.ResponseWriter, r *http.Request) {
	record, ok, err := s.service.Workflow(r.PathValue("id"))
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
	result, err := s.service.ApproveWorkflow(r.Context(), r.PathValue("id"), r.PathValue("workflow_id"))
	if err != nil {
		writeCommandErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleCloseWorkflow(w http.ResponseWriter, r *http.Request) {
	record, err := s.service.CloseWorkflow(
		r.Context(),
		r.PathValue("id"),
		r.PathValue("workflow_id"),
	)
	if err != nil {
		writeCommandErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, record)
}

func writeCommandErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, workflow.ErrCommandNotFound), errors.Is(err, workflow.ErrNotFound), errors.Is(err, conversation.ErrNotFound):
		writeErr(w, http.StatusNotFound, "not_found", err.Error())
	case errors.Is(err, workflow.ErrInvalidScope),
		errors.Is(err, workflow.ErrInvalidName),
		errors.Is(err, workflow.ErrInvalidContent),
		errors.Is(err, workflow.ErrReservedName),
		errors.Is(err, workflow.ErrDuplicateName),
		errors.Is(err, workflow.ErrInvalidKind),
		errors.Is(err, workflow.ErrInvalidGoal),
		errors.Is(err, workflow.ErrInvalidStatus):
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
	default:
		writeErr(w, http.StatusInternalServerError, "internal_error", err.Error())
	}
}

func (s *Server) handleGetHooks(w http.ResponseWriter, r *http.Request) {
	hooks, err := s.service.Hooks(r.URL.Query().Get("scope"), r.URL.Query().Get("project_id"))
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
	if err := s.service.ReplaceHooks(input.Scope, input.ProjectID, input.Hooks); err != nil {
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
	settings, err := s.service.MemorySettings()
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
	updated, err := s.service.UpdateMemorySettings(settings)
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
			Kind: "feishu", Name: "Feishu Bot", Locale: "zh-CN",
			ApprovalMode: interaction.ModeAuto,
			Status:       feishu.StatusStopped,
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
		input.Name = "Feishu Bot"
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
	items, err := s.service.Rules(scope, projectID)
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
	item, err := s.service.CreateRule(input.Scope, input.ProjectID, input.Content, contextdata.RuleOptions{
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
	item, err := s.service.UpdateRule(r.PathValue("id"), input.Content, options...)
	if err != nil {
		writeContextErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) handleDeleteRule(w http.ResponseWriter, r *http.Request) {
	if err := s.service.DeleteRule(r.PathValue("id")); err != nil {
		writeContextErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleListMemories(w http.ResponseWriter, r *http.Request) {
	scope, projectID := contextFilter(r)
	items, err := s.service.Memories(scope, projectID)
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
	item, err := s.service.CreateMemory(input.Scope, input.ProjectID, input.Content)
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
	item, err := s.service.UpdateMemory(r.PathValue("id"), input.Content)
	if err != nil {
		writeContextErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) handleDeleteMemory(w http.ResponseWriter, r *http.Request) {
	if err := s.service.DeleteMemory(r.PathValue("id")); err != nil {
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
	ch, err := s.service.SubscribeContext(r.Context())
	if err != nil {
		writeContextErr(w, err)
		return
	}
	replay, err := s.service.ReplayContext(after)
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
