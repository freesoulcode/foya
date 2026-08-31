package tool

import (
	"context"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"testing"

	"github.com/freesoulcode/foya/internal/approval"
)

type allowGateway struct{}

func (allowGateway) Request(context.Context, approval.Request) (approval.Decision, error) {
	return approval.DecisionAutoApprove, nil
}

func (allowGateway) Resolve(string, approval.Decision) error { return nil }

func (allowGateway) ClearSession(string) {}

type recordingGateway struct {
	request  approval.Request
	decision approval.Decision
}

func (g *recordingGateway) Request(_ context.Context, request approval.Request) (approval.Decision, error) {
	g.request = request
	return g.decision, nil
}

func (*recordingGateway) Resolve(string, approval.Decision) error { return nil }

func (*recordingGateway) ClearSession(string) {}

type staticTransport struct {
	contentType string
	body        string
}

func (t staticTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{t.contentType}},
		Body:       io.NopCloser(strings.NewReader(t.body)),
		Request:    request,
	}, nil
}

func TestWebFetchUsesStableSessionGrantScope(t *testing.T) {
	gateway := &recordingGateway{decision: approval.DecisionApproved}
	instance := NewWebFetchTool(gateway).(*webFetchTool)
	instance.client = &http.Client{Transport: staticTransport{
		contentType: "text/plain",
		body:        "ok",
	}}
	result, err := instance.Run(context.Background(), Call{
		Input: []byte(`{"url":"https://example.com/page"}`),
	})
	if err != nil || result.IsError {
		t.Fatalf("result = %#v, err = %v", result, err)
	}
	if gateway.request.Action != "network" ||
		gateway.request.Resource != "https://example.com/page" ||
		gateway.request.Scope != webAccessScope {
		t.Fatalf("approval request = %#v", gateway.request)
	}
}

func TestWebSearchUsesStableSessionGrantScope(t *testing.T) {
	gateway := &recordingGateway{decision: approval.DecisionDenied}
	instance := NewWebSearchTool(nil, gateway)
	_, err := instance.Run(context.Background(), Call{
		Input: []byte(`{"query":"Go release notes"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if gateway.request.Action != "network" ||
		gateway.request.Resource != "Go release notes" ||
		gateway.request.Scope != webAccessScope {
		t.Fatalf("approval request = %#v", gateway.request)
	}
}

func TestValidateWebURL(t *testing.T) {
	for _, raw := range []string{
		"file:///etc/passwd",
		"https://user:password@example.com",
		"https:///missing-host",
	} {
		location, _ := url.Parse(raw)
		if err := validateWebURL(location); err == nil {
			t.Fatalf("expected %q to be rejected", raw)
		}
	}
	location, _ := url.Parse("https://example.com/page")
	if err := validateWebURL(location); err != nil {
		t.Fatalf("expected public HTTPS URL to pass: %v", err)
	}
}

func TestBlockedAddress(t *testing.T) {
	for _, raw := range []string{"127.0.0.1", "10.0.0.1", "169.254.169.254", "::1", "fc00::1"} {
		if !blockedAddress(netip.MustParseAddr(raw)) {
			t.Fatalf("expected %s to be blocked", raw)
		}
	}
	if blockedAddress(netip.MustParseAddr("93.184.216.34")) {
		t.Fatal("public address was blocked")
	}
}

func TestWebFetchConvertsHTMLAndResolvesLinks(t *testing.T) {
	instance := NewWebFetchTool(allowGateway{}).(*webFetchTool)
	instance.client = &http.Client{Transport: staticTransport{
		contentType: "text/html; charset=utf-8",
		body: `<html><body><script>ignore()</script><main>
			<h1>Title</h1><p>Read <a href="/docs">the docs</a>.</p>
		</main></body></html>`,
	}}
	result, err := instance.Run(context.Background(), Call{
		Input: []byte(`{"url":"https://example.com/start"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	text := result.Content[0].Text
	if result.IsError || strings.Contains(text, "ignore") ||
		!strings.Contains(text, "Title") || !strings.Contains(text, "https://example.com/docs") {
		t.Fatalf("unexpected fetched content: %#v", result)
	}
}

func TestWebFetchRejectsBinaryContent(t *testing.T) {
	instance := NewWebFetchTool(allowGateway{}).(*webFetchTool)
	instance.client = &http.Client{Transport: staticTransport{
		contentType: "image/png",
		body:        "not really a png",
	}}
	result, err := instance.Run(context.Background(), Call{
		Input: []byte(`{"url":"https://example.com/image.png"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError || !strings.Contains(result.Content[0].Text, "not textual") {
		t.Fatalf("expected binary response rejection, got %#v", result)
	}
}
