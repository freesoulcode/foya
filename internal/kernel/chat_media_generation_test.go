package kernel

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/freesoulcode/foya/internal/agent"
	"github.com/freesoulcode/foya/internal/artifact"
	"github.com/freesoulcode/foya/internal/broker"
	canvas "github.com/freesoulcode/foya/internal/canvas"
	"github.com/freesoulcode/foya/internal/config"
	conversation "github.com/freesoulcode/foya/internal/conversation"
	interaction "github.com/freesoulcode/foya/internal/interaction"
	model "github.com/freesoulcode/foya/internal/model"
	"github.com/freesoulcode/foya/internal/terminal"
	"github.com/freesoulcode/foya/internal/tool"
)

func TestGenerateChatImageUsesDefaultModelAndStoresArtifact(t *testing.T) {
	const imageBase64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII="
	imageAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/images/generations" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Errorf("authorization = %q", r.Header.Get("Authorization"))
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload["model"] != "image-model" ||
			payload["prompt"] != "a glass observatory" ||
			payload["size"] != "1536x1024" ||
			payload["quality"] != "high" {
			t.Fatalf("image request = %#v", payload)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":[{"b64_json":"`+imageBase64+`"}]}`)
	}))
	defer imageAPI.Close()

	dataDir := t.TempDir()
	be, sessionID := newChatMediaTestService(t, dataDir)
	be.SetConnections([]config.Connection{{
		ID: "image", Name: "Image", Type: config.ConnectionTypeImage,
		Kind: "openai", AuthKind: "api_key", BaseURL: imageAPI.URL + "/v1", APIKey: "secret",
		Models: []string{"image-model"},
		ModelSettings: map[string]config.ModelSettings{
			"image-model": {ImageGeneration: true},
		},
	}})
	if _, err := be.UpdateDefaultModels(config.DefaultModels{
		Image: config.ModelRef{ConnectionID: "image", Model: "image-model"},
	}); err != nil {
		t.Fatal(err)
	}

	ref, err := be.GenerateChatImage(context.Background(), sessionID, "", canvas.ImageGenerationRequest{
		Prompt: "a glass observatory", AspectRatio: "16:9", Quality: "high",
	})
	if err != nil {
		t.Fatal(err)
	}
	if ref.Kind != "image" || ref.MediaType != "image/png" || !strings.HasSuffix(ref.Name, ".png") {
		t.Fatalf("image artifact = %#v", ref)
	}
	data, canonical, err := be.ReadArtifact(context.Background(), sessionID, ref.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 || canonical != ref {
		t.Fatalf("stored image = %d bytes, metadata = %#v", len(data), canonical)
	}
}

func TestGenerateChatVideoUsesExplicitModelWithoutDefault(t *testing.T) {
	videoData := []byte{0, 0, 0, 20, 'f', 't', 'y', 'p', 'i', 's', 'o', 'm', 0, 0, 0, 0}
	var serverURL string
	videoAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v3/contents/generations/tasks":
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			if payload["model"] != "video-model" ||
				payload["ratio"] != "9:16" ||
				payload["duration"] != float64(8) {
				t.Fatalf("video request = %#v", payload)
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, `{"task_id":"video-1","status":"completed","content":{"video_url":"`+serverURL+`/asset"}}`)
		case r.Method == http.MethodGet && r.URL.Path == "/asset":
			w.Header().Set("Content-Type", "video/mp4")
			_, _ = w.Write(videoData)
		default:
			http.NotFound(w, r)
		}
	}))
	defer videoAPI.Close()
	serverURL = videoAPI.URL

	dataDir := t.TempDir()
	be, sessionID := newChatMediaTestService(t, dataDir)
	be.SetConnections([]config.Connection{{
		ID: "video", Name: "Video", Type: config.ConnectionTypeVideo,
		VideoProtocol: config.VideoProtocolSeedance,
		Kind:          "openai", AuthKind: "api_key", BaseURL: videoAPI.URL + "/v1",
		Models: []string{"video-model"},
		ModelSettings: map[string]config.ModelSettings{
			"video-model": {VideoGeneration: true},
		},
	}})
	ref, err := be.GenerateChatVideo(context.Background(), sessionID, "", canvas.VideoGenerationRequest{
		Model: "video-model", Prompt: "slow camera orbit", AspectRatio: "9:16", Duration: 8,
	})
	if err != nil {
		t.Fatal(err)
	}
	if ref.Kind != "video" || ref.MediaType != "video/mp4" || !strings.HasSuffix(ref.Name, ".mp4") {
		t.Fatalf("video artifact = %#v", ref)
	}
	data, canonical, err := be.ReadArtifact(context.Background(), sessionID, ref.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, videoData) || canonical != ref {
		t.Fatalf("stored video = %v, metadata = %#v", data, canonical)
	}
}

