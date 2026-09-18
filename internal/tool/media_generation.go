package tool

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	canvas "github.com/freesoulcode/foya/internal/canvas"
	conversation "github.com/freesoulcode/foya/internal/conversation"
)

// ChatMediaGenerator resolves configured generation models and stores their
// output as session-owned artifacts.
type ChatMediaGenerator interface {
	ListChatMediaModels(context.Context, string) ([]ChatMediaModel, error)
	GenerateChatImage(
		context.Context,
		string,
		string,
		canvas.ImageGenerationRequest,
	) (conversation.AttachmentRef, error)
	GenerateChatVideo(
		context.Context,
		string,
		string,
		canvas.VideoGenerationRequest,
	) (conversation.AttachmentRef, error)
}

type ChatMediaModel struct {
	Type           string `json:"type"`
	ConnectionID   string `json:"connection_id"`
	ConnectionName string `json:"connection_name"`
	Model          string `json:"model"`
	Protocol       string `json:"protocol,omitempty"`
	Default        bool   `json:"default"`
}

type mediaGenerationTool struct {
	kind      string
	generator ChatMediaGenerator
}

type listMediaModelsTool struct {
	generator ChatMediaGenerator
}

type imageGenerationParams struct {
	ConnectionID string `json:"connection_id,omitempty"`
	Model        string `json:"model,omitempty"`
	Prompt       string `json:"prompt"`
	AspectRatio  string `json:"aspect_ratio,omitempty"`
	Quality      string `json:"quality,omitempty"`
}

type videoGenerationParams struct {
	ConnectionID string `json:"connection_id,omitempty"`
	Model        string `json:"model,omitempty"`
	Prompt       string `json:"prompt"`
	AspectRatio  string `json:"aspect_ratio,omitempty"`
	Duration     int    `json:"duration,omitempty"`
}

// MediaGenerationTools exposes configured image and video generation to the
// ordinary agent loop.
func MediaGenerationTools(generator ChatMediaGenerator) []Tool {
	return []Tool{
		&listMediaModelsTool{generator: generator},
		&mediaGenerationTool{kind: "image", generator: generator},
		&mediaGenerationTool{kind: "video", generator: generator},
	}
}

func (t *mediaGenerationTool) Name() string       { return "generate_" + t.kind }
func (t *mediaGenerationTool) Exposure() Exposure { return ExposureDirect }
func (t *mediaGenerationTool) Description() string {
	if t.kind == "video" {
		return "Generate a video from a text prompt. Pass the exact model and connection_id when the user names a model; call list_media_models first when the configured value is unknown. Without a model, the default or sole compatible video model is used."
	}
	return "Generate an image from a text prompt. Pass the exact model and connection_id when the user names a model; call list_media_models first when the configured value is unknown. Without a model, the default or sole compatible image model is used."
}

func (t *mediaGenerationTool) Spec() []byte {
	if t.kind == "video" {
		return []byte(`{
			"type":"object",
			"properties":{
				"connection_id":{"type":"string","description":"Configured connection ID from list_media_models. Required when the same model exists in multiple connections."},
				"model":{"type":"string","description":"Exact configured video model ID. Omit to use the default or sole compatible model."},
				"prompt":{"type":"string","minLength":1,"description":"A complete, concrete description of the video to generate."},
				"aspect_ratio":{"type":"string","enum":["16:9","9:16","1:1","4:3","3:4"],"default":"16:9"},
				"duration":{"type":"integer","minimum":4,"maximum":15,"default":5}
			},
			"required":["prompt"],
			"additionalProperties":false
		}`)
	}
	return []byte(`{
		"type":"object",
		"properties":{
			"connection_id":{"type":"string","description":"Configured connection ID from list_media_models. Required when the same model exists in multiple connections."},
			"model":{"type":"string","description":"Exact configured image model ID. Omit to use the default or sole compatible model."},
			"prompt":{"type":"string","minLength":1,"description":"A complete, concrete description of the image to generate."},
			"aspect_ratio":{"type":"string","enum":["1:1","16:9","9:16","4:3","3:4","3:2","2:3"],"default":"1:1"},
			"quality":{"type":"string","enum":["auto","low","medium","high"],"default":"auto"}
		},
		"required":["prompt"],
		"additionalProperties":false
	}`)
}

