package server

import "testing"

func findCrudResource(t *testing.T, tool string) crudResource {
	t.Helper()
	for _, r := range crudResources {
		if r.Tool == tool {
			return r
		}
	}
	t.Fatalf("no crudResource registered for tool %q", tool)
	return crudResource{}
}

func TestIPAMResourcesRegistered(t *testing.T) {
	cases := []struct {
		tool      string
		group     string
		kind      string
		namespace string
		actions   string
	}{
		{"ipclasses", "ipam.miloapis.com", "IPClass", "", "list|get"},
		{"ippools", "ipam.miloapis.com", "IPPool", "", "list|get|create|update|delete"},
		{"ipclaims", "ipam.miloapis.com", "IPClaim", "default", "list|get|create|update|delete"},
		{"ipallocations", "ipam.miloapis.com", "IPAllocation", "default", "list|get|delete"},
	}
	for _, c := range cases {
		t.Run(c.tool, func(t *testing.T) {
			r := findCrudResource(t, c.tool)
			if r.Group != c.group || r.Kind != c.kind || r.Namespace != c.namespace {
				t.Errorf("unexpected shape: %+v", r)
			}
			if r.actions() != c.actions {
				t.Errorf("expected actions %q, got %q", c.actions, r.actions())
			}
			if r.Toolset != "ipam" {
				t.Errorf("expected Toolset 'ipam', got %q", r.Toolset)
			}
		})
	}
}

func TestIPAMToolsetCanBeDisabled(t *testing.T) {
	t.Setenv("DATUM_MCP_DISABLE_TOOLSETS", "ipam")
	disabled := disabledToolsets()
	for _, tool := range []string{"ipclasses", "ippools", "ipclaims", "ipallocations"} {
		r := findCrudResource(t, tool)
		if !(r.Toolset != "" && disabled[r.Toolset]) {
			t.Errorf("expected %q to be gated off when ipam is disabled", tool)
		}
	}
	// A core tool must never be gate-able.
	r := findCrudResource(t, "domains")
	if r.Toolset != "" && disabled[r.Toolset] {
		t.Errorf("expected 'domains' (core) to remain enabled")
	}
}
