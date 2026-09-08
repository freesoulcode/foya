package agent

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"testing"

	"github.com/freesoulcode/foya/internal/artifact"
	conversation "github.com/freesoulcode/foya/internal/conversation"
	modelapi "github.com/freesoulcode/foya/internal/model"
	"github.com/freesoulcode/foya/internal/tool"
)

type imageCapabilityProvider struct {
	supported bool
}

func (p imageCapabilityProvider) Name() string { return "image-test" }

func (p imageCapabilityProvider) Stream(context.Context, modelapi.Request) (<-chan modelapi.StreamEvent, error) {
	return nil, nil
}

func (p imageCapabilityProvider) ModelCapabilities(string) modelapi.ModelCapabilities {
	supported := p.supported
	return modelapi.ModelCapabilities{ImageInput: &supported}
}

func TestPersistToolImagesCommitsArtifact(t *testing.T) {
	store, err := artifact.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	imageData, err := base64.StdEncoding.DecodeString(
		"iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=",
	)
	if err != nil {
		t.Fatal(err)
	}
	engine := &Engine{artifacts: store}
	refs := engine.persistToolImages(context.Background(), "session-1", "read", tool.Result{
		Content: []tool.ContentPart{{
			Type: "image", Name: "pixel.png", MediaType: "image/png", Data: imageData,
		}},
	})
	if len(refs) != 1 || refs[0].Name != "pixel.png" {
		t.Fatalf("tool image refs = %#v", refs)
	}
	if err := store.Delete(context.Background(), "session-1", refs[0].ID); !errors.Is(err, artifact.ErrCommitted) {
		t.Fatalf("delete tool artifact error = %v", err)
	}
}

func TestMaterializeProviderMessagesHonorsImageCapability(t *testing.T) {
	store, err := artifact.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	imageData := decodeTestPNG(t)
	ref, err := store.PutImage(context.Background(), "session-1", "pixel.png", bytes.NewReader(imageData))
	if err != nil {
		t.Fatal(err)
	}
	input := []conversation.Message{{
		Role: conversation.RoleUser, Content: "describe",
		Attachments: []conversation.AttachmentRef{ref},
	}}

	engine := &Engine{provider: imageCapabilityProvider{supported: true}, artifacts: store}
	materialized, err := engine.materializeProviderMessages(context.Background(), "session-1", input, "vision")
	if err != nil {
		t.Fatal(err)
	}
	if len(materialized) != 1 || len(materialized[0].Parts) != 2 ||
		materialized[0].Parts[1].Type != "image" ||
		!bytes.Equal(materialized[0].Parts[1].Data, imageData) {
		t.Fatalf("materialized messages = %#v", materialized)
	}

	engine.provider = imageCapabilityProvider{supported: false}
	materialized, err = engine.materializeProviderMessages(context.Background(), "session-1", input, "text")
	if err != nil {
		t.Fatal(err)
	}
	if len(materialized[0].Parts) != 2 || materialized[0].Parts[1].Type != "text" {
		t.Fatalf("text-only materialization = %#v", materialized)
	}
}

func decodeTestPNG(t *testing.T) []byte {
	t.Helper()
	data, err := base64.StdEncoding.DecodeString(
		"iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=",
	)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
