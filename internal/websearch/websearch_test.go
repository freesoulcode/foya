package websearch

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	model "github.com/freesoulcode/foya/internal/model"
	xhtml "golang.org/x/net/html"
)

type nativeSearcher struct {
	results []model.SearchResult
	err     error
}

func (s nativeSearcher) SearchWeb(context.Context, string, string, int) ([]model.SearchResult, error) {
	return s.results, s.err
}

func TestSearchPrefersNativeProvider(t *testing.T) {
	manager, err := NewManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	results, source, err := manager.Search(context.Background(), Request{
		Query: "foya",
		Native: nativeSearcher{results: []model.SearchResult{{
			Title: "Foya", URL: "https://example.com/foya",
		}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if source != "model" || len(results) != 1 || results[0].Rank != 1 {
		t.Fatalf("unexpected native search result: source=%q results=%#v", source, results)
	}
}

func TestSearchFallsBackWhenNativeIsUnsupported(t *testing.T) {
	manager, err := NewManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	manager.client.Transport = roundTripFunc(func() string {
		return `<html><body><table>
			<tr><td><a class="result-link" href="https://example.com/result">Fallback result</a></td></tr>
			<tr><td class="result-snippet">Fallback snippet</td></tr>
		</table></body></html>`
	})

	results, source, err := manager.Search(context.Background(), Request{
		Query:  "foya",
		Native: nativeSearcher{err: model.ErrNativeSearchUnsupported},
	})
	if err != nil {
		t.Fatal(err)
	}
	if source != "duckduckgo" || len(results) != 1 || results[0].Title != "Fallback result" {
		t.Fatalf("unexpected fallback result: source=%q results=%#v", source, results)
	}
}

func TestParseDuckDuckGoAndUnwrapRedirect(t *testing.T) {
	root, err := xhtml.Parse(strings.NewReader(`<html><body><table>
		<tr><td><a class="result-link" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fexample.com%2Fa">First</a></td></tr>
		<tr><td class="result-snippet">One result</td></tr>
		<tr><td><a class="result-link" href="https://example.org/b">Second</a></td></tr>
		<tr><td class="result-snippet">Two result</td></tr>
	</table></body></html>`))
	if err != nil {
		t.Fatal(err)
	}
	results := parseDuckDuckGo(root, 10)
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}
	if results[0].URL != "https://example.com/a" || results[0].Snippet != "One result" {
		t.Fatalf("unexpected first result: %#v", results[0])
	}
	if results[1].Rank != 2 || results[1].Source != "example.org" {
		t.Fatalf("unexpected second result: %#v", results[1])
	}
}

func TestBingProviderParsesResults(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func() string {
		return `{"webPages":{"value":[{"name":"Foya","url":"https://example.com/foya","snippet":"Result"}]}}`
	})}
	searcher := &bingProvider{
		client: client,
		config: ProviderConfig{ID: "bing", APIKey: "secret"},
	}
	results, err := searcher.Search(context.Background(), "foya", 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Title != "Foya" ||
		results[0].URL != "https://example.com/foya" {
		t.Fatalf("unexpected Bing results: %#v", results)
	}
}

func TestParseBaidu(t *testing.T) {
	root, err := xhtml.Parse(strings.NewReader(`<html><body>
		<div class="result"><h3><a href="https://example.com/a">First</a></h3><div>First snippet</div></div>
		<div class="result"><h3><a href="https://example.org/b">Second</a></h3><div>Second snippet</div></div>
	</body></html>`))
	if err != nil {
		t.Fatal(err)
	}
	results := parseBaidu(root, 10)
	if len(results) != 2 || results[0].Title != "First" ||
		results[0].Snippet != "First snippet" || results[1].Rank != 2 {
		t.Fatalf("unexpected Baidu results: %#v", results)
	}
}

func TestUpdateKeepsStoredAPIKey(t *testing.T) {
	manager, err := NewManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	settings := Settings{
		Enabled:         true,
		DefaultProvider: "google",
		Providers: []ProviderConfig{{
			ID: "google", Kind: "google_cse", Enabled: true,
			APIKey: "secret", SearchEngineID: "engine",
		}},
	}
	if err := manager.Update(settings); err != nil {
		t.Fatal(err)
	}
	settings.Providers[0].APIKey = ""
	settings.Providers[0].SearchEngineID = "new-engine"
	if err := manager.Update(settings); err != nil {
		t.Fatal(err)
	}
	if got := manager.config.Providers[0].APIKey; got != "secret" {
		t.Fatalf("stored API key was lost: %q", got)
	}
	public := manager.Settings()
	if public.Providers[0].APIKey != "" || !public.Providers[0].HasAPIKey {
		t.Fatalf("public settings leaked or hid credential state: %#v", public.Providers[0])
	}
}

func TestSettingsAlwaysExposeProvidersArray(t *testing.T) {
	manager, err := NewManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	settings := manager.Settings()
	if settings.Providers == nil {
		t.Fatal("providers must be an empty array, not nil")
	}
	data, err := json.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"providers":[]`) {
		t.Fatalf("providers array missing from JSON: %s", data)
	}
}

type roundTripFunc func() string

func (fn roundTripFunc) RoundTrip(*http.Request) (*http.Response, error) {
	body := fn()
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}, nil
}
