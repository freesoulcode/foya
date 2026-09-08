package canvas

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const maxGeneratedVideoBytes int64 = 500 << 20

type VideoGeneratorConfig struct {
	BaseURL  string
	APIKey   string
	Protocol string
}

const (
	ProtocolSeedance  = "seedance"
	ProtocolMiniMaxH3 = "minimax_h3"
)

type VideoGenerationRequest struct {
	Model              string
	Prompt             string
	AspectRatio        string
	Duration           int
	Reference          []byte
	ReferenceMediaType string
}

type VideoGenerationResult struct {
	Data      []byte
	MediaType string
}

type videoJob struct {
	ID           string `json:"id"`
	TaskID       string `json:"task_id"`
	Status       string `json:"status"`
	URL          string `json:"url"`
	ResultURL    string `json:"result_url"`
	VideoURL     string `json:"video_url"`
	ErrorMessage string `json:"error_message"`
	FailReason   string `json:"fail_reason"`
	Error        *struct {
		Message string `json:"message"`
	} `json:"error"`
	Data   *videoJob `json:"data"`
	Result struct {
		URL        string   `json:"url"`
		PrimaryURL string   `json:"primary_url"`
		URLs       []string `json:"urls"`
	} `json:"result"`
	Content struct {
		VideoURL string `json:"video_url"`
		URL      string `json:"url"`
	} `json:"content"`
	Task *videoJob `json:"task"`
}

type OpenAIVideoGenerator struct {
	baseURL      string
	apiKey       string
	protocol     string
	http         *http.Client
	pollInterval time.Duration
}

func NewOpenAIVideoGenerator(config VideoGeneratorConfig) *OpenAIVideoGenerator {
	baseURL := strings.TrimRight(strings.TrimSpace(config.BaseURL), "/")
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	return &OpenAIVideoGenerator{
		baseURL:      baseURL,
		apiKey:       config.APIKey,
		protocol:     config.Protocol,
		http:         &http.Client{},
		pollInterval: 2 * time.Second,
	}
}

func (adapter *OpenAIVideoGenerator) Generate(ctx context.Context, request VideoGenerationRequest) (VideoGenerationResult, error) {
	if strings.TrimSpace(request.Prompt) == "" {
		return VideoGenerationResult{}, errors.New("video prompt is required")
	}
	if strings.TrimSpace(request.Model) == "" {
		return VideoGenerationResult{}, errors.New("video model is required")
	}
	if adapter.protocol != ProtocolSeedance && adapter.protocol != ProtocolMiniMaxH3 {
		return VideoGenerationResult{}, fmt.Errorf("unsupported video protocol %q", adapter.protocol)
	}

	endpoint := adapter.createURL()
	job, err := adapter.create(ctx, endpoint, request)
	if err != nil {
		return VideoGenerationResult{}, err
	}
	for {
		switch strings.ToLower(job.Status) {
		case "completed", "succeeded", "success":
			if location := job.resultURL(); location != "" {
				location, authenticate, err := adapter.resolveDownloadURL(location)
				if err != nil {
					return VideoGenerationResult{}, err
				}
				return adapter.download(ctx, location, authenticate)
			}
			if job.ID == "" {
				return VideoGenerationResult{}, errors.New("video provider returned no job id")
			}
			return VideoGenerationResult{}, errors.New("video provider completed without a result URL")
		case "failed", "cancelled", "canceled", "expired":
			message := "video generation failed"
			if job.Error != nil && strings.TrimSpace(job.Error.Message) != "" {
				message += ": " + job.Error.Message
			} else if strings.TrimSpace(job.ErrorMessage) != "" {
				message += ": " + job.ErrorMessage
			} else if strings.TrimSpace(job.FailReason) != "" {
				message += ": " + job.FailReason
			}
			return VideoGenerationResult{}, errors.New(message)
		case "queued", "in_progress", "processing", "pending", "":
		default:
			return VideoGenerationResult{}, fmt.Errorf("video provider returned unknown status %q", job.Status)
		}

		if job.ID == "" {
			return VideoGenerationResult{}, errors.New("video provider returned no job id")
		}
		timer := time.NewTimer(adapter.pollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return VideoGenerationResult{}, ctx.Err()
		case <-timer.C:
		}
		job, err = adapter.retrieve(ctx, endpoint, job.ID)
		if err != nil {
			return VideoGenerationResult{}, err
		}
	}
}

