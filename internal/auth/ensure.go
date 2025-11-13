package auth

import (
	"context"
	"fmt"
	"os"

	"github.com/datum-cloud/datum-mcp/internal/authutil"
)

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
	// 2) Keyring token via TokenSource (auto-refresh).
	ts, err := authutil.GetTokenSource(ctx)
	if err == nil && ts != nil {
		t, err := ts.Token()
		if err == nil && t != nil && t.AccessToken != "" {
			return t.AccessToken, nil
		}
	}
	// 3) Run login flow.
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
	return t.AccessToken, nil
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