func (t *mediaGenerationTool) Run(ctx context.Context, call Call) (Result, error) {
	if t.generator == nil {
		return errResult("media generation is unavailable"), nil
	}
	sessionID := SessionIDFromContext(ctx)
	if sessionID == "" {
		return errResult("media generation requires a chat session"), nil
	}
	if t.kind == "video" {
		return t.runVideo(ctx, sessionID, call)
	}
	return t.runImage(ctx, sessionID, call)
}

func (t *mediaGenerationTool) runImage(
	ctx context.Context,
	sessionID string,
	call Call,
) (Result, error) {
	var params imageGenerationParams
	if err := json.Unmarshal(call.Input, &params); err != nil {
		return errResult("invalid arguments: " + err.Error()), nil
	}
	if strings.TrimSpace(params.Prompt) == "" {
		return errResult("image prompt is required"), nil
	}
	if params.AspectRatio == "" {
		params.AspectRatio = "1:1"
	}
	if params.Quality == "" {
		params.Quality = "auto"
	}
	ref, err := t.generator.GenerateChatImage(ctx, sessionID, params.ConnectionID, canvas.ImageGenerationRequest{
		Model:       params.Model,
		Prompt:      params.Prompt,
		AspectRatio: params.AspectRatio,
		Quality:     params.Quality,
	})
	if err != nil {
		return mediaGenerationError("image", err), nil
	}
	return generatedMediaResult("image", ref), nil
}

func (t *mediaGenerationTool) runVideo(
	ctx context.Context,
	sessionID string,
	call Call,
) (Result, error) {
	var params videoGenerationParams
	if err := json.Unmarshal(call.Input, &params); err != nil {
		return errResult("invalid arguments: " + err.Error()), nil
	}
	if strings.TrimSpace(params.Prompt) == "" {
		return errResult("video prompt is required"), nil
	}
	if params.AspectRatio == "" {
		params.AspectRatio = "16:9"
	}
	if params.Duration == 0 {
		params.Duration = 5
	}
	ref, err := t.generator.GenerateChatVideo(ctx, sessionID, params.ConnectionID, canvas.VideoGenerationRequest{
		Model:       params.Model,
		Prompt:      params.Prompt,
		AspectRatio: params.AspectRatio,
		Duration:    params.Duration,
	})
	if err != nil {
		return mediaGenerationError("video", err), nil
	}
	return generatedMediaResult("video", ref), nil
}

func mediaGenerationError(kind string, err error) Result {
	if errors.Is(err, context.Canceled) {
		return errResult(kind + " generation was cancelled")
	}
	return errResult(kind + " generation failed: " + err.Error())
}

func generatedMediaResult(kind string, ref conversation.AttachmentRef) Result {
	return Result{Content: []ContentPart{
		{Type: "text", Text: "Generated " + kind + ": " + ref.Name},
		{Type: "artifact_ref", Attachment: &ref},
	}}
}

func (t *listMediaModelsTool) Name() string       { return "list_media_models" }
func (t *listMediaModelsTool) Exposure() Exposure { return ExposureDirect }
func (t *listMediaModelsTool) Description() string {
	return "List configured image and video generation models, connection IDs, protocols, and defaults. Use this before media generation when the user names a model or no default model is known."
}

func (t *listMediaModelsTool) Spec() []byte {
	return []byte(`{
		"type":"object",
		"properties":{
			"type":{"type":"string","enum":["image","video"],"description":"Optional media type filter."}
		},
		"additionalProperties":false
	}`)
}

func (t *listMediaModelsTool) Run(ctx context.Context, call Call) (Result, error) {
	if t.generator == nil {
		return errResult("media generation is unavailable"), nil
	}
	var params struct {
		Type string `json:"type,omitempty"`
	}
	if err := json.Unmarshal(call.Input, &params); err != nil {
		return errResult("invalid arguments: " + err.Error()), nil
	}
	if params.Type != "" && params.Type != "image" && params.Type != "video" {
		return errResult("media type must be image or video"), nil
	}
	models, err := t.generator.ListChatMediaModels(ctx, params.Type)
	if err != nil {
		return errResult("list media models failed: " + err.Error()), nil
	}
	payload, err := json.Marshal(struct {
		Models []ChatMediaModel `json:"models"`
	}{Models: models})
	if err != nil {
		return Result{}, err
	}
	return Result{Content: []ContentPart{{Type: "text", Text: string(payload)}}}, nil
}
