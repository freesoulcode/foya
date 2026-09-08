package tool

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	canvas "github.com/freesoulcode/foya/internal/canvas"
)

type canvasStore interface {
	List(sessionID string) []canvas.Document
	Get(id string) (canvas.Document, bool)
	Update(id string, input canvas.UpdateInput) (canvas.Document, error)
}

type canvasNotifier func(context.Context, canvas.Document)

type canvasTool struct {
	action string
	store  canvasStore
	notify canvasNotifier
}

func CanvasTools(store canvasStore, notify canvasNotifier) []Tool {
	return []Tool{
		&canvasTool{action: "inspect", store: store, notify: notify},
		&canvasTool{action: "add_text", store: store, notify: notify},
		&canvasTool{action: "add_generation", store: store, notify: notify},
		&canvasTool{action: "connect", store: store, notify: notify},
		&canvasTool{action: "move", store: store, notify: notify},
	}
}

func (t *canvasTool) Name() string       { return "canvas_" + t.action }
func (t *canvasTool) Exposure() Exposure { return ExposureDeferred }
func (t *canvasTool) Description() string {
	switch t.action {
	case "inspect":
		return "Inspect a visual canvas and return its nodes, assets, and revision. If canvas_id is omitted, inspect the current session canvas."
	case "add_text":
		return "Add a positioned text node to a visual canvas. Read the canvas first and pass its current revision."
	case "add_generation":
		return "Add an image or video generation configuration node to a creative canvas."
	case "connect":
		return "Connect an upstream prompt or media node to a downstream generation node."
	default:
		return "Move canvas nodes by a world-coordinate offset. Read the canvas first and pass its current revision."
	}
}

func (t *canvasTool) Spec() []byte {
	switch t.action {
	case "inspect":
		return []byte(`{"type":"object","properties":{"canvas_id":{"type":"string"}},"additionalProperties":false}`)
	case "add_text":
		return []byte(`{"type":"object","properties":{"canvas_id":{"type":"string"},"expected_revision":{"type":"integer","minimum":1},"text":{"type":"string","minLength":1},"x":{"type":"number"},"y":{"type":"number"},"width":{"type":"number","minimum":24},"height":{"type":"number","minimum":24}},"required":["expected_revision","text","x","y"],"additionalProperties":false}`)
	case "add_generation":
		return []byte(`{"type":"object","properties":{"canvas_id":{"type":"string"},"expected_revision":{"type":"integer","minimum":1},"mode":{"type":"string","enum":["image","video"]},"prompt":{"type":"string"},"model":{"type":"string"},"aspect_ratio":{"type":"string"},"x":{"type":"number"},"y":{"type":"number"}},"required":["expected_revision","mode","x","y"],"additionalProperties":false}`)
	case "connect":
		return []byte(`{"type":"object","properties":{"canvas_id":{"type":"string"},"expected_revision":{"type":"integer","minimum":1},"from_node_id":{"type":"string","minLength":1},"to_node_id":{"type":"string","minLength":1},"kind":{"type":"string","enum":["reference","variation","output"]}},"required":["expected_revision","from_node_id","to_node_id"],"additionalProperties":false}`)
	default:
		return []byte(`{"type":"object","properties":{"canvas_id":{"type":"string"},"expected_revision":{"type":"integer","minimum":1},"node_ids":{"type":"array","items":{"type":"string"},"minItems":1},"dx":{"type":"number"},"dy":{"type":"number"}},"required":["expected_revision","node_ids","dx","dy"],"additionalProperties":false}`)
	}
}

