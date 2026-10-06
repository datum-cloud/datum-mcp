package server

import (
	"context"
	"strings"
	"testing"
)

// The authenticated branch of toolContext makes real API calls (org/project
// membership lookups) and is covered by the manual smoke test against a live
// org, not here. This covers the unauthenticated short-circuit, which must
// never make a network call or attempt the interactive login flow.
func TestToolContextNotAuthenticated(t *testing.T) {
	t.Setenv("DATUM_TOKEN", "")
	_, out, err := toolContext(context.Background(), nil, ContextInput{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	res, ok := out.(ContextResult)
	if !ok {
		t.Fatalf("expected ContextResult, got %T", out)
	}
	if res.Authenticated {
		t.Errorf("expected Authenticated=false with no token available")
	}
	if res.NextStep == "" || !strings.Contains(res.NextStep, "organizations") {
		t.Errorf("expected next_step to point at starting the login flow, got %q", res.NextStep)
	}
	if len(res.Organizations) != 0 || len(res.Projects) != 0 {
		t.Errorf("expected no organizations/projects populated before authentication, got %+v", res)
	}
}

// activeOrgOrEmpty's full precedence is exercised alongside resolveOrgName in
// resolve_test.go's TestResolveOrgName, as ordered subtests over the same
// shared (process-global, keyring-mock-backed) active-org state.
