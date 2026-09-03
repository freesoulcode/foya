// Package imagegen provides image-generation adapters used by creative canvases.
package imagegen

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	oai "github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
)

const maxGeneratedImageBytes int64 = 100 << 20

type OpenAIConfig struct {
	BaseURL string
	APIKey  string
}

type Request struct {
	Model       string
	Prompt      string
	AspectRatio string
	Quality     string
	References  [][]byte
}

type Result struct {
	Data      []byte
	MediaType string
}

type OpenAI struct {
	client oai.Client
	http   *http.Client
}

func NewOpenAI(config OpenAIConfig) *OpenAI {
	options := []option.RequestOption{}
	if strings.TrimSpace(config.BaseURL) != "" {
		options = append(options, option.WithBaseURL(strings.TrimRight(config.BaseURL, "/")+"/"))
	}
	if config.APIKey != "" {
		options = append(options, option.WithAPIKey(config.APIKey))
	}
	return &OpenAI{
		client: oai.NewClient(options...),
		http:   &http.Client{Timeout: 2 * time.Minute},
	}
}

func (adapter *OpenAI) Generate(ctx context.Context, request Request) (Result, error) {
	if strings.TrimSpace(request.Prompt) == "" {
		return Result{}, errors.New("image prompt is required")
	}
	if strings.TrimSpace(request.Model) == "" {
		return Result{}, errors.New("image model is required")
	}

	var (
		response *oai.ImagesResponse
		err      error
	)
	if len(request.References) == 0 {
		response, err = adapter.client.Images.Generate(ctx, oai.ImageGenerateParams{
			Model:   oai.ImageModel(request.Model),
			Prompt:  request.Prompt,
			N:       oai.Int(1),
			Quality: oai.ImageGenerateParamsQuality(request.Quality),
			Size:    oai.ImageGenerateParamsSize(imageSize(request.AspectRatio)),
		})
	} else {
		readers := make([]io.Reader, 0, len(request.References))
		for _, reference := range request.References {
			readers = append(readers, bytes.NewReader(reference))
		}
		response, err = adapter.client.Images.Edit(ctx, oai.ImageEditParams{
			Image:   oai.ImageEditParamsImageUnion{OfFileArray: readers},
			Model:   oai.ImageModel(request.Model),
			Prompt:  request.Prompt,
			N:       oai.Int(1),
			Quality: oai.ImageEditParamsQuality(request.Quality),
			Size:    oai.ImageEditParamsSize(imageSize(request.AspectRatio)),
		})
	}
	if err != nil {
		return Result{}, err
	}
	if response == nil || len(response.Data) == 0 {
		return Result{}, errors.New("image provider returned no result")
	}
	image := response.Data[0]
	if image.B64JSON != "" {
		data, decodeErr := base64.StdEncoding.DecodeString(image.B64JSON)
		if decodeErr != nil {
			return Result{}, fmt.Errorf("decode generated image: %w", decodeErr)
		}
		return Result{Data: data, MediaType: http.DetectContentType(data)}, nil
	}
	if image.URL == "" {
		return Result{}, errors.New("image provider returned an empty result")
	}
	return adapter.download(ctx, image.URL)
}

func (adapter *OpenAI) download(ctx context.Context, location string) (Result, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, location, nil)
	if err != nil {
		return Result{}, err
	}
	response, err := adapter.http.Do(req)
	if err != nil {
		return Result{}, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return Result{}, fmt.Errorf("download generated image: HTTP %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxGeneratedImageBytes+1))
	if err != nil {
		return Result{}, err
	}
	if int64(len(data)) > maxGeneratedImageBytes {
		return Result{}, errors.New("generated image exceeds size limit")
	}
	mediaType := strings.TrimSpace(strings.Split(response.Header.Get("Content-Type"), ";")[0])
	if mediaType == "" || mediaType == "application/octet-stream" {
		mediaType = http.DetectContentType(data)
	}
	if !strings.HasPrefix(mediaType, "image/") {
		return Result{}, fmt.Errorf("generated result is not an image: %s", mediaType)
	}
	return Result{Data: data, MediaType: mediaType}, nil
}

func imageSize(aspectRatio string) string {
	switch aspectRatio {
	case "16:9", "4:3", "3:2":
		return "1536x1024"
	case "9:16", "3:4", "2:3":
		return "1024x1536"
	default:
		return "1024x1024"
	}
}
