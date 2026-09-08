package kernel

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	canvas "github.com/freesoulcode/foya/internal/canvas"
	"github.com/freesoulcode/foya/internal/config"
	conversation "github.com/freesoulcode/foya/internal/conversation"
)

func (b *Service) publishCanvas(ctx context.Context, kind conversation.Kind, doc canvas.Document) {
	ev := conversation.Event{Seq: conversation.Seq(doc.Revision), Kind: kind, Session: doc.ID, Time: time.Now(), Payload: doc}
	_ = b.bus.PublishMustDeliver(ctx, "canvas:"+doc.ID, ev)
}

func (b *Service) CreateCanvas(ctx context.Context, input canvas.CreateInput) (canvas.Document, error) {
	if input.SessionID != "" {
		s, ok := b.sessions.Get(input.SessionID)
		if !ok {
			return canvas.Document{}, conversation.ErrNotFound
		}
		if input.ProjectID == "" {
			input.ProjectID = s.ProjectID
		}
	}
	store, err := b.canvasStore()
	if err != nil {
		return canvas.Document{}, err
	}
	doc, err := store.Create(input)
	if err == nil {
		b.publishCanvas(ctx, conversation.KindCanvasCreated, doc)
	}
	return doc, err
}

func (b *Service) ListCanvases(sessionID string) ([]canvas.Document, error) {
	store, err := b.canvasStore()
	if err != nil {
		return nil, err
	}
	return store.List(sessionID), nil
}

func (b *Service) Canvas(id string) (canvas.Document, error) {
	store, err := b.canvasStore()
	if err != nil {
		return canvas.Document{}, err
	}
	doc, ok := store.Get(id)
	if !ok {
		return canvas.Document{}, canvas.ErrNotFound
	}
	return doc, nil
}

func (b *Service) UpdateCanvas(ctx context.Context, id string, input canvas.UpdateInput) (canvas.Document, error) {
	store, err := b.canvasStore()
	if err != nil {
		return canvas.Document{}, err
	}
	doc, err := store.Update(id, input)
	if err == nil {
		b.publishCanvas(ctx, conversation.KindCanvasUpdated, doc)
	}
	return doc, err
}

func (b *Service) DeleteCanvas(ctx context.Context, id string) error {
	store, err := b.canvasStore()
	if err != nil {
		return err
	}
	doc, ok := store.Get(id)
	if !ok {
		return canvas.ErrNotFound
	}
	if err := store.Delete(id); err != nil {
		return err
	}
	doc.Revision++
	b.publishCanvas(ctx, conversation.KindCanvasDeleted, doc)
	return nil
}

func (b *Service) PutCanvasAsset(ctx context.Context, id, name, mediaType string, source io.Reader) (canvas.Asset, canvas.Document, error) {
	store, err := b.canvasStore()
	if err != nil {
		return canvas.Asset{}, canvas.Document{}, err
	}
	asset, err := store.PutAsset(ctx, id, name, mediaType, source)
	if err != nil {
		return canvas.Asset{}, canvas.Document{}, err
	}
	doc, _ := store.Get(id)
	b.publishCanvas(ctx, conversation.KindCanvasUpdated, doc)
	return asset, doc, nil
}

func (b *Service) ReadCanvasAsset(ctx context.Context, id, assetID string) ([]byte, canvas.Asset, error) {
	store, err := b.canvasStore()
	if err != nil {
		return nil, canvas.Asset{}, err
	}
	return store.ReadAsset(ctx, id, assetID)
}

