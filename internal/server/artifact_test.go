package server

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/freesoulcode/foya/internal/message"
	"github.com/freesoulcode/foya/internal/protocol"
)

func TestArtifactRoutesUploadReadDeleteAndCommit(t *testing.T) {
	handler, sessionID := newQueueTestServer(t)
	imageData := testPNG(t)

	first := uploadArtifact(t, handler, sessionID, "first.png", imageData)
	read := httptest.NewRecorder()
	handler.ServeHTTP(read, httptest.NewRequest(
		http.MethodGet,
		"/sessions/"+sessionID+"/artifacts/"+first.ID,
		nil,
	))
	if read.Code != http.StatusOK || read.Header().Get("Content-Type") != "image/png" ||
		!bytes.Equal(read.Body.Bytes(), imageData) {
		t.Fatalf("read artifact response: status=%d type=%q body=%x", read.Code, read.Header().Get("Content-Type"), read.Body.Bytes())
	}
	if code := requestJSON(
		t,
		handler,
		http.MethodDelete,
		"/sessions/"+sessionID+"/artifacts/"+first.ID,
		nil,
		nil,
	); code != http.StatusNoContent {
		t.Fatalf("delete staged artifact status = %d", code)
	}

	committed := uploadArtifact(t, handler, sessionID, "committed.png", imageData)
	var submitted protocol.SubmitTurnResponse
	if code := requestJSON(
		t,
		handler,
		http.MethodPost,
		"/sessions/"+sessionID+"/turns",
		protocol.SubmitTurnRequest{Attachments: []message.AttachmentRef{{ID: committed.ID}}},
		&submitted,
	); code != http.StatusOK {
		t.Fatalf("submit image turn status = %d", code)
	}
	if submitted.Status != "started" {
		t.Fatalf("submit status = %q", submitted.Status)
	}
	if code := requestJSON(
		t,
		handler,
		http.MethodDelete,
		"/sessions/"+sessionID+"/artifacts/"+committed.ID,
		nil,
		nil,
	); code != http.StatusConflict {
		t.Fatalf("delete committed artifact status = %d", code)
	}
}

func TestArtifactUploadRejectsNonImage(t *testing.T) {
	handler, sessionID := newQueueTestServer(t)
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "note.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte("not an image")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/sessions/"+sessionID+"/artifacts", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	if response.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("upload non-image status = %d, body=%s", response.Code, response.Body.String())
	}
}

func uploadArtifact(
	t *testing.T,
	handler http.Handler,
	sessionID, name string,
	data []byte,
) message.AttachmentRef {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/sessions/"+sessionID+"/artifacts", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	if response.Code != http.StatusCreated {
		t.Fatalf("upload status = %d, body=%s", response.Code, response.Body.String())
	}
	var decoded protocol.ArtifactResponse
	if err := json.NewDecoder(response.Body).Decode(&decoded); err != nil {
		t.Fatal(err)
	}
	return decoded.Attachment
}

func testPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{G: 0xff, A: 0xff})
	var data bytes.Buffer
	if err := png.Encode(&data, img); err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}
