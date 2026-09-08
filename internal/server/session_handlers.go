package server

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strconv"

	"github.com/freesoulcode/foya/internal/agent"
	"github.com/freesoulcode/foya/internal/artifact"
	conversation "github.com/freesoulcode/foya/internal/conversation"
	interaction "github.com/freesoulcode/foya/internal/interaction"
	kernel "github.com/freesoulcode/foya/internal/kernel"

	"github.com/freesoulcode/foya/internal/project"

	"github.com/freesoulcode/foya/internal/tool"
)

// handleCreateSession creates a chat with a client-selected model.
func (s *Server) handleCreateSession(w http.ResponseWriter, r *http.Request) {
	var req CreateSessionRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}

	approval := req.ApprovalMode
	if approval == "" {
		approval = string(interaction.ModeManual)
	}

	sess, err := s.service.CreateSession(conversation.CreateOptions{
		ConnectionID:    req.ConnectionID,
		Model:           req.Model,
		ReasoningEffort: conversation.ReasoningEffort(req.ReasoningEffort),
		ProjectID:       req.ProjectID,
		ApprovalMode:    approval,
	})
	if err != nil {
		if errors.Is(err, conversation.ErrInvalidReasoningEffort) {
			writeErr(w, http.StatusBadRequest, "invalid_reasoning_effort", err.Error())
			return
		}
		if errors.Is(err, conversation.ErrInvalidApprovalMode) {
			writeErr(w, http.StatusBadRequest, "invalid_approval_mode", err.Error())
			return
		}
		if errors.Is(err, kernel.ErrConnectionNotFound) {
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
	var req ForkSessionRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}

	sess, err := s.service.ForkSession(r.Context(), id, kernel.ForkSessionOptions{
		Title:      req.Title,
		ThroughSeq: conversation.Seq(req.ThroughSeq),
	})
	if err != nil {
		switch {
		case errors.Is(err, conversation.ErrNotFound):
			writeErr(w, http.StatusNotFound, "not_found", err.Error())
		case errors.Is(err, kernel.ErrActiveMessageNotFound):
			writeErr(w, http.StatusNotFound, "message_not_found", err.Error())
		case errors.Is(err, kernel.ErrInvalidForkBoundary):
			writeErr(w, http.StatusBadRequest, "invalid_fork_boundary", err.Error())
		case errors.Is(err, kernel.ErrSessionBusy):
			writeErr(w, http.StatusConflict, "session_busy", err.Error())
		default:
			writeErr(w, http.StatusInternalServerError, "fork_failed", err.Error())
		}
		return
	}
	writeJSON(w, http.StatusOK, sess)
}

// handleUpdateSession updates mutable chat settings for subsequent operations.
func (s *Server) handleUpdateSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req UpdateSessionRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}

	if req.Title != nil {
		if _, err := s.service.RenameSession(r.Context(), id, *req.Title); err != nil {
			if errors.Is(err, conversation.ErrNotFound) {
				writeErr(w, http.StatusNotFound, "not_found", err.Error())
				return
			}
			writeErr(w, http.StatusInternalServerError, "rename_failed", err.Error())
			return
		}
	}

	if req.Pinned != nil {
		if _, err := s.service.PinSession(r.Context(), id, *req.Pinned); err != nil {
			if errors.Is(err, conversation.ErrNotFound) {
				writeErr(w, http.StatusNotFound, "not_found", err.Error())
				return
			}
			writeErr(w, http.StatusInternalServerError, "pin_failed", err.Error())
			return
		}
	}

	sess, err := s.service.UpdateSession(
		r.Context(),
		id,
		req.ConnectionID,
		req.Model,
		req.ReasoningEffort,
		req.ProjectID,
		req.ApprovalMode,
	)
	if err != nil {
		if errors.Is(err, conversation.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "not_found", err.Error())
			return
		}
		if errors.Is(err, conversation.ErrProjectLocked) {
			writeErr(w, http.StatusConflict, "project_locked", err.Error())
			return
		}
		if errors.Is(err, project.ErrNotFound) {
			writeErr(w, http.StatusBadRequest, "project_not_found", err.Error())
			return
		}
		if errors.Is(err, conversation.ErrInvalidReasoningEffort) {
			writeErr(w, http.StatusBadRequest, "invalid_reasoning_effort", err.Error())
			return
		}
		if errors.Is(err, conversation.ErrInvalidApprovalMode) {
			writeErr(w, http.StatusBadRequest, "invalid_approval_mode", err.Error())
			return
		}
		if errors.Is(err, kernel.ErrConnectionNotFound) {
			writeErr(w, http.StatusBadRequest, "connection_not_found", err.Error())
			return
		}
		writeErr(w, http.StatusInternalServerError, "update_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, sess)
}

