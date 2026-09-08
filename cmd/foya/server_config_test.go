package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/freesoulcode/foya/internal/config"
)

func clearServerEnvironment(t *testing.T) {
	t.Helper()
	for _, name := range []string{
		"FOYA_DATA_DIR",
		"FOYA_SOCKET_PATH",
		"FOYA_LISTEN_ADDR",
		"FOYA_AUTH_TOKEN_SHA256",
		"FOYA_AUTH_TOKEN_HASH_FILE",
		"FOYA_TLS_CERT_FILE",
		"FOYA_TLS_KEY_FILE",
		"FOYA_ALLOW_PLAINTEXT",
	} {
		t.Setenv(name, "")
	}
}

func TestDaemonConfigLoadsRemoteTokenFile(t *testing.T) {
	clearServerEnvironment(t)
	dir := t.TempDir()
	tokenHashPath := filepath.Join(dir, "token.sha256")
	const tokenHash = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	if err := os.WriteFile(tokenHashPath, []byte(tokenHash+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := daemonConfig([]string{
		"--listen", "127.0.0.1:8787",
		"--data-dir", filepath.Join(dir, "data"),
		"--auth-token-hash-file", tokenHashPath,
		"--allow-plaintext",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Transport != config.TransportTCP || got.AuthTokenSHA256 != tokenHash {
		t.Fatalf("remote config = %+v", got)
	}
	if got.SocketPath != filepath.Join(dir, "data", "kernel.sock") {
		t.Fatalf("socket path = %q", got.SocketPath)
	}
}

func TestDaemonConfigRejectsUnsafeRemoteListener(t *testing.T) {
	clearServerEnvironment(t)
	if _, err := daemonConfig([]string{"--listen", "0.0.0.0:8787"}); err == nil {
		t.Fatal("remote listener without authentication and TLS was accepted")
	}
}
