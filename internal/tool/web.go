package tool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"github.com/freesoulcode/foya/internal/approval"
	"github.com/freesoulcode/foya/internal/provider"
	"github.com/freesoulcode/foya/internal/websearch"
	xhtml "golang.org/x/net/html"
)

const (
	webFetchMaxBytes = 5 * 1024 * 1024
	webFetchMaxText  = 200_000
	webAccessScope   = "internet"
)

type webSearchTool struct {
	manager *websearch.Manager
	gateway approval.Gateway
}

type webFetchTool struct {
	gateway approval.Gateway
	client  *http.Client
}

type webSearchParams struct {
	Query string `json:"query"`
	Limit int    `json:"limit,omitempty"`
}

type webFetchParams struct {
	URL string `json:"url"`
}

func NewWebSearchTool(manager *websearch.Manager, gateway approval.Gateway) Tool {
	return &webSearchTool{manager: manager, gateway: gateway}
}

func NewWebFetchTool(gateway approval.Gateway) Tool {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		addresses, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		if err != nil {
			return nil, err
		}
		for _, address := range addresses {
			if blockedAddress(address) {
				return nil, fmt.Errorf("web fetch target resolves to a private or reserved address")
			}
		}
		var lastErr error
		for _, address := range addresses {
			connection, dialErr := dialer.DialContext(ctx, network, net.JoinHostPort(address.String(), port))
			if dialErr == nil {
				return connection, nil
			}
			lastErr = dialErr
		}
		if lastErr == nil {
			lastErr = errors.New("web fetch target resolved to no addresses")
		}
		return nil, lastErr
	}
	client := &http.Client{
		Transport: transport,
		Timeout:   30 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return errors.New("web fetch exceeded 10 redirects")
			}
			return validateWebURL(req.URL)
		},
	}
	return &webFetchTool{gateway: gateway, client: client}
}

func (t *webSearchTool) Name() string       { return "web_search" }
func (t *webSearchTool) Exposure() Exposure { return ExposureDirect }
func (t *webSearchTool) Description() string {
	return "Search the live web and return bounded source rows."
}
func (t *webSearchTool) Spec() []byte {
	return []byte(`{
		"type":"object",
		"properties":{
			"query":{"type":"string","minLength":1,"maxLength":200},
			"limit":{"type":"integer","minimum":1,"maximum":10,"default":5}
		},
		"required":["query"],
		"additionalProperties":false
	}`)
}

func (t *webSearchTool) Run(ctx context.Context, call Call) (Result, error) {
	var params webSearchParams
	if err := json.Unmarshal(call.Input, &params); err != nil {
		return errResult("invalid arguments: " + err.Error()), nil
	}
	decision, err := t.gateway.Request(ctx, approval.Request{
		ToolName: t.Name(),
		Action:   "network",
		Detail:   "联网搜索: " + params.Query,
		Resource: params.Query,
		Scope:    webAccessScope,
	})
	if err != nil {
		return errResult("search approval interrupted: " + err.Error()), nil
	}
	if decision == approval.DecisionDenied {
		return errResult("user denied web search"), nil
	}
	runtime, _ := ModelRuntimeFromContext(ctx)
	var native provider.NativeWebSearcher
	if candidate, ok := runtime.Provider.(provider.NativeWebSearcher); ok {
		native = candidate
	}
	results, source, err := t.manager.Search(ctx, websearch.Request{
		Query: params.Query, Limit: params.Limit, Native: native, Model: runtime.Model,
	})
	if err != nil {
		return errResult("web search failed: " + err.Error()), nil
	}
	payload, _ := json.Marshal(struct {
		Provider string                  `json:"provider"`
		Query    string                  `json:"query"`
		Results  []provider.SearchResult `json:"results"`
	}{source, params.Query, results})
	return textResult(string(payload)), nil
}

func (t *webFetchTool) Name() string       { return "web_fetch" }
func (t *webFetchTool) Exposure() Exposure { return ExposureDirect }
func (t *webFetchTool) Description() string {
	return "Read the main textual content of a known HTTP or HTTPS URL."
}
func (t *webFetchTool) Spec() []byte {
	return []byte(`{
		"type":"object",
		"properties":{"url":{"type":"string","format":"uri","description":"HTTP or HTTPS URL"}},
		"required":["url"],
		"additionalProperties":false
	}`)
}

