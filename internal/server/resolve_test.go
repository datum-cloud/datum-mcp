package server

import (
	"strings"
	"testing"

	"github.com/datum-cloud/datum-mcp/internal/keyring"
	"github.com/datum-cloud/datum-mcp/internal/org"
	"github.com/datum-cloud/datum-mcp/internal/project"
)

func TestMain(m *testing.M) {
	keyring.MockInit()
	m.Run()
}

// TestResolveProjectName exercises the full override > active-project
// precedence as ordered subtests, since the underlying keyring mock is
// process-global and has no per-test reset.
func TestResolveProjectName(t *testing.T) {
	t.Run("override wins even before anything is active", func(t *testing.T) {
		got, err := resolveProjectName("explicit-project")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "explicit-project" {
			t.Errorf("expected override to win, got %q", got)
		}
	})

	t.Run("errors when nothing is active and no override given", func(t *testing.T) {
		_, err := resolveProjectName("")
		if err == nil {
			t.Fatalf("expected an error when no project is active")
		}
		if !strings.Contains(err.Error(), "no active project set") {
			t.Errorf("expected a descriptive error, got %q", err.Error())
		}
	})

	t.Run("falls back to the active project once set", func(t *testing.T) {
		if err := project.SetActive("active-project"); err != nil {
			t.Fatalf("SetActive returned error: %v", err)
		}
		got, err := resolveProjectName("")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "active-project" {
			t.Errorf("expected active project, got %q", got)
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

func TestDisabledToolsets(t *testing.T) {
	t.Setenv("DATUM_MCP_DISABLE_TOOLSETS", "")
	if got := disabledToolsets(); len(got) != 0 {
		t.Errorf("expected empty set when unset, got %v", got)
	}

	t.Setenv("DATUM_MCP_DISABLE_TOOLSETS", " Billing , iam ,core")
	got := disabledToolsets()
	if !got["billing"] || !got["iam"] {
		t.Errorf("expected billing and iam disabled (case-insensitive, trimmed), got %v", got)
	}
	if got["core"] {
		t.Errorf("expected 'core' to never be disablable, got %v", got)
	}
	if len(got) != 2 {
		t.Errorf("expected exactly 2 disabled toolsets, got %v", got)
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
