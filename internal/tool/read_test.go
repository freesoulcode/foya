package tool

import (
	"bytes"
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
)

func TestReadToolReturnsImageContent(t *testing.T) {
	imageData, err := base64.StdEncoding.DecodeString(
		"iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=",
	)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "pixel.png")
	if err := os.WriteFile(path, imageData, 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := NewReadTool(allowGateway{}).Run(context.Background(), Call{
		Input: []byte(`{"path":"` + path + `"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError || len(result.Content) != 1 {
		t.Fatalf("read result = %#v", result)
	}
	part := result.Content[0]
	if part.Type != "image" || part.Name != "pixel.png" ||
		part.MediaType != "image/png" || !bytes.Equal(part.Data, imageData) {
		t.Fatalf("image content part = %#v", part)
	}
}