func (b *Service) GenerateCanvasImage(ctx context.Context, id string, input canvas.GenerateImageInput) (canvas.Document, error) {
	store, err := b.canvasStore()
	if err != nil {
		return canvas.Document{}, err
	}
	doc, ok := store.Get(id)
	if !ok {
		return canvas.Document{}, canvas.ErrNotFound
	}
	if doc.Revision != input.ExpectedRevision {
		return doc, canvas.ErrRevisionConflict
	}

	configIndex, outputIndex := -1, -1
	for index := range doc.Nodes {
		switch doc.Nodes[index].ID {
		case input.ConfigNodeID:
			configIndex = index
		case input.OutputNodeID:
			outputIndex = index
		}
	}
	if configIndex < 0 || outputIndex < 0 {
		return doc, errors.New("generation config and output nodes are required")
	}
	configNode := doc.Nodes[configIndex]
	if configNode.Type != "generation" || configNode.Generation == nil || configNode.Generation.Mode != "image" {
		return doc, errors.New("node is not an image generation configuration")
	}
	if doc.Nodes[outputIndex].Type != "image" {
		return doc, errors.New("generation output node must be an image")
	}

	connectionID := input.ConnectionID
	if connectionID == "" {
		connectionID = configNode.Generation.ConnectionID
	}
	model := strings.TrimSpace(configNode.Generation.Model)
	if connectionID == "" || model == "" {
		if defaults, loadErr := b.DefaultModels(); loadErr == nil {
			if connectionID == "" {
				connectionID = defaults.Image.ConnectionID
			}
			if model == "" {
				model = defaults.Image.Model
			}
		}
	}
	b.mu.RLock()
	connection, exists := b.connections[connectionID]
	b.mu.RUnlock()
	if !exists {
		return b.failCanvasGeneration(ctx, store, doc, configIndex, outputIndex, fmt.Errorf("%w: %q", ErrConnectionNotFound, connectionID))
	}
	if connection.Type != config.ConnectionTypeImage {
		return b.failCanvasGeneration(ctx, store, doc, configIndex, outputIndex, errors.New("image generation requires an image connection"))
	}
	settings, configured := connection.ModelSettings[model]
	if configured && !settings.ImageGenerationSupported() {
		return b.failCanvasGeneration(
			ctx,
			store,
			doc,
			configIndex,
			outputIndex,
			fmt.Errorf("model %q does not support image generation", model),
		)
	}

	promptParts := make([]string, 0, 4)
	if value := strings.TrimSpace(configNode.Prompt); value != "" {
		promptParts = append(promptParts, value)
	}
	references := make([][]byte, 0, 4)
	for _, edge := range doc.Edges {
		if edge.ToNodeID != configNode.ID {
			continue
		}
		for _, node := range doc.Nodes {
			if node.ID != edge.FromNodeID {
				continue
			}
			if node.Type == "text" {
				if value := strings.TrimSpace(node.Text); value != "" {
					promptParts = append(promptParts, value)
				}
			}
			if node.Type == "image" && node.AssetID != "" {
				data, _, readErr := store.ReadAsset(ctx, doc.ID, node.AssetID)
				if readErr != nil {
					return b.failCanvasGeneration(ctx, store, doc, configIndex, outputIndex, readErr)
				}
				references = append(references, data)
			}
		}
	}
	prompt := strings.Join(promptParts, "\n\n")
	if prompt == "" {
		return b.failCanvasGeneration(ctx, store, doc, configIndex, outputIndex, errors.New("connect a prompt node or enter a prompt in the generation node"))
	}

	generationCtx, cancelGeneration := context.WithTimeout(ctx, canvasGenerationTimeout)
	defer cancelGeneration()
	result, generateErr := canvas.NewOpenAIImageGenerator(canvas.ImageGeneratorConfig{
		BaseURL: connection.BaseURL,
		APIKey:  connection.APIKey,
	}).Generate(generationCtx, canvas.ImageGenerationRequest{
		Model:       model,
		Prompt:      prompt,
		AspectRatio: configNode.Generation.AspectRatio,
		Quality:     configNode.Generation.Quality,
		References:  references,
	})
	if generateErr != nil {
		if errors.Is(generateErr, context.DeadlineExceeded) {
			generateErr = fmt.Errorf("Image generation timed out after %s", canvasGenerationTimeout)
		}
		return b.failCanvasGeneration(ctx, store, doc, configIndex, outputIndex, fmt.Errorf("%w: %v", ErrGenerationProvider, generateErr))
	}

	asset, err := store.PutAsset(ctx, doc.ID, "generated.png", result.MediaType, bytes.NewReader(result.Data))
	if err != nil {
		return b.failCanvasGeneration(ctx, store, doc, configIndex, outputIndex, err)
	}
	current, _ := store.Get(doc.ID)
	for index := range current.Nodes {
		switch current.Nodes[index].ID {
		case configNode.ID:
			current.Nodes[index].Status = "success"
			current.Nodes[index].Error = ""
		case input.OutputNodeID:
			current.Nodes[index].AssetID = asset.ID
			current.Nodes[index].Status = "success"
			current.Nodes[index].Error = ""
			if asset.Width > 0 && asset.Height > 0 {
				current.Nodes[index].Width = 360
				current.Nodes[index].Height = canvasMediaNodeHeaderHeight +
					360*float64(asset.Height)/float64(asset.Width)
			}
		}
	}
	updated, err := store.Update(current.ID, canvas.UpdateInput{
		ExpectedRevision: current.Revision,
		Nodes:            &current.Nodes,
	})
	if err == nil {
		b.publishCanvas(ctx, conversation.KindCanvasUpdated, updated)
	}
	return updated, err
}

