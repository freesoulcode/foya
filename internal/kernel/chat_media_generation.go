package kernel

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"mime"
	"path/filepath"
	"strings"

	canvas "github.com/freesoulcode/foya/internal/canvas"
	"github.com/freesoulcode/foya/internal/config"
	conversation "github.com/freesoulcode/foya/internal/conversation"
	"github.com/freesoulcode/foya/internal/tool"
)

func (b *Service) GenerateChatImage(
	ctx context.Context,
	sessionID string,
	connectionID string,
	request canvas.ImageGenerationRequest,
) (conversation.AttachmentRef, error) {
	if _, ok := b.sessions.Get(sessionID); !ok {
		return conversation.AttachmentRef{}, conversation.ErrNotFound
	}
	connection, model, err := b.chatGenerationTarget(
		ctx,
		config.ConnectionTypeImage,
		connectionID,
		request.Model,
	)
	if err != nil {
		return conversation.AttachmentRef{}, err
	}
	request.Model = model
	generationCtx, cancel := context.WithTimeout(ctx, canvasGenerationTimeout)
	defer cancel()
	result, err := canvas.NewOpenAIImageGenerator(canvas.ImageGeneratorConfig{
		BaseURL: connection.BaseURL,
		APIKey:  connection.APIKey,
	}).Generate(generationCtx, request)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			err = fmt.Errorf("image generation timed out after %s", canvasGenerationTimeout)
		}
		return conversation.AttachmentRef{}, fmt.Errorf("%w: %w", ErrGenerationProvider, err)
	}

	b.mu.RLock()
	store := b.artifacts
	b.mu.RUnlock()
	if store == nil {
		return conversation.AttachmentRef{}, errors.New("artifact store is unavailable")
	}
	return store.PutImage(
		ctx,
		sessionID,
		generatedMediaName("generated-image", result.MediaType),
		bytes.NewReader(result.Data),
	)
}

func (b *Service) GenerateChatVideo(
	ctx context.Context,
	sessionID string,
	connectionID string,
	request canvas.VideoGenerationRequest,
) (conversation.AttachmentRef, error) {
	if _, ok := b.sessions.Get(sessionID); !ok {
		return conversation.AttachmentRef{}, conversation.ErrNotFound
	}
	connection, model, err := b.chatGenerationTarget(
		ctx,
		config.ConnectionTypeVideo,
		connectionID,
		request.Model,
	)
	if err != nil {
		return conversation.AttachmentRef{}, err
	}
	request.Model = model
	generationCtx, cancel := context.WithTimeout(ctx, canvasVideoGenerationTimeout)
	defer cancel()
	result, err := canvas.NewOpenAIVideoGenerator(canvas.VideoGeneratorConfig{
		BaseURL:  connection.BaseURL,
		APIKey:   connection.APIKey,
		Protocol: connection.VideoProtocol,
	}).Generate(generationCtx, request)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			err = fmt.Errorf("video generation timed out after %s", canvasVideoGenerationTimeout)
		}
		return conversation.AttachmentRef{}, fmt.Errorf("%w: %w", ErrGenerationProvider, err)
	}

	b.mu.RLock()
	store := b.artifacts
	b.mu.RUnlock()
	if store == nil {
		return conversation.AttachmentRef{}, errors.New("artifact store is unavailable")
	}
	return store.PutVideo(
		ctx,
		sessionID,
		generatedMediaName("generated-video", result.MediaType),
		result.MediaType,
		bytes.NewReader(result.Data),
	)
}

func (b *Service) ListChatMediaModels(
	ctx context.Context,
	kind string,
) ([]tool.ChatMediaModel, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	kind = strings.TrimSpace(strings.ToLower(kind))
	if kind != "" && kind != config.ConnectionTypeImage && kind != config.ConnectionTypeVideo {
		return nil, fmt.Errorf("media type must be image or video")
	}
	defaults, err := b.DefaultModels()
	if err != nil {
		return nil, err
	}
	connections := b.Connections()
	models := make([]tool.ChatMediaModel, 0)
	for _, connection := range connections {
		if kind != "" && connection.Type != kind {
			continue
		}
		if connection.Type != config.ConnectionTypeImage &&
			connection.Type != config.ConnectionTypeVideo {
			continue
		}
		if connection.Type == config.ConnectionTypeVideo {
			if err := validateVideoProtocol(connection.Type, connection.VideoProtocol); err != nil {
				continue
			}
		}
		defaultRef := defaults.Image
		if connection.Type == config.ConnectionTypeVideo {
			defaultRef = defaults.Video
		}
		for _, modelID := range connection.Models {
			modelID = strings.TrimSpace(modelID)
			if modelID == "" {
				continue
			}
			settings, configured := connection.ModelSettings[modelID]
			supported := true
			if configured && connection.Type == config.ConnectionTypeImage {
				supported = settings.ImageGenerationSupported()
			}
			if configured && connection.Type == config.ConnectionTypeVideo {
				supported = settings.VideoGenerationSupported()
			}
			if !supported {
				continue
			}
			models = append(models, tool.ChatMediaModel{
				Type:           connection.Type,
				ConnectionID:   connection.ID,
				ConnectionName: connection.Name,
				Model:          modelID,
				Protocol:       connection.VideoProtocol,
				Default: defaultRef.ConnectionID == connection.ID &&
					defaultRef.Model == modelID,
			})
		}
	}
	return models, nil
}

