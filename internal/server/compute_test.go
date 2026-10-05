package server

import "testing"

func TestComputeResourcesRegistered(t *testing.T) {
	cases := []struct {
		tool    string
		kind    string
		actions string
	}{
		{"workloads", "Workload", "list|get|create|update|delete"},
		{"instances", "Instance", "list|get"},
	}
	for _, c := range cases {
		t.Run(c.tool, func(t *testing.T) {
			r := findCrudResource(t, c.tool)
			if r.Group != "compute.datumapis.com" || r.Kind != c.kind || r.Namespace != "default" {
				t.Errorf("unexpected shape: %+v", r)
			}
			if r.actions() != c.actions {
				t.Errorf("expected actions %q, got %q", c.actions, r.actions())
			}
			if r.Toolset != "compute" {
				t.Errorf("expected Toolset 'compute', got %q", r.Toolset)
			}
		})
	}
}
