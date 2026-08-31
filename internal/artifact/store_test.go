package artifact

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFileStoreImageLifecycle(t *testing.T) {
	dataDir := t.TempDir()
	store, err := NewFileStore(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	source := encodePNG(t, 4, 3)

	ref, err := store.PutImage(ctx, "session-1", "../screen.png", bytes.NewReader(source))
	if err != nil {
		t.Fatal(err)
	}
	if ref.Name != "screen.png" || ref.MediaType != "image/png" ||
		ref.Width != 4 || ref.Height != 3 || ref.SHA256 == "" {
		t.Fatalf("unexpected attachment metadata: %#v", ref)
	}
	data, canonical, err := store.Read(ctx, "session-1", ref.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, source) || canonical != ref {
		t.Fatal("stored image or canonical metadata changed")
	}
	if err := store.Commit(ctx, "session-1", []string{ref.ID}); err != nil {
		t.Fatal(err)
	}
	if err := store.Delete(ctx, "session-1", ref.ID); !errors.Is(err, ErrCommitted) {
		t.Fatalf("delete committed artifact error = %v", err)
	}

	meta, err := os.ReadFile(filepath.Join(dataDir, "artifacts", "session-1", ref.ID+".json"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte("base64")) || !bytes.Contains(meta, []byte(`"committed":true`)) {
		t.Fatalf("unexpected artifact persistence: data=%q metadata=%s", data, meta)
	}
}

func TestFileStoreRejectsInvalidAndOversizedImages(t *testing.T) {
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := store.PutImage(ctx, "session-1", "note.txt", strings.NewReader("not an image")); !errors.Is(err, ErrUnsupportedType) {
		t.Fatalf("unsupported image error = %v", err)
	}
	tooLarge := bytes.NewReader(make([]byte, MaxImageBytes+1))
	if _, err := store.PutImage(ctx, "session-1", "large.png", tooLarge); !errors.Is(err, ErrImageTooLarge) {
		t.Fatalf("large image error = %v", err)
	}
	if _, err := store.PutImage(ctx, "../escape", "image.png", bytes.NewReader(encodePNG(t, 1, 1))); !errors.Is(err, ErrInvalidID) {
		t.Fatalf("invalid session error = %v", err)
	}
	if _, err := store.PutImage(ctx, "..", "image.png", bytes.NewReader(encodePNG(t, 1, 1))); !errors.Is(err, ErrInvalidID) {
		t.Fatalf("parent session error = %v", err)
	}
}

func TestFileStoreScalesLongEdge(t *testing.T) {
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ref, err := store.PutImage(
		context.Background(),
		"session-1",
		"wide.png",
		bytes.NewReader(encodePNG(t, MaxImageEdge+2, 2)),
	)
	if err != nil {
		t.Fatal(err)
	}
	if ref.Width != MaxImageEdge || ref.Height != 1 {
		t.Fatalf("scaled dimensions = %dx%d", ref.Width, ref.Height)
	}
}

func encodePNG(t *testing.T, width, height int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	img.Set(0, 0, color.RGBA{R: 0xff, A: 0xff})
	var out bytes.Buffer
	if err := png.Encode(&out, img); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}
