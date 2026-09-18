package server

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"
)

func TestWorkspaceMediaRouteReturnsBinaryContent(t *testing.T) {
	handler, sessionID, service := newQueueTestServerWithService(t)
	ctx := context.Background()
	path, err := service.CreateWorkspaceEntry(ctx, sessionID, "preview.png", false)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := service.ResolveWorkspacePath(ctx, sessionID, path)
	if err != nil {
		t.Fatal(err)
	}
	imageData := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}
	if err := os.WriteFile(resolved, imageData, 0o600); err != nil {
		t.Fatal(err)
	}

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(
		http.MethodGet,
		"/sessions/"+sessionID+"/workspace/file?path="+url.QueryEscape(path)+"&media=1",
		nil,
	))
	if response.Code != http.StatusOK ||
		response.Header().Get("Content-Type") != "image/png" ||
		!bytes.Equal(response.Body.Bytes(), imageData) {
		t.Fatalf(
			"media response: status=%d type=%q body=%x",
			response.Code,
			response.Header().Get("Content-Type"),
			response.Body.Bytes(),
		)
	}
}
