package tool

import (
	"context"
	"errors"
	"testing"

	canvas "github.com/freesoulcode/foya/internal/canvas"
	conversation "github.com/freesoulcode/foya/internal/conversation"
)

type recordingMediaGenerator struct {
	models          []ChatMediaModel
	modelsKind      string
	modelsErr       error
	imageSession    string
	imageConnection string
	imageRequest    canvas.ImageGenerationRequest
	imageRef        conversation.AttachmentRef
	imageErr        error
	videoSession    string
	videoConnection string
	videoRequest    canvas.VideoGenerationRequest
	videoRef        conversation.AttachmentRef
	videoErr        error
}

func (g *recordingMediaGenerator) ListChatMediaModels(
	_ context.Context,
	kind string,
) ([]ChatMediaModel, error) {
	g.modelsKind = kind
	return g.models, g.modelsErr
}

func (g *recordingMediaGenerator) GenerateChatImage(
	_ context.Context,
	sessionID string,
	connectionID string,
	request canvas.ImageGenerationRequest,
) (conversation.AttachmentRef, error) {
	g.imageSession = sessionID
	g.imageConnection = connectionID
	g.imageRequest = request
	return g.imageRef, g.imageErr
}

func (g *recordingMediaGenerator) GenerateChatVideo(
	_ context.Context,
	sessionID string,
	connectionID string,
	request canvas.VideoGenerationRequest,
) (conversation.AttachmentRef, error) {
	g.videoSession = sessionID
	g.videoConnection = connectionID
	g.videoRequest = request
	return g.videoRef, g.videoErr
}

