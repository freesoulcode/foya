// Package kernel 是内核的组合根:装配所有 service(session、agent、
// broker、state、provider 等)并暴露给 server。
package kernel

import (
	"github.com/freesoulcode/foya/internal/agent"
	"github.com/freesoulcode/foya/internal/backend"
	"github.com/freesoulcode/foya/internal/broker"
	"github.com/freesoulcode/foya/internal/config"
	"github.com/freesoulcode/foya/internal/event"
	"github.com/freesoulcode/foya/internal/provider"
	"github.com/freesoulcode/foya/internal/provider/openai"
	"github.com/freesoulcode/foya/internal/session"
	"github.com/freesoulcode/foya/internal/state"
)

// App 是内核组合根。
type App struct {
	cfg     config.Config
	backend *backend.Backend
}

// New 按配置装配内核。
func New(cfg config.Config) *App {
	sessions := session.NewMemManager()
	log := state.NewMemLog()
	bus := broker.New[event.Event]()

	prov, model := buildProvider(cfg.Provider)
	engine := agent.NewEngine(log, bus, prov, model)

	be := backend.New(sessions, log, bus, engine, buildProvider, cfg.Provider)
	return &App{cfg: cfg, backend: be}
}

// buildProvider 按配置构造 OpenAI 兼容 provider,返回 provider 与默认模型名。
func buildProvider(pc config.Provider) (provider.Provider, string) {
	p := openai.New(openai.Config{BaseURL: pc.BaseURL, APIKey: pc.APIKey, Model: pc.Model})
	return p, pc.Model
}

// Backend 暴露业务层,供 server 使用。
func (a *App) Backend() *backend.Backend { return a.backend }

// Config 返回内核配置。
func (a *App) Config() config.Config { return a.cfg }