func (adapter *OpenAIVideoGenerator) create(ctx context.Context, endpoint string, request VideoGenerationRequest) (videoJob, error) {
	ratio := strings.TrimSpace(request.AspectRatio)
	if ratio == "" {
		ratio = "16:9"
	}
	content := []map[string]any{{"type": "text", "text": request.Prompt}}
	var imageURL string
	if len(request.Reference) > 0 {
		mediaType := strings.TrimSpace(request.ReferenceMediaType)
		if mediaType == "" {
			mediaType = http.DetectContentType(request.Reference)
		}
		imageURL = "data:" + mediaType + ";base64," + base64.StdEncoding.EncodeToString(request.Reference)
		content = append(content, map[string]any{
			"type":      "image_url",
			"image_url": map[string]string{"url": imageURL},
			"role":      "reference_image",
		})
	}
	var payload map[string]any
	if adapter.protocol == ProtocolSeedance {
		payload = map[string]any{
			"model":          request.Model,
			"content":        content,
			"ratio":          ratio,
			"duration":       videoDuration(request.Duration),
			"resolution":     "720p",
			"generate_audio": true,
			"watermark":      false,
		}
	} else {
		payload = map[string]any{
			"model":      request.Model,
			"content":    content,
			"ratio":      ratio,
			"duration":   videoDuration(request.Duration),
			"resolution": "768P",
		}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return videoJob{}, err
	}

	req, err := adapter.newRequest(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return videoJob{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	return adapter.doJob(req)
}

func (adapter *OpenAIVideoGenerator) retrieve(ctx context.Context, endpoint, id string) (videoJob, error) {
	req, err := adapter.newRequest(ctx, http.MethodGet, adapter.taskURL(endpoint, id), nil)
	if err != nil {
		return videoJob{}, err
	}
	return adapter.doJob(req)
}

func (adapter *OpenAIVideoGenerator) doJob(req *http.Request) (videoJob, error) {
	response, err := adapter.http.Do(req)
	if err != nil {
		return videoJob{}, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return videoJob{}, providerError(response)
	}
	var job videoJob
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&job); err != nil {
		return videoJob{}, fmt.Errorf("decode video provider response: %w", err)
	}
	if job.ID == "" && job.TaskID == "" && job.Data != nil {
		job = *job.Data
	}
	if job.ID == "" && job.TaskID == "" && job.Task != nil {
		job = *job.Task
	}
	if job.ID == "" {
		job.ID = job.TaskID
	}
	return job, nil
}

func (adapter *OpenAIVideoGenerator) download(ctx context.Context, location string, authenticate bool) (VideoGenerationResult, error) {
	req, err := adapter.newRequest(ctx, http.MethodGet, location, nil)
	if err != nil {
		return VideoGenerationResult{}, err
	}
	if !authenticate {
		req.Header.Del("Authorization")
	}
	response, err := adapter.http.Do(req)
	if err != nil {
		return VideoGenerationResult{}, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return VideoGenerationResult{}, providerError(response)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxGeneratedVideoBytes+1))
	if err != nil {
		return VideoGenerationResult{}, err
	}
	if int64(len(data)) > maxGeneratedVideoBytes {
		return VideoGenerationResult{}, errors.New("generated video exceeds size limit")
	}
	mediaType := strings.TrimSpace(strings.Split(response.Header.Get("Content-Type"), ";")[0])
	if mediaType == "" || mediaType == "application/octet-stream" {
		mediaType = http.DetectContentType(data)
	}
	if !strings.HasPrefix(mediaType, "video/") {
		return VideoGenerationResult{}, fmt.Errorf("generated result is not a video: %s", mediaType)
	}
	return VideoGenerationResult{Data: data, MediaType: mediaType}, nil
}

func (adapter *OpenAIVideoGenerator) newRequest(ctx context.Context, method, location string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, location, body)
	if err != nil {
		return nil, err
	}
	if adapter.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+adapter.apiKey)
	}
	return req, nil
}

func (adapter *OpenAIVideoGenerator) createURL() string {
	if adapter.protocol == ProtocolMiniMaxH3 {
		return adapter.versionedBaseURL("v2") + "/video_generation"
	}
	return adapter.seedanceBaseURL() + "/contents/generations/tasks"
}

func (adapter *OpenAIVideoGenerator) taskURL(endpoint, id string) string {
	if adapter.protocol == ProtocolMiniMaxH3 {
		return adapter.versionedBaseURL("v2") + "/query/video_generation/" + url.PathEscape(id)
	}
	return endpoint + "/" + url.PathEscape(id)
}

func (adapter *OpenAIVideoGenerator) versionedBaseURL(version string) string {
	base := adapter.baseURL
	for _, suffix := range []string{"/v1", "/v2"} {
		base = strings.TrimSuffix(base, suffix)
	}
	return base + "/" + version
}

func (adapter *OpenAIVideoGenerator) seedanceBaseURL() string {
	base := strings.TrimRight(adapter.baseURL, "/")
	if strings.HasSuffix(base, "/api/v3") {
		return base
	}
	base = strings.TrimSuffix(base, "/v1")
	return base + "/api/v3"
}

func (adapter *OpenAIVideoGenerator) resolveDownloadURL(location string) (string, bool, error) {
	base, err := url.Parse(adapter.baseURL + "/")
	if err != nil {
		return "", false, err
	}
	target, err := url.Parse(location)
	if err != nil {
		return "", false, err
	}
	target = base.ResolveReference(target)
	return target.String(), target.Scheme == base.Scheme && target.Host == base.Host, nil
}

func providerError(response *http.Response) error {
	data, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	var payload struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
		Message string `json:"message"`
	}
	_ = json.Unmarshal(data, &payload)
	message := strings.TrimSpace(payload.Error.Message)
	if message == "" {
		message = strings.TrimSpace(payload.Message)
	}
	if message == "" {
		message = strings.TrimSpace(string(data))
	}
	if message == "" {
		message = http.StatusText(response.StatusCode)
	}
	return fmt.Errorf("video provider: HTTP %d: %s", response.StatusCode, message)
}

func (job videoJob) resultURL() string {
	for _, location := range []string{
		job.URL,
		job.ResultURL,
		job.VideoURL,
		job.Content.URL,
		job.Result.PrimaryURL,
		job.Result.URL,
		job.Content.VideoURL,
	} {
		if strings.TrimSpace(location) != "" {
			return location
		}
	}
	if len(job.Result.URLs) > 0 {
		return job.Result.URLs[0]
	}
	return ""
}

func videoSize(aspectRatio string) string {
	switch aspectRatio {
	case "16:9", "4:3", "3:2":
		return "1280x720"
	default:
		return "720x1280"
	}
}

func videoDuration(duration int) int {
	if duration >= 4 && duration <= 15 {
		return duration
	}
	return 5
}
