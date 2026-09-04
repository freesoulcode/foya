package tool

import (
	"context"
	"testing"

	"github.com/freesoulcode/foya/internal/approval"
	"github.com/freesoulcode/foya/internal/broker"
	"github.com/freesoulcode/foya/internal/browseruse"
	"github.com/freesoulcode/foya/internal/event"
	"github.com/freesoulcode/foya/internal/testkit"
)

type registryTestTool struct {
	name        string
	description string
	exposure    Exposure
}

func (t registryTestTool) Name() string        { return t.name }
func (t registryTestTool) Description() string { return t.description }
func (t registryTestTool) Spec() []byte        { return nil }
func (t registryTestTool) Exposure() Exposure  { return t.exposure }
func (t registryTestTool) Run(context.Context, Call) (Result, error) {
	return Result{}, nil
}

func TestSpecsForHidesDeferredUntilActive(t *testing.T) {
	registry := NewRegistry()
	registry.Register(registryTestTool{name: "direct", exposure: ExposureDirect})
	registry.Register(registryTestTool{name: "deferred", exposure: ExposureDeferred})

	defs := registry.SpecsFor(nil)
	if len(defs) != 1 || defs[0].Function.Name != "direct" {
		t.Fatalf("defs before activation = %#v", defs)
	}

	defs = registry.SpecsFor(map[string]bool{"deferred": true})
	if len(defs) != 2 {
		t.Fatalf("defs after activation length = %d, want 2", len(defs))
	}
}

func TestSearchDeferredMatchesQueryTerms(t *testing.T) {
	registry := NewRegistry()
	registry.Register(registryTestTool{
		name:        "browser_snapshot",
		description: "Inspect the current embedded browser page.",
		exposure:    ExposureDeferred,
	})
	registry.Register(registryTestTool{
		name:        "direct_browser_help",
		description: "Visible helper.",
		exposure:    ExposureDirect,
	})

	matches := registry.SearchDeferred("inspect browser page", 8)
	if len(matches) != 1 || matches[0].Name() != "browser_snapshot" {
		t.Fatalf("matches = %#v", matches)
	}
}

func TestBrowserToolsIncludeScroll(t *testing.T) {
	log := testkit.NewLog()
	bus := broker.New[event.Event]()
	controller, err := browseruse.NewController(t.TempDir(), bus, log)
	if err != nil {
		t.Fatal(err)
	}
	gateway := approval.NewGateway(bus, log)
	registry := NewRegistry()
	for _, browserTool := range BrowserTools(controller, gateway) {
		registry.Register(browserTool)
	}

	tool, ok := registry.Get("browser_scroll")
	if !ok {
		t.Fatal("browser_scroll was not registered")
	}
	if tool.Exposure() != ExposureDeferred {
		t.Fatalf("browser_scroll exposure = %q, want %q", tool.Exposure(), ExposureDeferred)
	}
	matches := registry.SearchDeferred("scroll browser page down", 8)
	for _, match := range matches {
		if match.Name() == "browser_scroll" {
			return
		}
	}
	t.Fatalf("browser_scroll not found in deferred search matches: %#v", matches)
}

func TestBrowserNavigateAndSnapshotAreDirect(t *testing.T) {
	log := testkit.NewLog()
	bus := broker.New[event.Event]()
	controller, err := browseruse.NewController(t.TempDir(), bus, log)
	if err != nil {
		t.Fatal(err)
	}
	gateway := approval.NewGateway(bus, log)
	registry := NewRegistry()
	for _, browserTool := range BrowserTools(controller, gateway) {
		registry.Register(browserTool)
	}

	for _, name := range []string{"browser_navigate", "browser_snapshot"} {
		tool, ok := registry.Get(name)
		if !ok {
			t.Fatalf("%s was not registered", name)
		}
		if tool.Exposure() != ExposureDirect {
			t.Fatalf("%s exposure = %q, want %q", name, tool.Exposure(), ExposureDirect)
		}
	}
}
