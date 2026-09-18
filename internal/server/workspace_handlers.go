package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"

	conversation "github.com/freesoulcode/foya/internal/conversation"
	kernel "github.com/freesoulcode/foya/internal/kernel"
	"github.com/freesoulcode/foya/internal/project"
)

func (s *Server) handleListWorkspaceFiles(w http.ResponseWriter, r *http.Request) {
	entries, err := s.service.ListWorkspaceFiles(r.Context(), r.PathValue("id"))
	if err != nil {
		writeWorkspaceErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, entries)
}

func (s *Server) handleReadWorkspaceFile(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("media") == "1" {
		content, mediaType, err := s.service.ReadWorkspaceMediaFile(
			r.Context(),
			r.PathValue("id"),
			r.URL.Query().Get("path"),
		)
		if err != nil {
			writeWorkspaceErr(w, err)
			return
		}
		w.Header().Set("Content-Type", mediaType)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(content)
		return
	}
	content, err := s.service.ReadWorkspaceFile(
		r.Context(),
		r.PathValue("id"),
		r.URL.Query().Get("path"),
	)
	if err != nil {
		writeWorkspaceErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, content)
}

func (s *Server) handleCreateWorkspaceEntry(w http.ResponseWriter, r *http.Request) {
	var req CreateWorkspaceEntryRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if req.Kind != "file" && req.Kind != "directory" {
		writeErr(w, http.StatusBadRequest, "bad_request", "workspace entry kind must be file or directory")
		return
	}
	path, err := s.service.CreateWorkspaceEntry(
		r.Context(),
		r.PathValue("id"),
		req.Path,
		req.Kind == "directory",
	)
	if err != nil {
		writeWorkspaceErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, path)
}

func (s *Server) handleRenameWorkspaceEntry(w http.ResponseWriter, r *http.Request) {
	var req RenameWorkspaceEntryRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	path, err := s.service.RenameWorkspaceEntry(
		r.Context(),
		r.PathValue("id"),
		req.Path,
		req.NewName,
	)
	if err != nil {
		writeWorkspaceErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, path)
}

func (s *Server) handleDeleteWorkspaceEntry(w http.ResponseWriter, r *http.Request) {
	err := s.service.DeleteWorkspaceEntry(
		r.Context(),
		r.PathValue("id"),
		r.URL.Query().Get("path"),
	)
	if err != nil {
		writeWorkspaceErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleResolveWorkspacePath(w http.ResponseWriter, r *http.Request) {
	path, err := s.service.ResolveWorkspacePath(
		r.Context(),
		r.PathValue("id"),
		r.URL.Query().Get("path"),
	)
	if err != nil {
		writeWorkspaceErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, path)
}

func writeWorkspaceErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, conversation.ErrNotFound), errors.Is(err, project.ErrNotFound), errors.Is(err, os.ErrNotExist):
		writeErr(w, http.StatusNotFound, "not_found", err.Error())
	case errors.Is(err, kernel.ErrInvalidWorkspacePath):
		writeErr(w, http.StatusBadRequest, "invalid_workspace_path", err.Error())
	case errors.Is(err, kernel.ErrUnsupportedMediaFile):
		writeErr(w, http.StatusBadRequest, "unsupported_media_file", err.Error())
	case errors.Is(err, kernel.ErrWorkspaceEntryExists), errors.Is(err, os.ErrExist):
		writeErr(w, http.StatusConflict, "workspace_entry_exists", err.Error())
	default:
		writeErr(w, http.StatusInternalServerError, "workspace_failed", err.Error())
	}
}
