package server

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/datum-cloud/datum-mcp/internal/api"

	"github.com/datum-cloud/datum-mcp/internal/keyring"
	"github.com/datum-cloud/datum-mcp/internal/org"
	"github.com/datum-cloud/datum-mcp/internal/project"
)

func TestMain(m *testing.M) {
	keyring.MockInit()
	// Tests see every toolset unless they set their own; the lean default
	// has tests of its own.
	if os.Getenv("DATUM_MCP_ENABLE_TOOLSETS") == "" {
		os.Setenv("DATUM_MCP_ENABLE_TOOLSETS", "all")
	}
	// Never reach a real API from tests; ones that need an identity stub it.
	whoAmI = func(context.Context) (api.Identity, error) { return api.Identity{}, errors.New("no API in tests") }
	m.Run()
}

// stubIdentity makes the API report username (with uid "uid-for-tests") for
// any token, and clears the cached lookup so the next call sees it.
func stubIdentity(t *testing.T, username string) *int {
	t.Helper()
	calls := 0
	prev := whoAmI
	whoAmI = func(context.Context) (api.Identity, error) {
		calls++
		return api.Identity{Username: username, UID: "uid-for-tests"}, nil
	}
	identityToken, identityValue = "", api.Identity{}
	t.Cleanup(func() { whoAmI = prev; identityToken, identityValue = "", api.Identity{} })
	return &calls
}

// TestResolveProjectName exercises the full override > DATUM_PROJECT >
// active-project > service-account precedence as ordered subtests, since the
// underlying keyring mock is process-global and has no per-test reset.
func TestResolveProjectName(t *testing.T) {
	ctx := context.Background()
	t.Setenv("DATUM_TOKEN", "")
	t.Setenv("DATUM_PROJECT", "")

	t.Run("override wins even before anything is active", func(t *testing.T) {
		got, err := resolveProjectName(ctx, "explicit-project")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "explicit-project" {
			t.Errorf("expected override to win, got %q", got)
		}
	})

	t.Run("errors when nothing is active and no override given", func(t *testing.T) {
		_, err := resolveProjectName(ctx, "")
		if err == nil {
			t.Fatalf("expected an error when no project is active")
		}
		if !strings.Contains(err.Error(), "no active project set") || !strings.Contains(err.Error(), "DATUM_PROJECT") {
			t.Errorf("expected a descriptive error naming DATUM_PROJECT, got %q", err.Error())
		}
	})

	t.Run("a person's token has no project of its own", func(t *testing.T) {
		t.Setenv("DATUM_TOKEN", "user-token")
		stubIdentity(t, "someone@example.com")
		if _, err := resolveProjectName(ctx, ""); err == nil {
			t.Fatalf("expected an error: a person's identity implies no project")
		}
	})

	t.Run("a service account falls back to its own project", func(t *testing.T) {
		t.Setenv("DATUM_TOKEN", "sa-token")
		stubIdentity(t, "robot@acme-prod.identity.miloapis.com")
		got, source := activeProjectOrEmpty(ctx)
		if got != "acme-prod" || source != projectSourceServiceAccount {
			t.Fatalf("expected the service account's project, got %q (source %q)", got, source)
		}
	})

	t.Run("DATUM_PROJECT wins over a service account's project", func(t *testing.T) {
		t.Setenv("DATUM_TOKEN", "sa-token")
		calls := stubIdentity(t, "robot@acme-prod.identity.miloapis.com")
		t.Setenv("DATUM_PROJECT", "env-project")
		got, source := activeProjectOrEmpty(ctx)
		if got != "env-project" || source != projectSourceEnv {
			t.Fatalf("expected DATUM_PROJECT to win, got %q (source %q)", got, source)
		}
		if *calls != 0 {
			t.Errorf("expected no identity lookup once DATUM_PROJECT is set, got %d", *calls)
		}
	})

	t.Run("falls back to the active project once set", func(t *testing.T) {
		if err := project.SetActive("active-project"); err != nil {
			t.Fatalf("SetActive returned error: %v", err)
		}
		t.Setenv("DATUM_TOKEN", "sa-token")
		stubIdentity(t, "robot@acme-prod.identity.miloapis.com")
		got, source := activeProjectOrEmpty(ctx)
		if got != "active-project" || source != projectSourceStored {
			t.Errorf("expected the stored active project to win over the service account's, got %q (source %q)", got, source)
		}
	})

	t.Run("DATUM_PROJECT wins over the active project", func(t *testing.T) {
		t.Setenv("DATUM_PROJECT", "env-project")
		got, err := resolveProjectName(ctx, "")
		if err != nil || got != "env-project" {
			t.Errorf("expected DATUM_PROJECT to win, got %q err=%v", got, err)
		}
	})
}