func TestMediaGenerationToolsGenerateArtifactReferences(t *testing.T) {
	generator := &recordingMediaGenerator{
		imageRef: conversation.AttachmentRef{
			ID: "image-1", Name: "generated-image.png", Kind: "image", MediaType: "image/png",
		},
		videoRef: conversation.AttachmentRef{
			ID: "video-1", Name: "generated-video.mp4", Kind: "video", MediaType: "video/mp4",
		},
	}
	tools := MediaGenerationTools(generator)
	if len(tools) != 3 {
		t.Fatalf("tool count = %d", len(tools))
	}
	byName := make(map[string]Tool, len(tools))
	for _, mediaTool := range tools {
		byName[mediaTool.Name()] = mediaTool
		if mediaTool.Exposure() != ExposureDirect {
			t.Fatalf("%s exposure = %q", mediaTool.Name(), mediaTool.Exposure())
		}
	}
	ctx := WithSessionID(context.Background(), "session-1")

	imageResult, err := byName["generate_image"].Run(ctx, Call{
		Input: []byte(`{"connection_id":"images","model":"image-model","prompt":"a glass observatory","aspect_ratio":"16:9","quality":"high"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if imageResult.IsError || len(imageResult.Content) != 2 ||
		imageResult.Content[1].Attachment == nil ||
		imageResult.Content[1].Attachment.ID != "image-1" {
		t.Fatalf("image result = %#v", imageResult)
	}
	if generator.imageSession != "session-1" || generator.imageConnection != "images" ||
		generator.imageRequest.Model != "image-model" ||
		generator.imageRequest.Prompt != "a glass observatory" ||
		generator.imageRequest.AspectRatio != "16:9" ||
		generator.imageRequest.Quality != "high" {
		t.Fatalf("image request = %#v, session = %q", generator.imageRequest, generator.imageSession)
	}

	videoResult, err := byName["generate_video"].Run(ctx, Call{
		Input: []byte(`{"connection_id":"videos","model":"video-model","prompt":"slow camera orbit","aspect_ratio":"9:16","duration":8}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if videoResult.IsError || len(videoResult.Content) != 2 ||
		videoResult.Content[1].Attachment == nil ||
		videoResult.Content[1].Attachment.ID != "video-1" {
		t.Fatalf("video result = %#v", videoResult)
	}
	if generator.videoSession != "session-1" || generator.videoConnection != "videos" ||
		generator.videoRequest.Model != "video-model" ||
		generator.videoRequest.Prompt != "slow camera orbit" ||
		generator.videoRequest.AspectRatio != "9:16" ||
		generator.videoRequest.Duration != 8 {
		t.Fatalf("video request = %#v, session = %q", generator.videoRequest, generator.videoSession)
	}
}

func TestMediaGenerationToolsApplyOptionalDefaults(t *testing.T) {
	generator := &recordingMediaGenerator{}
	tools := MediaGenerationTools(generator)
	byName := make(map[string]Tool, len(tools))
	for _, mediaTool := range tools {
		byName[mediaTool.Name()] = mediaTool
	}
	ctx := WithSessionID(context.Background(), "session-1")

	if _, err := byName["generate_image"].Run(ctx, Call{Input: []byte(`{"prompt":"image"}`)}); err != nil {
		t.Fatal(err)
	}
	if generator.imageRequest.AspectRatio != "1:1" || generator.imageRequest.Quality != "auto" {
		t.Fatalf("image defaults = %#v", generator.imageRequest)
	}
	if _, err := byName["generate_video"].Run(ctx, Call{Input: []byte(`{"prompt":"video"}`)}); err != nil {
		t.Fatal(err)
	}
	if generator.videoRequest.AspectRatio != "16:9" || generator.videoRequest.Duration != 5 {
		t.Fatalf("video defaults = %#v", generator.videoRequest)
	}
}

func TestMediaGenerationToolsReturnModelVisibleErrors(t *testing.T) {
	generator := &recordingMediaGenerator{imageErr: context.Canceled}
	var imageTool Tool
	for _, mediaTool := range MediaGenerationTools(generator) {
		if mediaTool.Name() == "generate_image" {
			imageTool = mediaTool
			break
		}
	}
	if imageTool == nil {
		t.Fatal("generate_image tool missing")
	}

	result, err := imageTool.Run(context.Background(), Call{Input: []byte(`{"prompt":"test"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError || result.Content[0].Text != "media generation requires a chat session" {
		t.Fatalf("missing session result = %#v", result)
	}

	result, err = imageTool.Run(
		WithSessionID(context.Background(), "session-1"),
		Call{Input: []byte(`{"prompt":"test"}`)},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError || result.Content[0].Text != "image generation was cancelled" {
		t.Fatalf("cancelled result = %#v", result)
	}

	generator.imageErr = errors.New("provider unavailable")
	result, err = imageTool.Run(
		WithSessionID(context.Background(), "session-1"),
		Call{Input: []byte(`{"prompt":"test"}`)},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError || result.Content[0].Text != "image generation failed: provider unavailable" {
		t.Fatalf("provider error result = %#v", result)
	}
}

func TestListMediaModelsReturnsConfiguredCatalog(t *testing.T) {
	generator := &recordingMediaGenerator{models: []ChatMediaModel{{
		Type:           "video",
		ConnectionID:   "seedance",
		ConnectionName: "Seedance",
		Model:          "seedance-2.5",
		Protocol:       "seedance",
		Default:        false,
	}}}
	var listTool Tool
	for _, mediaTool := range MediaGenerationTools(generator) {
		if mediaTool.Name() == "list_media_models" {
			listTool = mediaTool
			break
		}
	}
	if listTool == nil {
		t.Fatal("list_media_models tool missing")
	}
	result, err := listTool.Run(context.Background(), Call{Input: []byte(`{"type":"video"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError || generator.modelsKind != "video" || len(result.Content) != 1 {
		t.Fatalf("list result = %#v, kind = %q", result, generator.modelsKind)
	}
	if result.Content[0].Text != `{"models":[{"type":"video","connection_id":"seedance","connection_name":"Seedance","model":"seedance-2.5","protocol":"seedance","default":false}]}` {
		t.Fatalf("list output = %s", result.Content[0].Text)
	}
}