func (b *Service) chatGenerationTarget(
	ctx context.Context,
	kind string,
	requestedConnectionID string,
	requestedModel string,
) (config.Connection, string, error) {
	models, err := b.ListChatMediaModels(ctx, kind)
	if err != nil {
		return config.Connection{}, "", err
	}
	capabilityName := "image"
	if kind == config.ConnectionTypeVideo {
		capabilityName = "video"
	}
	requestedConnectionID = strings.TrimSpace(requestedConnectionID)
	requestedModel = strings.TrimSpace(requestedModel)
	if requestedConnectionID != "" {
		connection, ok := b.Connection(requestedConnectionID)
		if !ok {
			return config.Connection{}, "", fmt.Errorf(
				"%w: %q",
				ErrConnectionNotFound,
				requestedConnectionID,
			)
		}
		if connection.Type != kind {
			return config.Connection{}, "", fmt.Errorf(
				"%s generation requires a %s connection",
				capabilityName,
				kind,
			)
		}
	}

	candidates := make([]tool.ChatMediaModel, 0, len(models))
	for _, model := range models {
		if requestedConnectionID != "" && model.ConnectionID != requestedConnectionID {
			continue
		}
		if requestedModel != "" && model.Model != requestedModel {
			continue
		}
		candidates = append(candidates, model)
	}
	if requestedModel != "" && len(candidates) == 0 {
		for _, model := range models {
			if requestedConnectionID != "" && model.ConnectionID != requestedConnectionID {
				continue
			}
			if strings.EqualFold(model.Model, requestedModel) {
				candidates = append(candidates, model)
			}
		}
	}

	var selected tool.ChatMediaModel
	switch {
	case requestedModel != "":
		if len(candidates) == 0 {
			return config.Connection{}, "", fmt.Errorf(
				"%s generation model %q is not available; available models: %s",
				capabilityName,
				requestedModel,
				chatMediaModelOptions(models),
			)
		}
		if len(candidates) > 1 {
			return config.Connection{}, "", fmt.Errorf(
				"%s generation model %q is configured by multiple connections; specify connection_id; available models: %s",
				capabilityName,
				requestedModel,
				chatMediaModelOptions(candidates),
			)
		}
		selected = candidates[0]
	case requestedConnectionID != "":
		for _, candidate := range candidates {
			if candidate.Default {
				selected = candidate
				break
			}
		}
		if selected.Model == "" && len(candidates) == 1 {
			selected = candidates[0]
		}
		if selected.Model == "" {
			return config.Connection{}, "", fmt.Errorf(
				"connection %q has no unambiguous %s generation model; specify model; available models: %s",
				requestedConnectionID,
				capabilityName,
				chatMediaModelOptions(candidates),
			)
		}
	default:
		for _, candidate := range models {
			if candidate.Default {
				selected = candidate
				break
			}
		}
		if selected.Model == "" && len(models) == 1 {
			selected = models[0]
		}
		if selected.Model == "" {
			return config.Connection{}, "", fmt.Errorf(
				"default %s generation model is not configured; specify model and connection_id; available models: %s",
				capabilityName,
				chatMediaModelOptions(models),
			)
		}
	}

	connection, ok := b.Connection(selected.ConnectionID)
	if !ok {
		return config.Connection{}, "", fmt.Errorf("%w: %q", ErrConnectionNotFound, selected.ConnectionID)
	}
	return connection, selected.Model, nil
}

func chatMediaModelOptions(models []tool.ChatMediaModel) string {
	if len(models) == 0 {
		return "none"
	}
	options := make([]string, 0, len(models))
	for _, model := range models {
		options = append(options, fmt.Sprintf("%q (%s)", model.Model, model.ConnectionID))
	}
	return strings.Join(options, ", ")
}

func generatedMediaName(base, mediaType string) string {
	mediaType = strings.TrimSpace(strings.Split(mediaType, ";")[0])
	switch mediaType {
	case "image/png":
		return base + ".png"
	case "image/jpeg":
		return base + ".jpg"
	case "image/webp":
		return base + ".webp"
	case "image/gif":
		return base + ".gif"
	case "video/mp4":
		return base + ".mp4"
	case "video/webm":
		return base + ".webm"
	case "video/quicktime":
		return base + ".mov"
	}
	if extensions, err := mime.ExtensionsByType(mediaType); err == nil && len(extensions) > 0 {
		return base + extensions[0]
	}
	if strings.HasPrefix(mediaType, "image/") {
		return base + ".png"
	}
	if strings.HasPrefix(mediaType, "video/") {
		return base + ".mp4"
	}
	return filepath.Base(base)
}
