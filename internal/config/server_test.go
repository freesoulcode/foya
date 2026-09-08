package config

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestDefaultRemoteServerFromEnvironment(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("FOYA_DATA_DIR", dataDir)
	t.Setenv("FOYA_LISTEN_ADDR", "127.0.0.1:8787")
	t.Setenv("FOYA_AUTH_TOKEN_SHA256", "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	t.Setenv("FOYA_ALLOW_PLAINTEXT", "true")

	got := Default()
	if got.Transport != TransportTCP || got.Lifecycle != LifecycleService {
		t.Fatalf("transport = %q, lifecycle = %q", got.Transport, got.Lifecycle)
	}
	if got.DataDir != dataDir || got.SocketPath != filepath.Join(dataDir, "kernel.sock") {
		t.Fatalf("data paths = %q, %q", got.DataDir, got.SocketPath)
	}
	if err := ValidateServerConfig(got); err != nil {
		t.Fatalf("valid remote config rejected: %v", err)
	}
}

func TestRemoteServerRequiresAuthenticationAndEncryption(t *testing.T) {
	base := Config{
		Transport: TransportTCP,
		Addr:      "0.0.0.0:8787",
		DataDir:   t.TempDir(),
	}
	if err := ValidateServerConfig(base); !errors.Is(err, ErrInvalidServerConfig) {
		t.Fatalf("missing token error = %v", err)
	}

	base.AuthTokenSHA256 = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	if err := ValidateServerConfig(base); !errors.Is(err, ErrInvalidServerConfig) {
		t.Fatalf("missing TLS error = %v", err)
	}

	base.AllowPlaintext = true
	if err := ValidateServerConfig(base); err != nil {
		t.Fatalf("explicit plaintext config rejected: %v", err)
	}

	base.AllowPlaintext = false
	base.TLSCertFile = "server.crt"
	if err := ValidateServerConfig(base); !errors.Is(err, ErrInvalidServerConfig) {
		t.Fatalf("incomplete TLS error = %v", err)
	}
	base.TLSKeyFile = "server.key"
	if err := ValidateServerConfig(base); err != nil {
		t.Fatalf("TLS config rejected: %v", err)
	}
}