func (t *webFetchTool) Run(ctx context.Context, call Call) (Result, error) {
	var params webFetchParams
	if err := json.Unmarshal(call.Input, &params); err != nil {
		return errResult("invalid arguments: " + err.Error()), nil
	}
	location, err := url.Parse(params.URL)
	if err != nil || validateWebURL(location) != nil {
		return errResult("invalid or unsafe URL"), nil
	}
	decision, err := t.gateway.Request(ctx, approval.Request{
		ToolName: t.Name(),
		Action:   "network",
		Detail:   "读取网页: " + location.String(),
		Resource: location.String(),
		Scope:    webAccessScope,
	})
	if err != nil {
		return errResult("fetch approval interrupted: " + err.Error()), nil
	}
	if decision == approval.DecisionDenied {
		return errResult("user denied web fetch"), nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, location.String(), nil)
	if err != nil {
		return errResult(err.Error()), nil
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 Foya/0.1")
	resp, err := t.client.Do(req)
	if err != nil {
		return errResult("web fetch failed: " + err.Error()), nil
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return errResult(fmt.Sprintf("web fetch returned HTTP %d", resp.StatusCode)), nil
	}
	reader := io.LimitReader(resp.Body, webFetchMaxBytes+1)
	data, err := io.ReadAll(reader)
	if err != nil {
		return errResult("web fetch read failed: " + err.Error()), nil
	}
	if len(data) > webFetchMaxBytes {
		return errResult("web fetch response exceeds 5 MB"), nil
	}
	content := string(data)
	contentType := strings.ToLower(resp.Header.Get("content-type"))
	if contentType != "" &&
		!strings.Contains(contentType, "text/") &&
		!strings.Contains(contentType, "json") &&
		!strings.Contains(contentType, "xml") {
		return errResult("web fetch response is not textual content"), nil
	}
	if strings.Contains(contentType, "text/html") || strings.Contains(contentType, "application/xhtml+xml") {
		content, err = htmlToMarkdown(content, resp.Request.URL)
		if err != nil {
			return errResult("web page parse failed: " + err.Error()), nil
		}
	}
	if len(content) > webFetchMaxText {
		content = content[:webFetchMaxText] + "\n\n[content truncated]"
	}
	return textResult(content), nil
}

func validateWebURL(location *url.URL) error {
	if location == nil || (location.Scheme != "http" && location.Scheme != "https") {
		return errors.New("URL must use HTTP or HTTPS")
	}
	if location.Hostname() == "" || location.User != nil {
		return errors.New("URL host is invalid")
	}
	return nil
}

func blockedAddress(address netip.Addr) bool {
	return address.IsLoopback() || address.IsPrivate() || address.IsLinkLocalUnicast() ||
		address.IsLinkLocalMulticast() || address.IsMulticast() || address.IsUnspecified()
}

func htmlToMarkdown(source string, base *url.URL) (string, error) {
	root, err := xhtml.Parse(strings.NewReader(source))
	if err != nil {
		return "", err
	}
	var out strings.Builder
	var walk func(*xhtml.Node)
	walk = func(node *xhtml.Node) {
		if node.Type == xhtml.ElementNode && (node.Data == "script" || node.Data == "style" || node.Data == "noscript") {
			return
		}
		if node.Type == xhtml.TextNode {
			text := strings.Join(strings.Fields(node.Data), " ")
			if text != "" {
				out.WriteString(text)
				out.WriteByte(' ')
			}
		}
		if node.Type == xhtml.ElementNode && node.Data == "a" {
			for _, attr := range node.Attr {
				if attr.Key == "href" {
					if link, err := base.Parse(attr.Val); err == nil {
						out.WriteString("(" + link.String() + ") ")
					}
				}
			}
		}
		if node.Type == xhtml.ElementNode {
			switch node.Data {
			case "p", "div", "article", "section", "li", "h1", "h2", "h3", "h4", "br":
				out.WriteByte('\n')
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(root)
	lines := strings.Split(out.String(), "\n")
	cleaned := lines[:0]
	for _, line := range lines {
		line = strings.Join(strings.Fields(line), " ")
		if line != "" {
			cleaned = append(cleaned, line)
		}
	}
	return strings.Join(cleaned, "\n\n"), nil
}
