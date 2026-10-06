package auth

import (
	"context"
	"testing"

	"github.com/datum-cloud/datum-mcp/internal/keyring"
)

func TestMain(m *testing.M) {
	keyring.MockInit()
	m.Run()
}

func TestCheckAuthEnvOverride(t *testing.T) {
	t.Setenv("DATUM_TOKEN", "test-token")
	tok, ok := CheckAuth(context.Background())
	if !ok || tok != "test-token" {
		t.Errorf("expected env override to win, got tok=%q ok=%v", tok, ok)
	}
}

func TestCheckAuthNoCredentialsDoesNotLogin(t *testing.T) {
	// No DATUM_TOKEN and nothing in the (mocked, empty) keyring: CheckAuth
	// must report false without attempting the interactive login flow (no
	// browser, no network, no hang).
	t.Setenv("DATUM_TOKEN", "")
	tok, ok := CheckAuth(context.Background())
	if ok || tok != "" {
		t.Errorf("expected no usable token, got tok=%q ok=%v", tok, ok)
	}
}