func TestServiceAccountProjectCachesPerToken(t *testing.T) {
	ctx := context.Background()
	t.Setenv("DATUM_TOKEN", "sa-token")
	calls := stubIdentity(t, "robot@acme-prod.identity.miloapis.com")
	for i := 0; i < 3; i++ {
		if got := serviceAccountProject(ctx); got != "acme-prod" {
			t.Fatalf("expected acme-prod, got %q", got)
		}
	}
	if *calls != 1 {
		t.Errorf("expected one identity lookup for one token, got %d", *calls)
	}
	t.Setenv("DATUM_TOKEN", "rotated-token")
	serviceAccountProject(ctx)
	if *calls != 2 {
		t.Errorf("expected a new token to be looked up again, got %d lookups", *calls)
	}
}

func TestServiceAccountProjectDoesNotCacheFailures(t *testing.T) {
	ctx := context.Background()
	t.Setenv("DATUM_TOKEN", "sa-token")
	stubIdentity(t, "")
	fail := true
	whoAmI = func(context.Context) (api.Identity, error) {
		if fail {
			return api.Identity{}, errors.New("transient")
		}
		return api.Identity{Username: "robot@acme-prod.identity.miloapis.com"}, nil
	}
	if got := serviceAccountProject(ctx); got != "" {
		t.Fatalf("expected no project while the lookup fails, got %q", got)
	}
	fail = false
	if got := serviceAccountProject(ctx); got != "acme-prod" {
		t.Errorf("expected a retry after a failed lookup to succeed, got %q", got)
	}
}

func TestServiceAccountProjectNeverLogsIn(t *testing.T) {
	t.Setenv("DATUM_TOKEN", "")
	calls := stubIdentity(t, "robot@acme-prod.identity.miloapis.com")
	if got := serviceAccountProject(context.Background()); got != "" {
		t.Errorf("expected no project without a token, got %q", got)
	}
	if *calls != 0 {
		t.Errorf("expected no identity lookup without a token, got %d", *calls)
	}
}

func TestCurrentUserID(t *testing.T) {
	ctx := context.Background()

	t.Run("DATUM_USER_ID wins without a lookup", func(t *testing.T) {
		t.Setenv("DATUM_TOKEN", "user-token")
		t.Setenv("DATUM_USER_ID", "env-user")
		calls := stubIdentity(t, "someone@example.com")
		if got, err := currentUserID(ctx); err != nil || got != "env-user" {
			t.Fatalf("expected DATUM_USER_ID, got %q err=%v", got, err)
		}
		if *calls != 0 {
			t.Errorf("expected no identity lookup, got %d", *calls)
		}
	})

	t.Run("DATUM_TOKEN alone uses the token's own user", func(t *testing.T) {
		t.Setenv("DATUM_TOKEN", "user-token")
		t.Setenv("DATUM_USER_ID", "")
		stubIdentity(t, "someone@example.com")
		if got, err := currentUserID(ctx); err != nil || got != "uid-for-tests" {
			t.Fatalf("expected the API-reported uid, got %q err=%v", got, err)
		}
	})

	t.Run("a failed lookup names DATUM_USER_ID", func(t *testing.T) {
		t.Setenv("DATUM_TOKEN", "user-token")
		t.Setenv("DATUM_USER_ID", "")
		stubIdentity(t, "")
		whoAmI = func(context.Context) (api.Identity, error) { return api.Identity{}, errors.New("boom") }
		_, err := currentUserID(ctx)
		if err == nil || !strings.Contains(err.Error(), "DATUM_USER_ID") {
			t.Errorf("expected an error naming DATUM_USER_ID, got %v", err)
		}
	})

	t.Run("without DATUM_TOKEN it reads the stored login, never the API", func(t *testing.T) {
		t.Setenv("DATUM_TOKEN", "")
		t.Setenv("DATUM_USER_ID", "")
		calls := stubIdentity(t, "someone@example.com")
		if _, err := currentUserID(ctx); err == nil {
			t.Errorf("expected an error with no stored login")
		}
		if *calls != 0 {
			t.Errorf("expected no identity lookup, got %d", *calls)
		}
	})
}

