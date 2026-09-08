package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
)

func (s *Server) handleStartTerminal(w http.ResponseWriter, r *http.Request) {
	var req TerminalStartRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	resource, err := s.service.StartTerminal(
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
	resource, err := s.service.AttachTerminal(r.PathValue("id"), r.PathValue("ref"))
	if err != nil {
		writeTerminalErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resource)
}

func (s *Server) handleWriteTerminal(w http.ResponseWriter, r *http.Request) {
	var req TerminalInputRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if err := s.service.WriteTerminal(r.PathValue("id"), r.PathValue("ref"), req.Input); err != nil {
		writeTerminalErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleResizeTerminal(w http.ResponseWriter, r *http.Request) {
	var req TerminalResizeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if err := s.service.ResizeTerminal(
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
	if err := s.service.StopTerminal(r.PathValue("id"), r.PathValue("ref")); err != nil {
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
	ch, err := s.service.SubscribeTerminal(
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

// handleEvents streams chat events over SSE.
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
	ch := s.service.Subscribe(ctx, id)
	flusher.Flush()

	for {
		select {
		case <-ctx.Done():
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
