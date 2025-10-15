package auth

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/datum-cloud/datum-mcp/internal/authutil"
)

var cachedToken string
var cachedExpiry time.Time

func tokenFromEnv() (string, bool) {
	if v := os.Getenv("DATUM_TOKEN"); v != "" {
		return v, true
	}
	return "", false
}

func EnsureAuth(ctx context.Context) (string, error) {
	// 1) Env var overrides everything.
	if tok, ok := tokenFromEnv(); ok {
		return tok, nil
	}
	// 2) Cached token if still valid.
	if cachedToken != "" && time.Now().Before(cachedExpiry) {
		return cachedToken, nil
	}
	// 3) Keyring token via TokenSource (auto-refresh).
	ts, err := authutil.GetTokenSource(ctx)
	if err == nil && ts != nil {
		t, err := ts.Token()
		if err == nil && t != nil && t.AccessToken != "" {
			cachedToken = t.AccessToken
			// Best-effort expiry; if zero, cache briefly
			if !t.Expiry.IsZero() {
				cachedExpiry = t.Expiry
			} else {
				cachedExpiry = time.Now().Add(5 * time.Minute)
			}
			return cachedToken, nil
		}
	}
	// 4) Run login flow.
	if err := RunLoginFlow(ctx, getenvBool("DATUM_VERBOSE", false)); err != nil {
		return "", err
	}
	// After login, try TokenSource again.
	ts, err = authutil.GetTokenSource(ctx)
	if err != nil {
		return "", fmt.Errorf("login succeeded but failed to get token source: %w", err)
	}
	t, err := ts.Token()
	if err != nil || t == nil || t.AccessToken == "" {
		return "", fmt.Errorf("failed to retrieve access token after login: %v", err)
	}
	cachedToken = t.AccessToken
	if !t.Expiry.IsZero() {
		cachedExpiry = t.Expiry
	} else {
		cachedExpiry = time.Now().Add(5 * time.Minute)
	}
	return cachedToken, nil
}

func getenvBool(k string, def bool) bool {
	if v := os.Getenv(k); v != "" {
		if v == "1" || v == "true" || v == "TRUE" || v == "yes" {
			return true
		}
		if v == "0" || v == "false" || v == "FALSE" || v == "no" {
			return false
		}
	}
	return def
}
