package server

import (
	"context"
	"strings"
	"testing"
)

func TestAllowedGroup(t *testing.T) {
	allowed := []string{
		"compute.datumapis.com",
		"networking.datumapis.com",
		"ipam.miloapis.com",
		"resourcemanager.miloapis.com",
		"gateway.networking.k8s.io",
		"gateway.envoyproxy.io",
	}
	for _, g := range allowed {
		if !allowedGroup(g) {
			t.Errorf("expected %q to be allowed", g)
		}
	}

	denied := []string{
		"", // core v1 has an empty group
		"rbac.authorization.k8s.io",
		"admissionregistration.k8s.io",
		"apiregistration.k8s.io",
		"authentication.k8s.io",
		"authorization.k8s.io",
		"coordination.k8s.io",
		"flowcontrol.apiserver.k8s.io",
		"apiextensions.k8s.io",
		"notdatumapis.com.evil.example",
	}
	for _, g := range denied {
		if allowedGroup(g) {
			t.Errorf("expected %q to be denied", g)
		}
	}
}

func TestToolResourceRejectsMissingFields(t *testing.T) {
	_, out, err := toolResource(context.Background(), nil, GenericResourceInput{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	te := out.(ToolError)
	if te.SuggestedAction == nil || te.SuggestedAction.Tool != "apis" {
		t.Errorf("expected a suggestion pointing at the apis tool, got %+v", te.SuggestedAction)
	}
}

func TestSensitiveGroupActionsAreReadOnly(t *testing.T) {
	expected := []string{"iam.miloapis.com", "billing.miloapis.com", "services.miloapis.com"}
	for _, group := range expected {
		actions, ok := sensitiveGroupActions[group]
		if !ok {
			t.Errorf("expected %q to have a sensitiveGroupActions entry", group)
			continue
		}
		r := crudResource{Actions: actions}
		if r.allows(ActionCreate) || r.allows(ActionUpdate) || r.allows(ActionDelete) {
			t.Errorf("group %q: expected resource tool restriction to be read-only, got allowed actions %v", group, actions)
		}
		if !r.allows(ActionList) || !r.allows(ActionGet) {
			t.Errorf("group %q: expected list/get to remain allowed, got %v", group, actions)
		}
	}
	if len(sensitiveGroupActions) != len(expected) {
		t.Errorf("expected exactly %d sensitive groups, got %d: %v", len(expected), len(sensitiveGroupActions), sensitiveGroupActions)
	}
}

func TestToolResourceRejectsDisallowedGroup(t *testing.T) {
	_, out, err := toolResource(context.Background(), nil, GenericResourceInput{
		Group: "rbac.authorization.k8s.io",
		Kind:  "Role",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	te := out.(ToolError)
	if te.Error == "" {
		t.Fatalf("expected a non-empty error message")
	}
	if te.SuggestedAction == nil || te.SuggestedAction.Tool != "apis" {
		t.Errorf("expected a suggestion pointing at the apis tool, got %+v", te.SuggestedAction)
	}
}

// resourceCappedActions is the action set the generic tool will allow for a
// (group, kind), in the same "list|get" form a tool suggests, so a case reads
// at a glance.
func resourceCappedActions(group, kind string) string {
	return crudResource{Actions: genericResourceActions(group, kind)}.actions()
}

func TestResourceCapsActionsToTheDedicatedTool(t *testing.T) {
	cases := []struct {
		group, kind string
		want        string
	}{
		// The dedicated tool is list|get (operator-authored classes), so the
		// generic tool must not offer a write.
		{"ipam.miloapis.com", "IPClass", "list|get"},
		{"dns.networking.miloapis.com", "DNSZoneClass", "list|get"},
		// The dedicated tool keeps 'delete' (releasing a held allocation) but
		// has no create/update.
		{"ipam.miloapis.com", "IPAllocation", "list|get|delete"},
		// projects lists, gets and creates a Project; it has no update/delete.
		{"resourcemanager.miloapis.com", "Project", "list|get|create"},
		// A kind whose dedicated tool is unrestricted stays unrestricted.
		{"ipam.miloapis.com", "IPPool", "list|get|create|update|delete"},
		// The cap does not depend on the case a caller passes.
		{"IPAM.miloapis.com", "ipclass", "list|get"},
	}
	for _, c := range cases {
		if got := resourceCappedActions(c.group, c.kind); got != c.want {
			t.Errorf("%s/%s: expected actions %q, got %q", c.group, c.kind, c.want, got)
		}
	}
}

// The cap runs before auth.EnsureAuth, so a call that asks for an action the
// dedicated tool withholds fails without starting a login. Before the cap
// existed, IPClass delete passed the action check and reached the auth step.
func TestResourceRefusesAnActionTheDedicatedToolWithholds(t *testing.T) {
	_, out, err := toolResource(context.Background(), nil, GenericResourceInput{
		RoutedInput: RoutedInput{Action: ActionDelete},
		Group:       "ipam.miloapis.com",
		Kind:        "IPClass",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	te, ok := out.(ToolError)
	if !ok {
		t.Fatalf("expected a ToolError, got %T: %+v", out, out)
	}
	if !strings.Contains(te.Error, "unsupported resource action") {
		t.Errorf("expected an unsupported-action error, got %q", te.Error)
	}
	if te.SuggestedAction == nil || te.SuggestedAction.Tool != "resource" {
		t.Errorf("expected a suggestion back to the resource tool's own actions, got %+v", te.SuggestedAction)
	}
}

// The per-kind form of the invariant sensitiveGroupActions states per group,
// checked against every dedicated registration: the generic tool offers no
// action the dedicated tool withholds.
func TestResourceIsNeverWiderThanTheDedicatedTool(t *testing.T) {
	seen := map[string]string{}
	for _, r := range crudResources {
		key := kindKey(r.Group, r.Kind)
		if prev, ok := seen[key]; ok {
			t.Errorf("%s is registered by both %q and %q, so the action cap for that kind is ambiguous", key, prev, r.Tool)
		}
		seen[key] = r.Tool
		for _, a := range fullActions {
			if !(crudResource{Actions: genericResourceActions(r.Group, r.Kind)}).allows(a) {
				continue
			}
			if !r.allows(a) {
				t.Errorf("%s/%s: 'resource' allows %q, but the dedicated tool %q does not", r.Group, r.Kind, a, r.Tool)
			}
		}
	}
}
