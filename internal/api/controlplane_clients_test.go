package api

import (
	"context"
	"encoding/json"
	"testing"

	"golang.org/x/oauth2"

	"github.com/datum-cloud/datum-mcp/internal/authutil"
	"github.com/datum-cloud/datum-mcp/internal/keyring"
)

// seedFakeCredentials makes authutil.GetAPIHostname/GetActiveCredentials
// succeed without a real login, by writing directly into the mocked keyring
// the same way RunLoginFlow does after a real one.
func seedFakeCredentials(t *testing.T) {
	t.Helper()
	keyring.MockInit()
	creds := authutil.StoredCredentials{
		Hostname:    "auth.datum.net",
		APIHostname: "api.datum.net",
		UserEmail:   "test@example.com",
		Token:       &oauth2.Token{AccessToken: "fake-token"},
	}
	b, err := json.Marshal(creds)
	if err != nil {
		t.Fatalf("marshal fake credentials: %v", err)
	}
	if err := keyring.Set(authutil.ServiceName, creds.UserEmail, string(b)); err != nil {
		t.Fatalf("seed credentials: %v", err)
	}
	if err := keyring.Set(authutil.ServiceName, authutil.ActiveUserKey, creds.UserEmail); err != nil {
		t.Fatalf("seed active user: %v", err)
	}
}

func TestNewProjectHTTPClientSetsRequestTimeout(t *testing.T) {
	seedFakeCredentials(t)
	t.Setenv("DATUM_TOKEN", "fake-token") // skip the real EnsureAuth token-source path too

	cli, host, err := NewProjectHTTPClient(context.Background(), "demo-project")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cli.Timeout != requestTimeout {
		t.Errorf("expected client Timeout %v, got %v", requestTimeout, cli.Timeout)
	}
	if host != "https://api.datum.net" {
		t.Errorf("expected host derived from seeded credentials, got %q", host)
	}
}