func TestGenerateChatMediaRejectsDisabledExplicitModel(t *testing.T) {
	dataDir := t.TempDir()
	be, sessionID := newChatMediaTestService(t, dataDir)
	be.SetConnections([]config.Connection{{
		ID: "image", Name: "Image", Type: config.ConnectionTypeImage,
		Models: []string{"disabled-image-model"},
		ModelSettings: map[string]config.ModelSettings{
			"disabled-image-model": {},
		},
	}})
	_, err := be.GenerateChatImage(
		context.Background(),
		sessionID,
		"image",
		canvas.ImageGenerationRequest{Model: "disabled-image-model", Prompt: "test"},
	)
	if err == nil || !strings.Contains(err.Error(), "is not available") {
		t.Fatalf("unsupported model error = %v", err)
	}
}

func TestListChatMediaModelsReportsDefaultsAndCapabilities(t *testing.T) {
	dataDir := t.TempDir()
	be, _ := newChatMediaTestService(t, dataDir)
	be.SetConnections([]config.Connection{
		{
			ID: "image", Name: "Images", Type: config.ConnectionTypeImage,
			Models: []string{"image-default", "image-disabled"},
			ModelSettings: map[string]config.ModelSettings{
				"image-default":  {ImageGeneration: true},
				"image-disabled": {},
			},
		},
		{
			ID: "video", Name: "Seedance", Type: config.ConnectionTypeVideo,
			VideoProtocol: config.VideoProtocolSeedance,
			Models:        []string{"seedance-2.5"},
			ModelSettings: map[string]config.ModelSettings{
				"seedance-2.5": {VideoGeneration: true},
			},
		},
	})
	if _, err := be.UpdateDefaultModels(config.DefaultModels{
		Image: config.ModelRef{ConnectionID: "image", Model: "image-default"},
	}); err != nil {
		t.Fatal(err)
	}

	models, err := be.ListChatMediaModels(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 {
		t.Fatalf("media models = %#v", models)
	}
	if models[0].Model != "image-default" || !models[0].Default ||
		models[1].Model != "seedance-2.5" || models[1].Default ||
		models[1].Protocol != config.VideoProtocolSeedance {
		t.Fatalf("media models = %#v", models)
	}
	connection, modelID, err := be.chatGenerationTarget(
		context.Background(),
		config.ConnectionTypeVideo,
		"",
		"",
	)
	if err != nil {
		t.Fatal(err)
	}
	if connection.ID != "video" || modelID != "seedance-2.5" {
		t.Fatalf("sole video target = %q/%q", connection.ID, modelID)
	}
}

func newChatMediaTestService(t testing.TB, dataDir string) (*Service, string) {
	t.Helper()
	sessions := newTestSessionManager(t)
	session, err := sessions.Create(conversation.CreateOptions{Title: "Media"})
	if err != nil {
		t.Fatal(err)
	}
	log := newTestStore(t)
	bus := broker.New[conversation.Event]()
	gateway := interaction.NewGateway(bus, log)
	fallback := newControlledProvider()
	engine := agent.NewEngine(log, bus, sessions, fallback, "fallback", tool.NewRegistry(), gateway)
	service := NewService(
		sessions, log, bus, engine, gateway, terminal.NewManager(),
		func(connection config.Provider) (model.Provider, string) {
			return fallback, connection.Model
		},
		config.Provider{}, dataDir,
	)
	store, err := artifact.NewFileStore(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	service.SetArtifactStore(store)
	return service, session.ID
}