func (b *Service) GenerateCanvasVideo(ctx context.Context, id string, input canvas.GenerateVideoInput) (canvas.Document, error) {
	store, err := b.canvasStore()
	if err != nil {
		return canvas.Document{}, err
	}
	doc, ok := store.Get(id)
	if !ok {
		return canvas.Document{}, canvas.ErrNotFound
	}
	if doc.Revision != input.ExpectedRevision {
		return doc, canvas.ErrRevisionConflict
	}

	configIndex, outputIndex := -1, -1
	for index := range doc.Nodes {
		switch doc.Nodes[index].ID {
		case input.ConfigNodeID:
			configIndex = index
		case input.OutputNodeID:
			outputIndex = index
		}
	}
	if configIndex < 0 || outputIndex < 0 {
		return doc, errors.New("generation config and output nodes are required")
	}
	configNode := doc.Nodes[configIndex]
	if configNode.Type != "generation" || configNode.Generation == nil || configNode.Generation.Mode != "video" {
		return doc, errors.New("node is not a video generation configuration")
	}
	if doc.Nodes[outputIndex].Type != "video" {
		return doc, errors.New("generation output node must be a video")
	}

	connectionID := input.ConnectionID
	if connectionID == "" {
		connectionID = configNode.Generation.ConnectionID
	}
	model := strings.TrimSpace(configNode.Generation.Model)
	if connectionID == "" || model == "" {
		if defaults, loadErr := b.DefaultModels(); loadErr == nil {
			if connectionID == "" {
				connectionID = defaults.Video.ConnectionID
			}
			if model == "" {
				model = defaults.Video.Model
			}
		}
	}
	b.mu.RLock()
	connection, exists := b.connections[connectionID]
	b.mu.RUnlock()
	if !exists {
		return b.failCanvasGeneration(ctx, store, doc, configIndex, outputIndex, fmt.Errorf("%w: %q", ErrConnectionNotFound, connectionID))
	}
	if connection.Type != config.ConnectionTypeVideo {
		return b.failCanvasGeneration(ctx, store, doc, configIndex, outputIndex, errors.New("video generation requires a video connection"))
	}
	settings, configured := connection.ModelSettings[model]
	if configured && !settings.VideoGenerationSupported() {
		return b.failCanvasGeneration(
			ctx,
			store,
			doc,
			configIndex,
			outputIndex,
			fmt.Errorf("model %q does not support video generation", model),
		)
	}

	promptParts := make([]string, 0, 4)
	if value := strings.TrimSpace(configNode.Prompt); value != "" {
		promptParts = append(promptParts, value)
	}
	var (
		reference          []byte
		referenceMediaType string
	)
	for _, edge := range doc.Edges {
		if edge.ToNodeID != configNode.ID {
			continue
		}
		for _, node := range doc.Nodes {
			if node.ID != edge.FromNodeID {
				continue
			}
			if node.Type == "text" {
				if value := strings.TrimSpace(node.Text); value != "" {
					promptParts = append(promptParts, value)
				}
			}
			if node.Type == "image" && node.AssetID != "" && len(reference) == 0 {
				data, asset, readErr := store.ReadAsset(ctx, doc.ID, node.AssetID)
				if readErr != nil {
					return b.failCanvasGeneration(ctx, store, doc, configIndex, outputIndex, readErr)
				}
				reference = data
				referenceMediaType = asset.MediaType
			}
		}
	}
	prompt := strings.Join(promptParts, "\n\n")
	if prompt == "" {
		return b.failCanvasGeneration(ctx, store, doc, configIndex, outputIndex, errors.New("connect a prompt node or enter a prompt in the generation node"))
	}

	generationCtx, cancelGeneration := context.WithTimeout(ctx, canvasVideoGenerationTimeout)
	defer cancelGeneration()
	result, generateErr := canvas.NewOpenAIVideoGenerator(canvas.VideoGeneratorConfig{
		BaseURL:  connection.BaseURL,
		APIKey:   connection.APIKey,
		Protocol: connection.VideoProtocol,
	}).Generate(generationCtx, canvas.VideoGenerationRequest{
		Model:              model,
		Prompt:             prompt,
		AspectRatio:        configNode.Generation.AspectRatio,
		Duration:           configNode.Generation.Duration,
		Reference:          reference,
		ReferenceMediaType: referenceMediaType,
	})
	if generateErr != nil {
		if errors.Is(generateErr, context.DeadlineExceeded) {
			generateErr = fmt.Errorf("Video generation timed out after %s", canvasVideoGenerationTimeout)
		}
		return b.failCanvasGeneration(ctx, store, doc, configIndex, outputIndex, fmt.Errorf("%w: %v", ErrGenerationProvider, generateErr))
	}

	asset, err := store.PutAsset(ctx, doc.ID, "generated.mp4", result.MediaType, bytes.NewReader(result.Data))
	if err != nil {
		return b.failCanvasGeneration(ctx, store, doc, configIndex, outputIndex, err)
	}
	current, _ := store.Get(doc.ID)
	for index := range current.Nodes {
		switch current.Nodes[index].ID {
		case configNode.ID:
			current.Nodes[index].Status = "success"
			current.Nodes[index].Error = ""
		case input.OutputNodeID:
			current.Nodes[index].AssetID = asset.ID
			current.Nodes[index].Status = "success"
			current.Nodes[index].Error = ""
		}
	}
	updated, err := store.Update(current.ID, canvas.UpdateInput{
		ExpectedRevision: current.Revision,
		Nodes:            &current.Nodes,
	})
	if err == nil {
		b.publishCanvas(ctx, conversation.KindCanvasUpdated, updated)
	}
	return updated, err
}

func (b *Service) failCanvasGeneration(
	ctx context.Context,
	store *canvas.Store,
	doc canvas.Document,
	configIndex, outputIndex int,
	cause error,
) (canvas.Document, error) {
	doc.Nodes[configIndex].Status = "error"
	doc.Nodes[configIndex].Error = cause.Error()
	doc.Nodes[outputIndex].Status = "error"
	doc.Nodes[outputIndex].Error = cause.Error()
	updated, updateErr := store.Update(doc.ID, canvas.UpdateInput{
		ExpectedRevision: doc.Revision,
		Nodes:            &doc.Nodes,
	})
	if updateErr == nil {
		b.publishCanvas(ctx, conversation.KindCanvasUpdated, updated)
		return updated, cause
	}
	return doc, cause
}

func (b *Service) SubscribeCanvas(ctx context.Context, id string) (<-chan conversation.Event, error) {
	if _, err := b.Canvas(id); err != nil {
		return nil, err
	}
	return b.bus.Subscribe(ctx, "canvas:"+id), nil
}
