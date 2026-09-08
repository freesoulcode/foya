package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"

	canvas "github.com/freesoulcode/foya/internal/canvas"
	conversation "github.com/freesoulcode/foya/internal/conversation"
	kernel "github.com/freesoulcode/foya/internal/kernel"
)

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
	items, err := s.service.ListCanvases(r.URL.Query().Get("session_id"))
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
	doc, err := s.service.CreateCanvas(r.Context(), canvas.CreateInput{
		Title: input.Title, SessionID: input.SessionID, ProjectID: input.ProjectID,
	})
	if err != nil {
		writeCanvasErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, doc)
}

func (s *Server) handleGetCanvas(w http.ResponseWriter, r *http.Request) {
	doc, err := s.service.Canvas(r.PathValue("id"))
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
	doc, err := s.service.UpdateCanvas(r.Context(), r.PathValue("id"), canvas.UpdateInput{
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
	if err := s.service.DeleteCanvas(r.Context(), r.PathValue("id")); err != nil {
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
	asset, doc, err := s.service.PutCanvasAsset(r.Context(), r.PathValue("id"), header.Filename, header.Header.Get("Content-Type"), file)
	if err != nil {
		writeCanvasErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"asset": asset, "canvas": doc})
}

func (s *Server) handleReadCanvasAsset(w http.ResponseWriter, r *http.Request) {
	data, asset, err := s.service.ReadCanvasAsset(r.Context(), r.PathValue("id"), r.PathValue("asset_id"))
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
	doc, err := s.service.GenerateCanvasImage(r.Context(), r.PathValue("id"), canvas.GenerateImageInput{
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
	doc, err := s.service.GenerateCanvasVideo(r.Context(), r.PathValue("id"), canvas.GenerateVideoInput{
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
	ch, err := s.service.SubscribeCanvas(r.Context(), r.PathValue("id"))
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
	case errors.Is(err, canvas.ErrNotFound), errors.Is(err, canvas.ErrAssetNotFound), errors.Is(err, os.ErrNotExist), errors.Is(err, conversation.ErrNotFound):
		writeErr(w, http.StatusNotFound, "canvas_not_found", err.Error())
	case errors.Is(err, canvas.ErrRevisionConflict):
		writeErr(w, http.StatusConflict, "canvas_revision_conflict", err.Error())
	case errors.Is(err, canvas.ErrUnsupportedMedia):
		writeErr(w, http.StatusUnsupportedMediaType, "unsupported_canvas_media", err.Error())
	case errors.Is(err, canvas.ErrAssetTooLarge):
		writeErr(w, http.StatusRequestEntityTooLarge, "canvas_asset_too_large", err.Error())
	case errors.Is(err, kernel.ErrGenerationProvider):
		writeErr(w, http.StatusBadGateway, "generation_provider_failed", err.Error())
	default:
		writeErr(w, http.StatusBadRequest, "canvas_failed", err.Error())
	}
}