func TestDiscoveryErrBreaksTheListingLoop(t *testing.T) {
	ctx := context.Background()
	forbidden := apierrors.NewForbidden(schema.GroupResource{Group: "resourcemanager.miloapis.com", Resource: "organizations"}, "", errors.New("nope"))

	t.Run("forbidden points at naming the project", func(t *testing.T) {
		t.Setenv("DATUM_TOKEN", "")
		_, out, _ := discoveryErr(ctx, forbidden)
		te := out.(ToolError)
		if te.SuggestedAction == nil || te.SuggestedAction.Tool != "projects" || te.SuggestedAction.Action != "set" {
			t.Errorf("expected a suggestion of projects/set, got %+v", te.SuggestedAction)
		}
		if !strings.Contains(te.Error, "DATUM_PROJECT") || !strings.Contains(te.Error, "'project'") {
			t.Errorf("expected the error to name the explicit-project options, got %q", te.Error)
		}
	})

	t.Run("a service account is told its project is already in use", func(t *testing.T) {
		t.Setenv("DATUM_TOKEN", "sa-token")
		stubIdentity(t, "robot@acme-prod.identity.miloapis.com")
		_, out, _ := discoveryErr(ctx, forbidden)
		te := out.(ToolError)
		if !strings.Contains(te.Error, `"acme-prod"`) {
			t.Errorf("expected the error to name the service account's project, got %q", te.Error)
		}
		if te.SuggestedAction == nil || te.SuggestedAction.Tool != "projects" || te.SuggestedAction.Action != "get" {
			t.Errorf("expected a suggestion of projects/get, got %+v", te.SuggestedAction)
		}
	})

	t.Run("other errors pass through unsuggested", func(t *testing.T) {
		t.Setenv("DATUM_TOKEN", "")
		_, out, _ := discoveryErr(ctx, errors.New("boom"))
		if te := out.(ToolError); te.SuggestedAction != nil || te.Error != "boom" {
			t.Errorf("expected the error unchanged, got %+v", te)
		}
	})
}

// TestResolveOrgName exercises override > DATUM_ORG env > active-org
// precedence as ordered subtests.
func TestResolveOrgName(t *testing.T) {
	t.Run("override wins", func(t *testing.T) {
		got, err := resolveOrgName("override-org")
		if err != nil || got != "override-org" {
			t.Fatalf("expected override to win, got %q err=%v", got, err)
		}
	})

	t.Run("errors when nothing is set", func(t *testing.T) {
		_, err := resolveOrgName("")
		if err == nil {
			t.Fatalf("expected an error when no organization is active")
		}
		if !strings.Contains(err.Error(), "no active organization set") {
			t.Errorf("expected a descriptive error, got %q", err.Error())
		}
		// activeOrgOrEmpty (used by the 'context' tool) shares the same
		// underlying state and must agree with resolveOrgName, minus the error.
		if got := activeOrgOrEmpty(); got != "" {
			t.Errorf("expected activeOrgOrEmpty to agree (empty), got %q", got)
		}
	})

	t.Run("DATUM_ORG env wins over active org", func(t *testing.T) {
		if err := org.SetActive("active-org"); err != nil {
			t.Fatalf("SetActive returned error: %v", err)
		}
		t.Setenv("DATUM_ORG", "env-org")
		got, err := resolveOrgName("")
		if err != nil || got != "env-org" {
			t.Fatalf("expected DATUM_ORG env to win over active org, got %q err=%v", got, err)
		}
		if got := activeOrgOrEmpty(); got != "env-org" {
			t.Errorf("expected activeOrgOrEmpty to agree, got %q", got)
		}
	})

	t.Run("falls back to active org once env is unset", func(t *testing.T) {
		t.Setenv("DATUM_ORG", "")
		got, err := resolveOrgName("")
		if err != nil || got != "active-org" {
			t.Fatalf("expected active org fallback, got %q err=%v", got, err)
		}
	})
}

