package authutil

import (
	"errors"
	"testing"

	"github.com/datum-cloud/datum-mcp/internal/keyring"
)

func TestMain(m *testing.M) {
	keyring.MockInit()
	m.Run()
}

func TestGetAPIHostnameEnvOverrideNeedsNoStoredCredentials(t *testing.T) {
	// No prior login at all (fresh, empty mocked keyring): GetActiveCredentials
	// would fail with ErrNoActiveUser. DATUM_API_HOSTNAME must be enough on
	// its own, so DATUM_TOKEN + DATUM_API_HOSTNAME together can run fully
	// non-interactively without ever touching stored credentials.
	if _, _, err := GetActiveCredentials(); !errors.Is(err, ErrNoActiveUser) {
		t.Fatalf("test setup assumption broken: expected ErrNoActiveUser with no credentials stored, got %v", err)
	}

	t.Setenv("DATUM_API_HOSTNAME", "api.ci-example.net")
	got, err := GetAPIHostname()
	if err != nil {
		t.Fatalf("expected DATUM_API_HOSTNAME alone to succeed, got error: %v", err)
	}
	if got != "api.ci-example.net" {
		t.Errorf("expected the env override, got %q", got)
	}
}

func TestGetAPIHostnameFallsBackToStoredCredentialsWhenEnvUnset(t *testing.T) {
	t.Setenv("DATUM_API_HOSTNAME", "")
	if _, err := GetAPIHostname(); !errors.Is(err, ErrNoActiveUser) {
		t.Errorf("expected the pre-existing stored-credentials path (and its error) to still apply, got %v", err)
	}
}
