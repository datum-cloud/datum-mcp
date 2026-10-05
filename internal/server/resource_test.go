package server

import (
	"context"
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
