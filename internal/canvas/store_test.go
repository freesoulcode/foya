package canvas

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"testing"
)

func TestStorePersistsCanvasAssetsAndRevisions(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := store.Create(CreateInput{Title: "Board", SessionID: "session-1"})
	if err != nil {
		t.Fatal(err)
	}
	if doc.Revision != 1 || doc.Title != "Board" {
		t.Fatalf("created canvas = %#v", doc)
	}

	var raw bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 4, 3))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	if err := png.Encode(&raw, img); err != nil {
		t.Fatal(err)
	}
	asset, err := store.PutAsset(context.Background(), doc.ID, "original.png", "image/png", bytes.NewReader(raw.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if asset.Width != 4 || asset.Height != 3 || asset.Bytes != int64(raw.Len()) {
		t.Fatalf("asset = %#v", asset)
	}
	doc, _ = store.Get(doc.ID)
	if doc.Revision != 2 || len(doc.Assets) != 1 {
		t.Fatalf("canvas after asset = %#v", doc)
	}

	nodes := []Node{{ID: "image-1", Type: "image", AssetID: asset.ID, Width: 400, Height: 300}}
	nodes = append(nodes, Node{
		ID: "generation-1", Type: "generation", Width: 300, Height: 260, X: 500,
		Generation: &GenerationSpec{Mode: "image", AspectRatio: "1:1", Count: 2},
	})
	edges := []Edge{{ID: "edge-1", FromNodeID: "image-1", ToNodeID: "generation-1", Kind: "reference"}}
	viewport := Viewport{X: 100, Y: 80, Zoom: 1.25}
	updated, err := store.Update(doc.ID, UpdateInput{
		ExpectedRevision: doc.Revision,
		Nodes:            &nodes,
		Edges:            &edges,
		Viewport:         &viewport,
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Revision != 3 || len(updated.Nodes) != 2 || len(updated.Edges) != 1 || updated.Viewport.Zoom != 1.25 {
		t.Fatalf("updated canvas = %#v", updated)
	}
	if _, err := store.Update(doc.ID, UpdateInput{ExpectedRevision: 2, Nodes: &nodes}); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("revision conflict = %v", err)
	}

	restored, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	restoredDoc, ok := restored.Get(doc.ID)
	if !ok || restoredDoc.Revision != 3 || len(restoredDoc.Nodes) != 2 || len(restoredDoc.Edges) != 1 {
		t.Fatalf("restored canvas = %#v", restoredDoc)
	}
	storedBytes, storedAsset, err := restored.ReadAsset(context.Background(), doc.ID, asset.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(storedBytes, raw.Bytes()) || storedAsset.SHA256 != asset.SHA256 {
		t.Fatal("original canvas asset changed")
	}
}

func TestStoreRejectsInvalidNodesAndMedia(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	doc, err := store.Create(CreateInput{})
	if err != nil {
		t.Fatal(err)
	}
	bad := []Node{{ID: "bad", Type: "image", AssetID: "missing", Width: 100, Height: 100}}
	if _, err := store.Update(doc.ID, UpdateInput{ExpectedRevision: doc.Revision, Nodes: &bad}); err == nil {
		t.Fatal("missing asset was accepted")
	}
	if _, err := store.PutAsset(context.Background(), doc.ID, "note.txt", "text/plain", bytes.NewBufferString("text")); !errors.Is(err, ErrUnsupportedMedia) {
		t.Fatalf("unsupported media error = %v", err)
	}
}
