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

func TestFileStoreVideoLifecycle(t *testing.T) {
	dataDir := t.TempDir()
	store, err := NewFileStore(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	source := []byte{0, 0, 0, 20, 'f', 't', 'y', 'p', 'i', 's', 'o', 'm', 0, 0, 0, 0}

	ref, err := store.PutVideo(ctx, "session-1", "../clip.mp4", "", bytes.NewReader(source))
	if err != nil {
		t.Fatal(err)
	}
	if ref.Name != "clip.mp4" || ref.Kind != "video" ||
		ref.MediaType != "video/mp4" || ref.Bytes != int64(len(source)) || ref.SHA256 == "" {
		t.Fatalf("unexpected video metadata: %#v", ref)
	}
	data, canonical, err := store.Read(ctx, "session-1", ref.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, source) || canonical != ref {
		t.Fatal("stored video or canonical metadata changed")
	}
	if err := store.Commit(ctx, "session-1", []string{ref.ID}); err != nil {
		t.Fatal(err)
	}
	if err := store.Delete(ctx, "session-1", ref.ID); !errors.Is(err, ErrCommitted) {
		t.Fatalf("delete committed video artifact error = %v", err)
	}
}

func TestFileStoreRejectsInvalidVideos(t *testing.T) {
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := store.PutVideo(ctx, "session-1", "note.txt", "", strings.NewReader("not a video")); !errors.Is(err, ErrUnsupportedVideo) {
		t.Fatalf("unsupported video error = %v", err)
	}
	if _, err := store.PutVideo(ctx, "../escape", "clip.mp4", "video/mp4", strings.NewReader("video")); !errors.Is(err, ErrInvalidID) {
		t.Fatalf("invalid session error = %v", err)
	}
}

func TestFileStoreFileLifecycle(t *testing.T) {
	dataDir := t.TempDir()
	store, err := NewFileStore(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	source := []byte("<!doctype html><title>Report</title>")

	ref, err := store.PutFile(ctx, "session-1", "reports/index.html", "", bytes.NewReader(source))
	if err != nil {
		t.Fatal(err)
	}
	if ref.Name != "index.html" || ref.Kind != "file" ||
		!strings.HasPrefix(ref.MediaType, "text/html") ||
		ref.Bytes != int64(len(source)) || ref.SHA256 == "" {
		t.Fatalf("unexpected file metadata: %#v", ref)
	}
	data, canonical, err := store.Read(ctx, "session-1", ref.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, source) || canonical != ref {
		t.Fatal("stored file or canonical metadata changed")
	}
	if err := store.Commit(ctx, "session-1", []string{ref.ID}); err != nil {
		t.Fatal(err)
	}
	if err := store.Delete(ctx, "session-1", ref.ID); !errors.Is(err, ErrCommitted) {
		t.Fatalf("delete committed file artifact error = %v", err)
	}
}

func TestFileStoreWorkspaceDir(t *testing.T) {
	dataDir := t.TempDir()
	store, err := NewFileStore(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	dir, err := store.WorkspaceDir(context.Background(), "session-1")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dataDir, "artifacts", "session-1", "workspace")
	if dir != want {
		t.Fatalf("workspace dir = %q, want %q", dir, want)
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		t.Fatalf("workspace dir was not created: info=%#v err=%v", info, err)
	}
	if _, err := store.WorkspaceDir(context.Background(), "../escape"); !errors.Is(err, ErrInvalidID) {
		t.Fatalf("invalid session workspace error = %v", err)
	}
}

func TestFileStoreCopyWorkspace(t *testing.T) {
	dataDir := t.TempDir()
	store, err := NewFileStore(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	source, err := store.WorkspaceDir(ctx, "source-session")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(source, "nested"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "nested", "report.md"), []byte("# Report"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := store.CopyWorkspace(ctx, "source-session", "target-session"); err != nil {
		t.Fatal(err)
	}
	target, err := store.WorkspaceDir(ctx, "target-session")
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(target, "nested", "report.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "# Report" {
		t.Fatalf("copied workspace data = %q", data)
	}
}

func TestFileStoreCopyWorkspacePreservesSafeSymlinks(t *testing.T) {
	dataDir := t.TempDir()
	store, err := NewFileStore(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	source, err := store.WorkspaceDir(ctx, "source-session")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "real.txt"), []byte("real"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("real.txt", filepath.Join(source, "link.txt")); err != nil {
		t.Skipf("symlinks are unavailable: %v", err)
	}
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(source, "escape.txt")); err != nil {
		t.Fatal(err)
	}
	if err := store.CopyWorkspace(ctx, "source-session", "target-session"); err != nil {
		t.Fatal(err)
	}
	target, err := store.WorkspaceDir(ctx, "target-session")
	if err != nil {
		t.Fatal(err)
	}
	linkTarget, err := os.Readlink(filepath.Join(target, "link.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if linkTarget != "real.txt" {
		t.Fatalf("copied symlink target = %q", linkTarget)
	}
	if _, err := os.Lstat(filepath.Join(target, "escape.txt")); !os.IsNotExist(err) {
		t.Fatalf("copied escaping symlink or unexpected stat error: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(target, "real.txt"))
	if err != nil || string(data) != "real" {
		t.Fatalf("copied regular file = %q, err = %v", data, err)
	}
}

func TestFileStoreRejectsInvalidAndOversizedFiles(t *testing.T) {
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	tooLarge := bytes.NewReader(make([]byte, MaxFileBytes+1))
	if _, err := store.PutFile(ctx, "session-1", "large.txt", "", tooLarge); !errors.Is(err, ErrArtifactTooLarge) {
		t.Fatalf("large file error = %v", err)
	}
	if _, err := store.PutFile(ctx, "../escape", "note.txt", "", strings.NewReader("note")); !errors.Is(err, ErrInvalidID) {
		t.Fatalf("invalid session error = %v", err)
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
