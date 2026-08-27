// Package credential 管理用户的 LLM API Key / provider 凭证。
//
// key 归内核所有:前端只输入,经独立通道单向传入内核,绝不进事件流、
// 不广播。存储首选 OS keychain(桌面),容器/无桌面环境降级到加密文件。
// 查询只返回是否配置,永不返回 secret 明文。
package credential

import (
	"context"
	"time"
)

// Kind 标识凭证类型。
type Kind string

const (
	KindAPIKey     Kind = "api_key"
	KindOAuthToken Kind = "oauth_token"
)

// Backend 标识凭证存储后端。
type Backend string

const (
	BackendKeychain      Backend = "keychain"       // OS 原生密钥库(首选)
	BackendEncryptedFile Backend = "encrypted_file" // 无 keychain 环境降级
)

// Locator 按「连接 + 类型」寻址凭证,不按 secret 值寻址。
type Locator struct {
	ConnectionID string
	Kind         Kind
}

// Status 对外只暴露是否配置,结构上不含 secret。
type Status struct {
	Configured bool
	UpdatedAt  time.Time
}

// Store 是凭证存储:屏蔽 keychain / 加密文件两种后端。
type Store interface {
	Set(ctx context.Context, loc Locator, secret string) error
	Get(ctx context.Context, loc Locator) (string, error) // 仅执行边界内部使用
	Delete(ctx context.Context, loc Locator) error
	Status(ctx context.Context, loc Locator) (Status, error)
	Backend() Backend
}
