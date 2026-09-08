package kernel

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/freesoulcode/foya/internal/agent"
	"github.com/freesoulcode/foya/internal/broker"
	canvas "github.com/freesoulcode/foya/internal/canvas"
	"github.com/freesoulcode/foya/internal/config"
	conversation "github.com/freesoulcode/foya/internal/conversation"
	interaction "github.com/freesoulcode/foya/internal/interaction"
	model "github.com/freesoulcode/foya/internal/model"
	"github.com/freesoulcode/foya/internal/terminal"
	"github.com/freesoulcode/foya/internal/tool"
)

func TestGenerateCanvasVideoStoresCompletedAsset(t *testing.T) {
	videoData := []byte{0, 0, 0, 20, 'f', 't', 'y', 'p', 'i', 's', 'o', 'm', 0, 0, 0, 0}
	var serverURL string
	videoAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v3/contents/generations/tasks":
			var request struct {
				Model    string `json:"model"`
				Duration int    `json:"duration"`
				Content  []any  `json:"content"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatal(err)
			}
			if request.Model != "doubao-seedance-2.0-mini" ||
				request.Duration != 8 ||
				len(request.Content) != 1 {
				t.Fatalf("unexpected video request: %#v", request)
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
	sessions := newTestSessionManager(t)
	log := newTestStore(t)
	bus := broker.New[conversation.Event]()
	gateway := interaction.NewGateway(bus, log)
	fallback := newControlledProvider()
	engine := agent.NewEngine(log, bus, sessions, fallback, "fallback", tool.NewRegistry(), gateway)
	be := NewService(
		sessions, log, bus, engine, gateway, terminal.NewManager(),
		func(connection config.Provider) (model.Provider, string) {
			return fallback, connection.Model
		},
		config.Provider{}, dataDir,
	)
	store, err := canvas.NewStore(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	be.SetCanvasStore(store)
	be.SetConnections([]config.Connection{{
		ID: "video", Name: "Video", Type: config.ConnectionTypeVideo,
		VideoProtocol: config.VideoProtocolSeedance, Kind: "openai", AuthKind: "api_key",
		BaseURL: videoAPI.URL + "/v1", Models: []string{"doubao-seedance-2.0-mini"},
		ModelSettings: map[string]config.ModelSettings{
			"doubao-seedance-2.0-mini": {VideoGeneration: true},
		},
	}})

	doc, err := be.CreateCanvas(context.Background(), canvas.CreateInput{Title: "Video"})
	if err != nil {
		t.Fatal(err)
	}
	nodes := []canvas.Node{
		{
			ID: "prompt", Type: "text", Text: "camera pans right",
			X: 0, Y: 0, Width: 200, Height: 100,
		},
		{
			ID: "generation", Type: "generation", Prompt: "at sunset",
			X: 300, Y: 0, Width: 300, Height: 300,
			Generation: &canvas.GenerationSpec{
				ConnectionID: "video", Mode: "video", Model: "doubao-seedance-2.0-mini",
				AspectRatio: "16:9", Duration: 8,
			},
		},
		{
			ID: "output", Type: "video", Status: "running",
			X: 700, Y: 0, Width: 420, Height: 236,
		},
	}
	edges := []canvas.Edge{
		{ID: "reference", FromNodeID: "prompt", ToNodeID: "generation", Kind: "reference"},
		{ID: "output-edge", FromNodeID: "generation", ToNodeID: "output", Kind: "output"},
	}
	doc, err = be.UpdateCanvas(context.Background(), doc.ID, canvas.UpdateInput{
		ExpectedRevision: doc.Revision, Nodes: &nodes, Edges: &edges,
	})
	if err != nil {
		t.Fatal(err)
	}

	doc, err = be.GenerateCanvasVideo(context.Background(), doc.ID, canvas.GenerateVideoInput{
		ExpectedRevision: doc.Revision,
		ConfigNodeID:     "generation",
		OutputNodeID:     "output",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Assets) != 1 || doc.Assets[0].Kind != "video" {
		t.Fatalf("assets = %#v", doc.Assets)
	}
	data, _, err := store.ReadAsset(context.Background(), doc.ID, doc.Assets[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(videoData) {
		t.Fatalf("video data = %v", data)
	}
}
