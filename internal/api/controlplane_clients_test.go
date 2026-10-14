package api

import (
	"context"
	"encoding/json"
	"k8s.io/client-go/rest"
	"net/http"
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

func TestNewProjectHTTPClientHasNoWholeRequestTimeout(t *testing.T) {
	seedFakeCredentials(t)
	t.Setenv("DATUM_TOKEN", "fake-token") // skip the real EnsureAuth token-source path too

	cli, host, err := NewProjectHTTPClient(context.Background(), "demo-project")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// No Client.Timeout: that would cover the whole round trip including
	// body read, killing a slow-but-progressing transfer (a large OpenAPI
	// document, say) partway through. The per-attempt stall guard is
	// ResponseHeaderTimeout on the underlying transport instead, checked
	// below via the actual wrapping chain WrapTransport builds.
	if cli.Timeout != 0 {
		t.Errorf("expected no whole-request Client.Timeout, got %v", cli.Timeout)
	}
	if host != "https://api.datum.net" {
		t.Errorf("expected host derived from seeded credentials, got %q", host)
	}

	prefix, ok := cli.Transport.(*prefixRoundTripper)
	if !ok {
		t.Fatalf("expected outermost *prefixRoundTripper, got %T", cli.Transport)
	}
	authed, ok := prefix.next.(*authRoundTripper)
	if !ok {
		t.Fatalf("expected *authRoundTripper, got %T", prefix.next)
	}
	retried, ok := authed.next.(*retryRoundTripper)
	if !ok {
		t.Fatalf("expected *retryRoundTripper (raw http.Client path needs its own retry - client-go's doesn't reach here), got %T", authed.next)
	}
	base, ok := retried.next.(*http.Transport)
	if !ok {
		t.Fatalf("expected innermost *http.Transport, got %T", retried.next)
	}
	if base.ResponseHeaderTimeout != requestTimeout {
		t.Errorf("expected ResponseHeaderTimeout %v, got %v", requestTimeout, base.ResponseHeaderTimeout)
	}
}

// Each control plane serves its own kinds, so each gets its own mapper: one
// discovered against an org control plane can't answer for a project's.
func TestMapperIsPerControlPlane(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	cfg := &rest.Config{Host: "https://api.example.test"}
	org, err := getOrCreateMapper(cfg, "/apis/resourcemanager.miloapis.com/v1alpha1/organizations/o/control-plane")
	if err != nil {
		t.Fatal(err)
	}
	project, err := getOrCreateMapper(cfg, projectControlPlanePrefix("p"))
	if err != nil {
		t.Fatal(err)
	}
	if org == project {
		t.Errorf("expected the org and project control planes to get different mappers")
	}
	again, err := getOrCreateMapper(cfg, projectControlPlanePrefix("p"))
	if err != nil {
		t.Fatal(err)
	}
	if again != project {
		t.Errorf("expected one control plane to reuse its mapper")
	}
}

func TestSafePathComponentStaysOneDirectory(t *testing.T) {
	for in, want := range map[string]string{
		"/apis/resourcemanager.miloapis.com/v1alpha1/projects/p/control-plane": "apis_resourcemanager.miloapis.com_v1alpha1_projects_p_control-plane",
		"/apis/x/projects/../../../etc/control-plane":                          "apis_x_projects_.._.._.._etc_control-plane",
		"":   "root",
		"/":  "root",
		"..": "root",
	} {
		if got := safePathComponent(in); got != want {
			t.Errorf("safePathComponent(%q) = %q, want %q", in, got, want)
		}
	}
}