func TestCrudResourceActionsAndAnnotations(t *testing.T) {
	rw := crudResource{Tool: "domains", Group: "networking.datumapis.com", Kind: "Domain", Namespace: "default"}
	if rw.actions() != "list|get|create|update|delete" {
		t.Errorf("expected full action set, got %q", rw.actions())
	}
	ann := rw.annotations()
	if ann.ReadOnlyHint {
		t.Errorf("expected ReadOnlyHint false for a writable resource")
	}
	if ann.DestructiveHint == nil || !*ann.DestructiveHint {
		t.Errorf("expected DestructiveHint true for a writable resource")
	}

	ro := crudResource{Tool: "dnszoneclasses", Group: "dns.networking.miloapis.com", Kind: "DNSZoneClass", Actions: []Action{ActionList, ActionGet}}
	if ro.actions() != "list|get" {
		t.Errorf("expected read-only action set, got %q", ro.actions())
	}
	ann = ro.annotations()
	if !ann.ReadOnlyHint {
		t.Errorf("expected ReadOnlyHint true for a read-only resource")
	}
	if ann.DestructiveHint == nil || *ann.DestructiveHint {
		t.Errorf("expected DestructiveHint false for a read-only resource")
	}

	suggestion := ro.suggestValidAction()
	if suggestion.Tool != "dnszoneclasses" || suggestion.Action != "list|get" {
		t.Errorf("unexpected suggestValidAction result: %+v", suggestion)
	}
}

func TestListParamsToAPIOptions(t *testing.T) {
	p := ListParams{Limit: 25, Continue: "tok", LabelSelector: "a=b", FieldSelector: "metadata.name=foo"}
	got := p.toAPIOptions()
	if got.Limit != 25 || got.Continue != "tok" || got.LabelSelector != "a=b" || got.FieldSelector != "metadata.name=foo" {
		t.Errorf("expected a direct field-for-field conversion, got %+v", got)
	}
}

func TestWithDryRunNote(t *testing.T) {
	notDryRun := withDryRunNote(map[string]any{"deleted": "x"}, false)
	if _, present := notDryRun["dryRun"]; present {
		t.Errorf("expected no dryRun key when DryRun is false, got %+v", notDryRun)
	}

	dryRun := withDryRunNote(map[string]any{"deleted": "x"}, true)
	if dryRun["dryRun"] != true {
		t.Errorf("expected dryRun=true to be tagged on the result, got %+v", dryRun)
	}
}

func TestCrudResourceMixedActionSet(t *testing.T) {
	// e.g. IPAllocation: inspectable and releasable, but never directly created.
	r := crudResource{Tool: "ipallocations", Actions: []Action{ActionList, ActionGet, ActionDelete}}
	if r.actions() != "list|get|delete" {
		t.Errorf("expected list|get|delete, got %q", r.actions())
	}
	if r.allows(ActionCreate) || r.allows(ActionUpdate) {
		t.Errorf("expected create/update to be disallowed")
	}
	if !r.allows(ActionList) || !r.allows(ActionGet) || !r.allows(ActionDelete) {
		t.Errorf("expected list/get/delete to be allowed")
	}
	if r.readOnly() {
		t.Errorf("expected readOnly() false: this resource can still delete")
	}
	ann := r.annotations()
	if ann.ReadOnlyHint {
		t.Errorf("expected ReadOnlyHint false (it can delete)")
	}
	if ann.DestructiveHint == nil || !*ann.DestructiveHint {
		t.Errorf("expected DestructiveHint true (it can delete)")
	}
}

func TestParseToolsets(t *testing.T) {
	if got := parseToolsets(""); got != nil {
		t.Errorf("expected nil when unset, got %v", got)
	}
	if got := parseToolsets("core"); got == nil || len(got) != 0 {
		t.Errorf("expected an empty, non-nil set for 'core' alone, got %v", got)
	}

	got := parseToolsets(" Billing , iam ,core")
	if !got["billing"] || !got["iam"] {
		t.Errorf("expected billing and iam (case-insensitive, trimmed), got %v", got)
	}
	if got["core"] {
		t.Errorf("expected 'core' to never be listed (it isn't optional), got %v", got)
	}
	if len(got) != 2 {
		t.Errorf("expected exactly 2 toolsets, got %v", got)
	}
}

func TestCrudResourceDescriptionMentionsPrerequisitesAndNote(t *testing.T) {
	r := crudResource{
		Tool: "trafficprotectionpolicies", Group: "networking.datumapis.com", Kind: "TrafficProtectionPolicy", Namespace: "default",
		Note: "Policies target either a Gateway or HTTPRoute via spec.targetRefs.",
	}
	d := r.description()
	for _, want := range []string{"active project", "projects", r.Note} {
		if !strings.Contains(d, want) {
			t.Errorf("expected description to mention %q, got: %s", want, d)
		}
	}
}
