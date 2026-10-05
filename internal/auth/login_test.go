package auth

import (
	"context"
	"encoding/json"
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