type canvasParams struct {
	CanvasID         string   `json:"canvas_id"`
	ExpectedRevision uint64   `json:"expected_revision"`
	Text             string   `json:"text"`
	X                float64  `json:"x"`
	Y                float64  `json:"y"`
	Width            float64  `json:"width"`
	Height           float64  `json:"height"`
	NodeIDs          []string `json:"node_ids"`
	DX               float64  `json:"dx"`
	DY               float64  `json:"dy"`
	Mode             string   `json:"mode"`
	Prompt           string   `json:"prompt"`
	Model            string   `json:"model"`
	AspectRatio      string   `json:"aspect_ratio"`
	FromNodeID       string   `json:"from_node_id"`
	ToNodeID         string   `json:"to_node_id"`
	Kind             string   `json:"kind"`
}

func (t *canvasTool) Run(ctx context.Context, call Call) (Result, error) {
	var params canvasParams
	if err := json.Unmarshal(call.Input, &params); err != nil {
		return errResult("invalid arguments: " + err.Error()), nil
	}
	doc, err := t.resolve(ctx, params.CanvasID)
	if err != nil {
		return errResult(err.Error()), nil
	}
	if t.action == "inspect" {
		data, _ := json.Marshal(doc)
		return textResult(string(data)), nil
	}
	if params.ExpectedRevision != doc.Revision {
		return errResult(fmt.Sprintf("canvas revision changed: current revision is %d", doc.Revision)), nil
	}
	nodes := append([]canvas.Node(nil), doc.Nodes...)
	edges := append([]canvas.Edge(nil), doc.Edges...)
	if t.action == "add_text" {
		if params.Width == 0 {
			params.Width = 240
		}
		if params.Height == 0 {
			params.Height = 120
		}
		idBytes := make([]byte, 8)
		_, _ = rand.Read(idBytes)
		nodes = append(nodes, canvas.Node{ID: "text_" + hex.EncodeToString(idBytes), Type: "text", Text: params.Text, X: params.X, Y: params.Y, Width: params.Width, Height: params.Height, ZIndex: len(nodes)})
	} else if t.action == "add_generation" {
		idBytes := make([]byte, 8)
		_, _ = rand.Read(idBytes)
		nodeID := "generation_" + hex.EncodeToString(idBytes)
		nodes = append(nodes, canvas.Node{
			ID: nodeID, Type: "generation", Title: "Generate", Prompt: params.Prompt,
			X: params.X, Y: params.Y, Width: 300, Height: 260, ZIndex: len(nodes),
			Status: "idle", Generation: &canvas.GenerationSpec{
				Mode: params.Mode, Model: params.Model, AspectRatio: params.AspectRatio, Count: 1,
			},
		})
	} else if t.action == "connect" {
		idBytes := make([]byte, 8)
		_, _ = rand.Read(idBytes)
		kind := params.Kind
		if kind == "" {
			kind = "reference"
		}
		edges = append(edges, canvas.Edge{
			ID: "edge_" + hex.EncodeToString(idBytes), FromNodeID: params.FromNodeID,
			ToNodeID: params.ToNodeID, Kind: kind,
		})
	} else {
		ids := make(map[string]struct{}, len(params.NodeIDs))
		for _, id := range params.NodeIDs {
			ids[id] = struct{}{}
		}
		for index := range nodes {
			if _, ok := ids[nodes[index].ID]; ok {
				nodes[index].X += params.DX
				nodes[index].Y += params.DY
			}
		}
	}
	updated, err := t.store.Update(doc.ID, canvas.UpdateInput{
		ExpectedRevision: params.ExpectedRevision, Nodes: &nodes, Edges: &edges,
	})
	if err != nil {
		return errResult(err.Error()), nil
	}
	if t.notify != nil {
		t.notify(ctx, updated)
	}
	data, _ := json.Marshal(updated)
	return textResult(string(data)), nil
}

func (t *canvasTool) resolve(ctx context.Context, id string) (canvas.Document, error) {
	if id != "" {
		if doc, ok := t.store.Get(id); ok {
			return doc, nil
		}
		return canvas.Document{}, canvas.ErrNotFound
	}
	items := t.store.List(SessionIDFromContext(ctx))
	if len(items) == 0 {
		return canvas.Document{}, errors.New("current session has no canvas")
	}
	return items[0], nil
}
