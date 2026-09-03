// Package browseruse coordinates browser actions between the agent and the
// desktop WebView.
package browseruse

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/freesoulcode/foya/internal/broker"
	"github.com/freesoulcode/foya/internal/event"
	"github.com/freesoulcode/foya/internal/state"
)

var ErrNotFound = errors.New("browser action not found")

type ActionRequest struct {
	ID            string `json:"id"`
	SessionID     string `json:"session_id"`
	ToolCallID    string `json:"tool_call_id,omitempty"`
	BrowserID     string `json:"browser_id"`
	Action        string `json:"action"`
	URL           string `json:"url,omitempty"`
	Ref           string `json:"ref,omitempty"`
	ObservationID string `json:"observation_id,omitempty"`
	Text          string `json:"text,omitempty"`
	Key           string `json:"key,omitempty"`
	Direction     string `json:"direction,omitempty"`
	Amount        int    `json:"amount,omitempty"`
	TimeoutMS     int    `json:"timeout_ms,omitempty"`
	Clear         bool   `json:"clear,omitempty"`
	FullPage      bool   `json:"full_page,omitempty"`
	CreatedAt     string `json:"created_at"`
}

type ActionResult struct {
	URL           string         `json:"url,omitempty"`
	Title         string         `json:"title,omitempty"`
	Revision      uint64         `json:"revision,omitempty"`
	ObservationID string         `json:"observation_id,omitempty"`
	Snapshot      string         `json:"snapshot,omitempty"`
	Screenshot    []byte         `json:"-"`
	MediaType     string         `json:"-"`
	Code          string         `json:"code,omitempty"`
	Message       string         `json:"message,omitempty"`
	PreURL        string         `json:"pre_url,omitempty"`
	PostURL       string         `json:"post_url,omitempty"`
	Verified      *bool          `json:"verified,omitempty"`
	ActualText    string         `json:"actual_text,omitempty"`
	Trace         map[string]any `json:"trace,omitempty"`
	Error         string         `json:"error,omitempty"`
}

type pendingAction struct {
	request ActionRequest
	result  chan ActionResult
}

type Controller struct {
	dataDir string
	bus     *broker.Broker[event.Event]
	log     *state.MemLog

	mu      sync.Mutex
	pending map[string]pendingAction
}

func NewController(
	dataDir string,
	bus *broker.Broker[event.Event],
	log *state.MemLog,
) (*Controller, error) {
	controller := &Controller{
		dataDir: dataDir,
		bus:     bus,
		log:     log,
		pending: make(map[string]pendingAction),
	}
	return controller, nil
}

func (c *Controller) Execute(
	ctx context.Context,
	request ActionRequest,
) (ActionResult, error) {
	if request.ID == "" {
		request.ID = randomID()
	}
	if request.BrowserID == "" {
		request.BrowserID = "agent-" + request.SessionID
	}
	if request.CreatedAt == "" {
		request.CreatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	return c.executeWebView(ctx, request)
}

func (c *Controller) executeWebView(
	ctx context.Context,
	request ActionRequest,
) (ActionResult, error) {
	waiter := pendingAction{request: request, result: make(chan ActionResult, 1)}
	c.mu.Lock()
	c.pending[request.ID] = waiter
	c.mu.Unlock()

	ev := event.Event{
		Kind:    event.KindBrowserActionRequested,
		Session: request.SessionID,
		Time:    time.Now(),
		Payload: request,
	}
	seq, _ := c.log.Append(ctx, ev)
	ev.Seq = seq
	if err := c.bus.PublishMustDeliver(ctx, "session:"+request.SessionID, ev); err != nil {
		c.takePending(request.ID)
		return ActionResult{}, err
	}

	select {
	case result := <-waiter.result:
		if result.Error != "" {
			return result, errors.New(result.Error)
		}
		return result, nil
	case <-ctx.Done():
		c.takePending(request.ID)
		c.publishResolved(context.WithoutCancel(ctx), request, "cancelled")
		return ActionResult{}, ctx.Err()
	}
}

func (c *Controller) Resolve(
	sessionID, requestID string,
	result ActionResult,
) error {
	pending, ok := c.takePending(requestID)
	if !ok || pending.request.SessionID != sessionID {
		return ErrNotFound
	}
	result.URL = normalizeBrowserURL(result.URL)
	result.Snapshot = normalizeSnapshot(result.Snapshot)
	pending.result <- result
	status := "done"
	if result.Error != "" {
		status = "error"
	}
	c.publishResolved(context.Background(), pending.request, status)
	return nil
}

func (c *Controller) ClearSession(sessionID string) {
	c.mu.Lock()
	var pending []pendingAction
	for id, item := range c.pending {
		if item.request.SessionID == sessionID {
			delete(c.pending, id)
			pending = append(pending, item)
		}
	}
	c.mu.Unlock()
	for _, item := range pending {
		item.result <- ActionResult{Error: "browser session closed"}
	}
}

func (c *Controller) Close() {}

func (c *Controller) takePending(id string) (pendingAction, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	item, ok := c.pending[id]
	if ok {
		delete(c.pending, id)
	}
	return item, ok
}

func (c *Controller) publishResolved(
	ctx context.Context,
	request ActionRequest,
	status string,
) {
	ev := event.Event{
		Kind:    event.KindBrowserActionResolved,
		Session: request.SessionID,
		Time:    time.Now(),
		Payload: map[string]string{"id": request.ID, "status": status},
	}
	seq, _ := c.log.Append(ctx, ev)
	ev.Seq = seq
	_ = c.bus.PublishMustDeliver(ctx, "session:"+request.SessionID, ev)
}

func normalizeBrowserURL(value string) string {
	value = strings.TrimSpace(value)
	if len(value) >= 2 && strings.HasPrefix(value, "`") &&
		strings.HasSuffix(value, "`") {
		return strings.TrimSpace(value[1 : len(value)-1])
	}
	return value
}

func normalizeSnapshot(raw string) string {
	var value any
	if json.Unmarshal([]byte(raw), &value) != nil {
		return raw
	}
	normalizeSnapshotValue(value, "")
	data, err := json.Marshal(value)
	if err != nil {
		return raw
	}
	return string(data)
}

func normalizeSnapshotValue(value any, key string) {
	switch item := value.(type) {
	case map[string]any:
		for childKey, child := range item {
			normalizeSnapshotValue(child, childKey)
		}
	case []any:
		for _, child := range item {
			normalizeSnapshotValue(child, key)
		}
	case string:
		if key == "url" || key == "href" {
			// JSON values are updated through their parent map below.
			return
		}
	}
	if parent, ok := value.(map[string]any); ok {
		for childKey, child := range parent {
			if text, ok := child.(string); ok &&
				(childKey == "url" || childKey == "href") {
				parent[childKey] = normalizeBrowserURL(text)
			}
		}
	}
}

func randomID() string {
	var raw [12]byte
	_, _ = rand.Read(raw[:])
	return hex.EncodeToString(raw[:])
}
