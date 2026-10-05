package server

import "testing"

func TestVPCResourcesRegistered(t *testing.T) {
	cases := []struct {
		tool string
		kind string
	}{
		{"networks", "Network"},
		{"subnets", "Subnet"},
		{"connectors", "Connector"},
	}
	for _, c := range cases {
		t.Run(c.tool, func(t *testing.T) {
			r := findCrudResource(t, c.tool)
			if r.Group != "networking.datumapis.com" || r.Kind != c.kind || r.Namespace != "default" {
				t.Errorf("unexpected shape: %+v", r)
			}
			if r.actions() != "list|get|create|update|delete" {
				t.Errorf("expected full action set, got %q", r.actions())
			}
			if r.Toolset != "vpc" {
				t.Errorf("expected Toolset 'vpc', got %q", r.Toolset)
			}
		})
	}
}
