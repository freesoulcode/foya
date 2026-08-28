package tool

import (
	"encoding/json"
	"sync"

	"github.com/freesoulcode/foya/internal/provider"
)

// memRegistry 是 Registry 的内存实现(线程安全)。
type memRegistry struct {
	mu    sync.RWMutex
	tools map[string]Tool
}

// NewRegistry 创建一个空的内存工具注册表。
func NewRegistry() Registry {
	return &memRegistry{tools: make(map[string]Tool)}
}

func (r *memRegistry) Register(t Tool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	name := t.Name()
	if _, exists := r.tools[name]; exists {
		// 内置工具重复注册是编程错误,panic 以便尽早发现。
		panic("tool already registered: " + name)
	}
	r.tools[name] = t
}

func (r *memRegistry) RegisterExternal(t Tool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	// 外部工具(MCP/动态)同名时静默跳过,不 panic。
	r.tools[t.Name()] = t
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

// Specs 把所有非隐藏工具的 JSON Schema 转为 provider.ToolDef,
// 供 engine 组装模型请求时使用。
func (r *memRegistry) Specs() []provider.ToolDef {
	tools := r.List()
	defs := make([]provider.ToolDef, 0, len(tools))
	for _, t := range tools {
		spec := t.Spec()
		var params json.RawMessage
		if len(spec) > 0 {
			// spec 本身就是完整的 JSON Schema 对象,直接作为 parameters。
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
