package server

import "testing"

func TestIAMResourcesRegistered(t *testing.T) {
	cases := []struct {
		tool  string
		group string
		kind  string
	}{
		{"roles", "iam.miloapis.com", "Role"},
		{"policybindings", "iam.miloapis.com", "PolicyBinding"},
	}
	for _, c := range cases {
		t.Run(c.tool, func(t *testing.T) {
			r := findCrudResource(t, c.tool)
			if r.Group != c.group || r.Kind != c.kind || r.Namespace != "default" {
				t.Errorf("unexpected shape: %+v", r)
			}
			if r.actions() != "list|get" {
				t.Errorf("expected read-only, got %q", r.actions())
			}
			if r.Toolset != "iam" {
				t.Errorf("expected Toolset 'iam', got %q", r.Toolset)
			}
		})
	}
}

func TestServicesResourcesRegistered(t *testing.T) {
	services := findCrudResource(t, "services")
	if services.Group != "services.miloapis.com" || services.Kind != "Service" || services.Namespace != "" {
		t.Errorf("unexpected shape: %+v", services)
	}
	if services.actions() != "list|get" {
		t.Errorf("expected read-only, got %q", services.actions())
	}

	entitlements := findCrudResource(t, "serviceentitlements")
	if entitlements.Group != "services.miloapis.com" || entitlements.Kind != "ServiceEntitlement" || entitlements.Namespace != "" {
		t.Errorf("unexpected shape: %+v", entitlements)
	}
	if entitlements.actions() != "list|get|create" {
		t.Errorf("expected list|get|create only (create = 'enable'; no update/delete - that would let an agent remove a project's access to a service), got %q", entitlements.actions())
	}

	for _, tool := range []string{"services", "serviceentitlements"} {
		if r := findCrudResource(t, tool); r.Toolset != "services" {
			t.Errorf("%s: expected Toolset 'services', got %q", tool, r.Toolset)
		}
	}

	// ServiceConsumer (provider-side, system-managed) must not be exposed.
	for _, r := range crudResources {
		if r.Kind == "ServiceConsumer" {
			t.Errorf("ServiceConsumer should not be exposed as a tool; its schema says providers never create these directly")
		}
	}
}

func TestBillingResourcesRegistered(t *testing.T) {
	cases := []string{"billingaccounts", "invoices"}
	for _, tool := range cases {
		t.Run(tool, func(t *testing.T) {
			r := findCrudResource(t, tool)
			if r.Group != "billing.miloapis.com" || r.Namespace != "default" {
				t.Errorf("unexpected shape: %+v", r)
			}
			if r.actions() != "list|get" {
				t.Errorf("expected read-only, got %q", r.actions())
			}
			if r.Toolset != "billing" {
				t.Errorf("expected Toolset 'billing', got %q", r.Toolset)
			}
		})
	}
}