// handleDeleteSession cancels active work, removes data, and broadcasts deletion.
func (s *Server) handleDeleteSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.service.DeleteSession(r.Context(), id); err != nil {
		if errors.Is(err, conversation.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "not_found", err.Error())
			return
		}
		writeErr(w, http.StatusInternalServerError, "delete_failed", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleListSessions lists chats.
func (s *Server) handleListSessions(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.service.ListSessions())
}

func (s *Server) handleChildSessions(w http.ResponseWriter, r *http.Request) {
	items, err := s.service.ChildSessions(r.PathValue("id"))
	if err != nil {
		if errors.Is(err, conversation.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "not_found", err.Error())
			return
		}
		writeErr(w, http.StatusInternalServerError, "children_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, items)
}

// handleHistory returns chat history projected from the event log.
func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	msgs, err := s.service.History(r.Context(), id)
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
	ref, err := s.service.PutImage(r.Context(), r.PathValue("id"), header.Filename, file)
	if err != nil {
		switch {
		case errors.Is(err, conversation.ErrNotFound):
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
	writeJSON(w, http.StatusCreated, ArtifactResponse{Attachment: ref})
}

func (s *Server) handleReadArtifact(w http.ResponseWriter, r *http.Request) {
	data, ref, err := s.service.ReadArtifact(
		r.Context(),
		r.PathValue("id"),
		r.PathValue("artifact_id"),
	)
	if err != nil {
		if errors.Is(err, conversation.ErrNotFound) || errors.Is(err, os.ErrNotExist) {
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
	err := s.service.DeleteArtifact(
		r.Context(),
		r.PathValue("id"),
		r.PathValue("artifact_id"),
	)
	if err != nil {
		if errors.Is(err, artifact.ErrCommitted) {
			writeErr(w, http.StatusConflict, "artifact_committed", err.Error())
		} else if errors.Is(err, conversation.ErrNotFound) || errors.Is(err, os.ErrNotExist) {
			writeErr(w, http.StatusNotFound, "artifact_not_found", err.Error())
		} else {
			writeErr(w, http.StatusInternalServerError, "artifact_delete_failed", err.Error())
		}
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleUsage returns token usage for the latest model request.
func (s *Server) handleUsage(w http.ResponseWriter, r *http.Request) {
	usage, err := s.service.Usage(r.Context(), r.PathValue("id"))
	if err != nil {
		if errors.Is(err, conversation.ErrNotFound) {
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
	statistics, err := s.service.UsageStatistics(r.Context(), days)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "usage_statistics_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, statistics)
}

func (s *Server) handleGetFileReview(w http.ResponseWriter, r *http.Request) {
	review, err := s.service.PendingFileReview(r.Context(), r.PathValue("id"))
	if err != nil {
		if errors.Is(err, conversation.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "not_found", err.Error())
		} else {
			writeErr(w, http.StatusInternalServerError, "file_review_failed", err.Error())
		}
		return
	}
	files := make([]RewindFilePreview, 0, len(review.Files))
	for _, file := range review.Files {
		files = append(files, RewindFilePreview{
			Key:       file.Key,
			Path:      file.Path,
			Status:    file.Status,
			Additions: file.Additions,
			Deletions: file.Deletions,
			Diff:      file.Diff,
		})
	}
	writeJSON(w, http.StatusOK, FileReviewResponse{
		Files:          files,
		FileStateToken: review.FileStateToken,
		ThroughSeq:     uint64(review.ThroughSeq),
	})
}

func (s *Server) handleResolveFileReview(w http.ResponseWriter, r *http.Request) {
	var req ResolveFileReviewRequest
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
		err = s.service.KeepFileChanges(
			r.Context(),
			sessionID,
			conversation.Seq(req.ExpectedThroughSeq),
		)
	case "undo":
		err = s.service.UndoFileChanges(
			r.Context(),
			sessionID,
			conversation.Seq(req.ExpectedThroughSeq),
			req.ExpectedFileState,
			req.ForceFileKeys,
		)
	default:
		writeErr(w, http.StatusBadRequest, "invalid_action", "file review action must be keep or undo")
		return
	}
	if err != nil {
		switch {
		case errors.Is(err, conversation.ErrNotFound):
			writeErr(w, http.StatusNotFound, "not_found", err.Error())
		case errors.Is(err, kernel.ErrNoPendingFileChanges):
			writeErr(w, http.StatusConflict, "no_pending_file_changes", err.Error())
		case errors.Is(err, kernel.ErrFileReviewChanged):
			writeErr(w, http.StatusConflict, "file_review_changed", err.Error())
		case errors.Is(err, kernel.ErrFileStateChanged):
			writeErr(w, http.StatusConflict, "file_state_changed", err.Error())
		case errors.Is(err, kernel.ErrFileRewindConflict):
			writeErr(w, http.StatusConflict, "file_changed", err.Error())
		case errors.Is(err, kernel.ErrSessionBusy):
			writeErr(w, http.StatusConflict, "session_busy", err.Error())
		case errors.Is(err, kernel.ErrSessionQueueNotEmpty):
			writeErr(w, http.StatusConflict, "queue_not_empty", err.Error())
		default:
			writeErr(w, http.StatusInternalServerError, "file_review_failed", err.Error())
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "resolved"})
}

// handleSubmitTurn atomically starts or queues a message.
func (s *Server) handleSubmitTurn(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req SubmitTurnRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	result, err := s.service.SubmitInput(r.Context(), id, conversation.UserInput{
		Text:            req.Message,
		SkillRef:        req.SkillRef,
		Attachments:     req.Attachments,
		BrowserElements: req.BrowserElements,
	})
	if err != nil {
		writeQueueErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, SubmitTurnResponse{
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
	var req RewindTurnRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	result, err := s.service.RewindTurn(
		r.Context(),
		r.PathValue("id"),
		conversation.Seq(seq),
		req.Confirm,
		conversation.Seq(req.ExpectedHeadSeq),
		req.ExpectedFileState,
		req.ForceFileKeys,
	)
	if err != nil {
		switch {
		case errors.Is(err, conversation.ErrNotFound):
			writeErr(w, http.StatusNotFound, "not_found", err.Error())
		case errors.Is(err, kernel.ErrActiveUserMessageNotFound):
			writeErr(w, http.StatusNotFound, "message_not_found", err.Error())
		case errors.Is(err, kernel.ErrRewindContextUnsupported):
			writeErr(w, http.StatusConflict, "rewind_context_unsupported", err.Error())
		case errors.Is(err, kernel.ErrSessionBusy):
			writeErr(w, http.StatusConflict, "session_busy", err.Error())
		case errors.Is(err, kernel.ErrSessionQueueNotEmpty):
			writeErr(w, http.StatusConflict, "queue_not_empty", err.Error())
		case errors.Is(err, kernel.ErrHistoryChanged):
			writeErr(w, http.StatusConflict, "history_changed", err.Error())
		case errors.Is(err, kernel.ErrFileRewindConflict):
			writeErr(w, http.StatusConflict, "file_changed", err.Error())
		case errors.Is(err, kernel.ErrFileStateChanged):
			writeErr(w, http.StatusConflict, "file_state_changed", err.Error())
		case errors.Is(err, kernel.ErrFileRewindFailed):
			writeErr(w, http.StatusInternalServerError, "file_rewind_failed", err.Error())
		default:
			writeErr(w, http.StatusInternalServerError, "rewind_failed", err.Error())
		}
		return
	}
	files := make([]RewindFilePreview, 0, len(result.Files))
	for _, file := range result.Files {
		files = append(files, RewindFilePreview{
			Key:       file.Key,
			Path:      file.Path,
			Status:    file.Status,
			Additions: file.Additions,
			Deletions: file.Deletions,
			Diff:      file.Diff,
		})
	}
	writeJSON(w, http.StatusOK, RewindTurnResponse{
		Status:         result.Status,
		Message:        result.Message,
		Files:          files,
		FileStateToken: result.FileStateToken,
		HeadSeq:        uint64(result.HeadSeq),
	})
}

// handleCompactSession manually compacts completed history for an idle session.
func (s *Server) handleCompactSession(w http.ResponseWriter, r *http.Request) {
	checkpoint, err := s.service.CompactSession(r.Context(), r.PathValue("id"))
	if err != nil {
		switch {
		case errors.Is(err, conversation.ErrNotFound):
			writeErr(w, http.StatusNotFound, "not_found", err.Error())
		case errors.Is(err, kernel.ErrSessionBusy):
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
	writeJSON(w, http.StatusOK, CompactSessionResponse{
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

// handleCancelTurn stops the active turn for a chat.
func (s *Server) handleCancelTurn(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s.service.CancelTurn(id)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleCancelTool(w http.ResponseWriter, r *http.Request) {
	err := s.service.CancelTool(r.PathValue("id"), r.PathValue("tool_call_id"))
	if err != nil {
		switch {
		case errors.Is(err, conversation.ErrNotFound):
			writeErr(w, http.StatusNotFound, "session_not_found", err.Error())
		case errors.Is(err, kernel.ErrToolCallNotRunning):
			writeErr(w, http.StatusConflict, "tool_not_running", err.Error())
		default:
			writeErr(w, http.StatusInternalServerError, "tool_cancel_failed", err.Error())
		}
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleBackgroundTool(w http.ResponseWriter, r *http.Request) {
	snapshot, err := s.service.BackgroundTool(
		r.PathValue("id"),
		r.PathValue("tool_call_id"),
	)
	if err != nil {
		switch {
		case errors.Is(err, conversation.ErrNotFound):
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
	snapshot, err := s.service.RevealToolCommand(
		r.PathValue("id"),
		r.PathValue("tool_call_id"),
	)
	if err != nil {
		switch {
		case errors.Is(err, conversation.ErrNotFound):
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
	items, err := s.service.ListBackgroundCommands(r.PathValue("id"))
	if err != nil {
		if errors.Is(err, conversation.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "session_not_found", err.Error())
			return
		}
		writeErr(w, http.StatusInternalServerError, "background_commands_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) handleGetBackgroundCommand(w http.ResponseWriter, r *http.Request) {
	snapshot, err := s.service.GetBackgroundCommand(
		r.PathValue("id"),
		r.PathValue("command_id"),
	)
	if err != nil {
		switch {
		case errors.Is(err, conversation.ErrNotFound):
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
	snapshot, err := s.service.StopBackgroundCommand(
		r.PathValue("id"),
		r.PathValue("command_id"),
	)
	if err != nil {
		switch {
		case errors.Is(err, conversation.ErrNotFound):
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
	items, err := s.service.ListQueuedMessages(r.PathValue("id"))
	if err != nil {
		writeQueueErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

// handleEnqueueMessage explicitly appends a message without starting it.
func (s *Server) handleEnqueueMessage(w http.ResponseWriter, r *http.Request) {
	var req QueueMessageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	item, err := s.service.EnqueueInput(r.Context(), r.PathValue("id"), conversation.UserInput{
		Text:            req.Message,
		SkillRef:        req.SkillRef,
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
	var req UpdateQueuedMessageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	item, err := s.service.UpdateQueuedMessage(
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
	err := s.service.DeleteQueuedMessage(
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
	item, err := s.service.DispatchQueuedMessage(
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

// handleResolveApproval applies a client approval decision.
func (s *Server) handleResolveApproval(w http.ResponseWriter, r *http.Request) {
	requestID := r.PathValue("request_id")
	var req ApprovalDecisionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if req.RequestID == "" {
		req.RequestID = requestID
	}
	if err := s.service.ResolveApproval(req.RequestID, req.Decision); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_approval_decision", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleAnswerQuestions(w http.ResponseWriter, r *http.Request) {
	var req QuestionAnswerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	err := s.service.AnswerQuestions(r.PathValue("id"), r.PathValue("batch_id"), req.Answers)
	if err != nil {
		writeQuestionErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleCancelQuestions(w http.ResponseWriter, r *http.Request) {
	err := s.service.CancelQuestions(r.PathValue("id"), r.PathValue("batch_id"))
	if err != nil {
		writeQuestionErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func writeQuestionErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, conversation.ErrNotFound):
		writeErr(w, http.StatusNotFound, "session_not_found", err.Error())
	case errors.Is(err, interaction.ErrNotFound):
		writeErr(w, http.StatusNotFound, "question_not_found", err.Error())
	case errors.Is(err, interaction.ErrInvalidBatch):
		writeErr(w, http.StatusBadRequest, "invalid_question_answers", err.Error())
	default:
		writeErr(w, http.StatusInternalServerError, "question_unavailable", err.Error())
	}
}
