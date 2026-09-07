// Package credential manages user-owned provider credentials.
//
// Secrets belong to the kernel. Clients can write them through a dedicated
// channel, but they never enter the event stream or a read response.
package credential

import (
	"context"
	"time"
)

// Kind identifies a credential type.
type Kind string

const (
	KindAPIKey     Kind = "api_key"
	KindOAuthToken Kind = "oauth_token"
)

// Backend identifies a credential storage implementation.
type Backend string

const (
	BackendKeychain      Backend = "keychain"       // Preferred native keychain.
	BackendEncryptedFile Backend = "encrypted_file" // Headless fallback.
)

// Locator addresses a credential by connection and kind.
type Locator struct {
	ConnectionID string
	Kind         Kind
}

// Status exposes presence without returning the secret.
type Status struct {
	Configured bool
	UpdatedAt  time.Time
}

// Store abstracts keychain and encrypted-file backends.
type Store interface {
	Set(ctx context.Context, loc Locator, secret string) error
	Get(ctx context.Context, loc Locator) (string, error) // Used only at execution boundaries.
	Delete(ctx context.Context, loc Locator) error
	Status(ctx context.Context, loc Locator) (Status, error)
	Backend() Backend
}
