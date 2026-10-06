package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestDiscoverProviderWithRetrySucceedsFirstTry(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		origin := "http://" + r.Host
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"issuer":                 origin,
			"authorization_endpoint": origin + "/authorize",
			"token_endpoint":         origin + "/token",
			"jwks_uri":               origin + "/jwks",
		})
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := discoverProviderWithRetry(ctx, srv.URL); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if hits != 1 {
		t.Errorf("expected exactly 1 discovery request on first-try success, got %d", hits)
	}
}

func TestDiscoverProviderWithRetryExhaustsAttemptsOnPersistentFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	start := time.Now()
	_, err := discoverProviderWithRetry(context.Background(), srv.URL)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected an error after exhausting all attempts")
	}
	// 2 backoff waits between 3 attempts: ~500ms + ~1000ms.
	if elapsed < oidcDiscoveryBaseDelay {
		t.Errorf("expected at least one backoff wait to have elapsed, got %s", elapsed)
	}
}

func TestDiscoverProviderWithRetrySucceedsAfterTransientFailure(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if hits < 3 {
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
			return
		}
		origin := "http://" + r.Host
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"issuer":                 origin,
			"authorization_endpoint": origin + "/authorize",
			"token_endpoint":         origin + "/token",
			"jwks_uri":               origin + "/jwks",
		})
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := discoverProviderWithRetry(ctx, srv.URL); err != nil {
		t.Fatalf("expected eventual success after transient 503s, got: %v", err)
	}
	if hits != 3 {
		t.Errorf("expected 3 requests (2 failures + 1 success), got %d", hits)
	}
}

func TestDiscoverProviderWithRetryDoesNotRetryOn4xx(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()

	start := time.Now()
	_, err := discoverProviderWithRetry(context.Background(), srv.URL)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected an error for a persistent 404")
	}
	if hits != 1 {
		t.Errorf("expected exactly 1 request (a 404 is not transient, so no retry), got %d", hits)
	}
	if elapsed > oidcDiscoveryBaseDelay {
		t.Errorf("expected an immediate return with no backoff wait, took %s", elapsed)
	}
}

func TestDiscoverProviderWithRetryDoesNotRetryOnMalformedDiscoveryDocument(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("not valid json"))
	}))
	defer srv.Close()

	start := time.Now()
	_, err := discoverProviderWithRetry(context.Background(), srv.URL)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected an error for a malformed discovery document")
	}
	if hits != 1 {
		t.Errorf("expected exactly 1 request (a parse error is not transient, so no retry), got %d", hits)
	}
	if elapsed > oidcDiscoveryBaseDelay {
		t.Errorf("expected an immediate return with no backoff wait, took %s", elapsed)
	}
}

func TestDiscoverProviderWithRetryRespectsContextCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already canceled before the first attempt

	start := time.Now()
	_, err := discoverProviderWithRetry(ctx, srv.URL)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected an error for an already-canceled context")
	}
	if elapsed > oidcDiscoveryBaseDelay {
		t.Errorf("expected an immediate return on a pre-canceled context, took %s", elapsed)
	}
}

func TestIsRetryableOIDCDiscoveryError(t *testing.T) {
	if isRetryableOIDCDiscoveryError(nil) {
		t.Error("expected nil error to be non-retryable")
	}

	netErr := &net.DNSError{Err: "no such host", Name: "auth.example.invalid", IsTimeout: true}
	if !isRetryableOIDCDiscoveryError(netErr) {
		t.Error("expected a net.Error (DNS timeout) to be retryable")
	}
	wrapped := fmt.Errorf("get: %w", netErr)
	if !isRetryableOIDCDiscoveryError(wrapped) {
		t.Error("expected a wrapped net.Error to still be detected as retryable")
	}

	if !isRetryableOIDCDiscoveryError(errors.New("503 Service Unavailable: try again later")) {
		t.Error("expected a 5xx-prefixed message to be retryable")
	}
	if isRetryableOIDCDiscoveryError(errors.New("404 Not Found: no such issuer")) {
		t.Error("expected a 4xx-prefixed message to be non-retryable")
	}
	if isRetryableOIDCDiscoveryError(errors.New("oidc: failed to decode provider discovery object: unexpected EOF")) {
		t.Error("expected a parse error to be non-retryable")
	}
	if isRetryableOIDCDiscoveryError(errors.New("oidc: issuer URL provided to client did not match")) {
		t.Error("expected an issuer-mismatch error to be non-retryable")
	}
}

func TestHasRetryable5xxPrefix(t *testing.T) {
	cases := []struct {
		msg  string
		want bool
	}{
		{"500 Internal Server Error: oops", true},
		{"503 Service Unavailable: try again", true},
		{"599 Network Connect Timeout Error: x", true},
		{"400 Bad Request: x", false},
		{"404 Not Found: x", false},
		{"", false},
		{"oidc: failed to decode provider discovery object: x", false},
		{"not a status line at all", false},
	}
	for _, c := range cases {
		if got := hasRetryable5xxPrefix(c.msg); got != c.want {
			t.Errorf("hasRetryable5xxPrefix(%q) = %v, want %v", c.msg, got, c.want)
		}
	}
}
