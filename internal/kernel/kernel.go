// Package kernel 是内核的组合根:装配所有 service(session、agent、
// broker、approval、state、provider、credential 等)并暴露给 server。
//
// 脚手架阶段各依赖尚未实现,App 仅串起已有骨架,保证工程可编译、可启动。
package kernel

import (
	"github.com/freesoulcode/foya/internal/backend"
	"github.com/freesoulcode/foya/internal/config"
)

// App 是内核组合根。
type App struct {
	cfg     config.Config
	backend *backend.Backend
}

// New 按配置装配内核。
//
// TODO: 实例化 session.Manager、agent.Loop、broker、approval.Gateway、
//       state.Log、provider.Provider、credential.Store 并注入 backend。
func New(cfg config.Config) *App {
	be := backend.New(nil, nil)
	return &App{cfg: cfg, backend: be}
}

// Backend 暴露业务层,供 server 使用。
func (a *App) Backend() *backend.Backend { return a.backend }

// Config 返回内核配置。
func (a *App) Config() config.Config { return a.cfg }
