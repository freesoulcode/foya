package server

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/freesoulcode/foya/internal/config"
)

func TestBearerAuthenticationProtectsRemoteRoutes(t *testing.T) {
	const token = "0123456789abcdef0123456789abcdef"
	digest := sha256.Sum256([]byte(token))
	handler := New(config.Config{
		Transport:       config.TransportTCP,
		AuthTokenSHA256: hex.EncodeToString(digest[:]),
	}, nil).Handler()

	for name, authorization := range map[string]string{
		"missing": "",
		"invalid": "Bearer wrong",
	} {
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/sessions", nil)
			req.Header.Set("Authorization", authorization)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
			}
			if rec.Header().Get("WWW-Authenticate") == "" {
				t.Fatal("WWW-Authenticate header is missing")
			}
		})
	}

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("authorized status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestHealthCheckDoesNotRequireAuthentication(t *testing.T) {
	handler := New(config.Config{
		Transport:       config.TransportTCP,
		AuthTokenSHA256: "invalid configuration still protects non-health routes",
	}, nil).Handler()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || rec.Body.String() != "ok" {
		t.Fatalf("health response = %d %q", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("cache control = %q", rec.Header().Get("Cache-Control"))
	}
}
