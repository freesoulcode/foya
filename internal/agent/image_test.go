package agent

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"strings"
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

func TestPersistToolArtifactsCommitsImage(t *testing.T) {
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
	refs, err := engine.persistToolArtifacts(context.Background(), "session-1", "read", tool.Result{
		Content: []tool.ContentPart{{
			Type: "image", Name: "pixel.png", MediaType: "image/png", Data: imageData,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 1 || refs[0].Name != "pixel.png" {
		t.Fatalf("tool image refs = %#v", refs)
	}
	if err := store.Delete(context.Background(), "session-1", refs[0].ID); !errors.Is(err, artifact.ErrCommitted) {
		t.Fatalf("delete tool artifact error = %v", err)
	}
}

func TestPersistToolArtifactsCommitsArtifactRef(t *testing.T) {
	store, err := artifact.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ref, err := store.PutFile(context.Background(), "session-1", "report.html", "", strings.NewReader("<h1>Report</h1>"))
	if err != nil {
		t.Fatal(err)
	}
	engine := &Engine{artifacts: store}
	refs, err := engine.persistToolArtifacts(context.Background(), "session-1", "write", tool.Result{
		Content: []tool.ContentPart{{Type: "artifact_ref", Attachment: &ref}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 1 || refs[0].Kind != "file" || refs[0].Name != "report.html" {
		t.Fatalf("tool artifact refs = %#v", refs)
	}
	if err := store.Delete(context.Background(), "session-1", refs[0].ID); !errors.Is(err, artifact.ErrCommitted) {
		t.Fatalf("delete tool artifact error = %v", err)
	}
}

func TestPersistToolArtifactsCleansRefsOnCommitError(t *testing.T) {
	base, err := artifact.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store := &commitFailingArtifactStore{FileStore: base}
	ref, err := store.PutFile(context.Background(), "session-1", "report.html", "", strings.NewReader("<h1>Report</h1>"))
	if err != nil {
		t.Fatal(err)
	}
	engine := &Engine{artifacts: store}
	refs, err := engine.persistToolArtifacts(context.Background(), "session-1", "write", tool.Result{
		Content: []tool.ContentPart{{Type: "artifact_ref", Attachment: &ref}},
	})
	if err == nil || !strings.Contains(err.Error(), "commit tool artifacts") {
		t.Fatalf("commit error = %v", err)
	}
	if len(refs) != 0 {
		t.Fatalf("refs after failed commit = %#v", refs)
	}
	if len(store.deleted) != 1 || store.deleted[0] != ref.ID {
		t.Fatalf("deleted artifacts = %#v, want %q", store.deleted, ref.ID)
	}
	if _, _, err := store.FileStore.Read(context.Background(), "session-1", ref.ID); err == nil {
		t.Fatal("artifact ref remained after failed commit")
	}
}

type commitFailingArtifactStore struct {
	*artifact.FileStore
	deleted []string
}

func (s *commitFailingArtifactStore) Commit(context.Context, string, []string) error {
	return errors.New("commit failed")
}

func (s *commitFailingArtifactStore) Delete(ctx context.Context, sessionID, artifactID string) error {
	s.deleted = append(s.deleted, artifactID)
	return s.FileStore.Delete(ctx, sessionID, artifactID)
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
