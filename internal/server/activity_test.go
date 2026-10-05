package server

import (
	"context"
	"testing"
)

func TestActivityQueryKindMapping(t *testing.T) {
	cases := map[string]string{
		"audit":  "AuditLogQuery",
		"events": "EventQuery",
		"feed":   "ActivityQuery",
	}
	for queryType, kind := range cases {
		if got := activityQueryKinds[queryType]; got != kind {
			t.Errorf("queryType %q: expected kind %q, got %q", queryType, kind, got)
		}
	}
	if len(activityQueryKinds) != 3 {
		t.Errorf("expected exactly 3 query types, got %d: %v", len(activityQueryKinds), activityQueryKinds)
	}
}

func TestToolActivityRejectsUnknownQueryType(t *testing.T) {
	_, out, err := toolActivity(context.Background(), nil, ActivityInput{QueryType: "bogus"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	te := out.(ToolError)
	if te.Error == "" {
		t.Fatalf("expected a non-empty error message")
	}
	if te.SuggestedAction == nil || te.SuggestedAction.Tool != "activity" {
		t.Errorf("expected a suggestion naming the activity tool, got %+v", te.SuggestedAction)
	}
}

func TestSearchResourceRegistered(t *testing.T) {
	r := findCrudResource(t, "search")
	if r.Group != "search.miloapis.com" || r.Kind != "ResourceSearchQuery" || r.Namespace != "" {
		t.Errorf("unexpected shape: %+v", r)
	}
	if r.actions() != "list|get|create|update|delete" {
		t.Errorf("expected full action set, got %q", r.actions())
	}
	if r.Toolset != "search" {
		t.Errorf("expected Toolset 'search', got %q", r.Toolset)
	}
}
