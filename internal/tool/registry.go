package tool

import (
	"encoding/json"
	"sort"
	"strings"
	"sync"
	"unicode"

	"github.com/freesoulcode/foya/internal/provider"
)

// memRegistry is the thread-safe in-memory Registry implementation.
type memRegistry struct {
	mu    sync.RWMutex
	tools map[string]Tool
}

// NewRegistry creates an empty in-memory tool registry.
func NewRegistry() Registry {
	return &memRegistry{tools: make(map[string]Tool)}
}

func (r *memRegistry) Register(t Tool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	name := t.Name()
	if _, exists := r.tools[name]; exists {
		// Duplicate built-in registration is a programming error.
		panic("tool already registered: " + name)
	}
	r.tools[name] = t
}

func (r *memRegistry) RegisterExternal(t Tool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	// External MCP or dynamic tools replace an existing entry by name.
	r.tools[t.Name()] = t
}

func (r *memRegistry) Unregister(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.tools, name)
}

func (r *memRegistry) Get(name string) (Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	return t, ok
}

func (r *memRegistry) List() []Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Tool, 0, len(r.tools))
	for _, t := range r.tools {
		if t.Exposure() != ExposureHidden {
			out = append(out, t)
		}
	}
	return out
}

// Specs returns provider definitions for all non-hidden tools.
func (r *memRegistry) Specs() []provider.ToolDef {
	return r.SpecsFor(nil)
}

func (r *memRegistry) SpecsFor(activeDeferred map[string]bool) []provider.ToolDef {
	r.mu.RLock()
	tools := make([]Tool, 0, len(r.tools))
	for _, t := range r.tools {
		switch t.Exposure() {
		case ExposureHidden:
			continue
		case ExposureDeferred:
			if !activeDeferred[t.Name()] {
				continue
			}
		}
		tools = append(tools, t)
	}
	r.mu.RUnlock()
	sort.Slice(tools, func(i, j int) bool {
		return tools[i].Name() < tools[j].Name()
	})
	defs := make([]provider.ToolDef, 0, len(tools))
	for _, t := range tools {
		spec := t.Spec()
		var params json.RawMessage
		if len(spec) > 0 {
			// A tool spec is already a complete JSON Schema object.
			params = spec
		} else {
			params = json.RawMessage(`{"type":"object","properties":{}}`)
		}
		defs = append(defs, provider.ToolDef{
			Type: "function",
			Function: provider.FunctionDef{
				Name:        t.Name(),
				Description: t.Description(),
				Parameters:  params,
			},
		})
	}
	return defs
}

func (r *memRegistry) SearchDeferred(query string, limit int) []Tool {
	query = strings.ToLower(strings.TrimSpace(query))
	if limit <= 0 {
		limit = 8
	}
	if limit > 20 {
		limit = 20
	}
	queryTerms := searchTerms(query)
	r.mu.RLock()
	candidates := make([]Tool, 0)
	scores := make(map[string]int)
	for _, t := range r.tools {
		if t.Exposure() != ExposureDeferred {
			continue
		}
		haystack := strings.ToLower(t.Name() + "\n" + t.Description())
		score := deferredSearchScore(haystack, query, queryTerms)
		if query == "" || score > 0 {
			candidates = append(candidates, t)
			scores[t.Name()] = score
		}
	}
	r.mu.RUnlock()
	sort.Slice(candidates, func(i, j int) bool {
		if scores[candidates[i].Name()] != scores[candidates[j].Name()] {
			return scores[candidates[i].Name()] > scores[candidates[j].Name()]
		}
		return candidates[i].Name() < candidates[j].Name()
	})
	if len(candidates) > limit {
		return candidates[:limit]
	}
	return candidates
}

func searchTerms(query string) []string {
	fields := strings.FieldsFunc(query, func(r rune) bool {
		return unicode.IsSpace(r) || r == '_' || r == '-' || r == '/' || r == ':' || r == ',' ||
			r == '.' || r == ';' || r == '(' || r == ')' || r == '[' ||
			r == ']' || r == '{' || r == '}'
	})
	terms := make([]string, 0, len(fields))
	seen := make(map[string]bool, len(fields))
	for _, field := range fields {
		field = strings.TrimSpace(field)
		if len(field) < 3 || seen[field] {
			continue
		}
		seen[field] = true
		terms = append(terms, field)
	}
	return terms
}

func deferredSearchScore(haystack, query string, terms []string) int {
	if query != "" && strings.Contains(haystack, query) {
		return 100
	}
	score := 0
	for _, term := range terms {
		if strings.Contains(haystack, term) {
			score++
		}
	}
	return score
}
