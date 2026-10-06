package server

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestErrResultBody(t *testing.T) {
	res, out, err := errResult(errors.New("no active project set"), suggestListProjects())
	if err != nil {
		t.Fatalf("errResult must always return a nil error (see ToolHandlerFor), got %v", err)
	}
	if !res.IsError {
		t.Errorf("expected IsError to be true")
	}
	if len(res.Content) != 1 {
		t.Fatalf("expected exactly one content block, got %d", len(res.Content))
	}

	te, ok := out.(ToolError)
	if !ok {
		t.Fatalf("expected out to be a ToolError, got %T", out)
	}
	if te.Error != "no active project set" {
		t.Errorf("expected error message preserved, got %q", te.Error)
	}
	if te.SuggestedAction == nil || te.SuggestedAction.Tool != "projects" || te.SuggestedAction.Action != "list" {
		t.Errorf("expected suggested_action pointing at projects/list, got %+v", te.SuggestedAction)
	}

	var parsed map[string]any
	b, _ := json.Marshal(out)
	if err := json.Unmarshal(b, &parsed); err != nil {
		t.Fatalf("expected JSON-marshalable output: %v", err)
	}
	if parsed["error"] != "no active project set" {
		t.Errorf("expected JSON body to carry the error message, got %v", parsed)
	}
}

func TestErrResultWithoutSuggestion(t *testing.T) {
	_, out, _ := errResult(errors.New("boom"), nil)
	te := out.(ToolError)
	if te.SuggestedAction != nil {
		t.Errorf("expected nil suggested_action, got %+v", te.SuggestedAction)
	}
}

func TestOkResult(t *testing.T) {
	res, out, err := okResult(map[string]string{"project": "demo"})
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if res.IsError {
		t.Errorf("expected IsError to be false")
	}
	if len(res.Content) != 1 {
		t.Fatalf("expected exactly one content block, got %d", len(res.Content))
	}
	m := out.(map[string]string)
	if m["project"] != "demo" {
		t.Errorf("expected output to round-trip, got %v", out)
	}
}

func TestSuggestHelpers(t *testing.T) {
	if a := suggestListOrgs(); a.Tool != "organizations" || a.Action != "list" {
		t.Errorf("unexpected suggestListOrgs result: %+v", a)
	}
	if a := suggestListProjects(); a.Tool != "projects" || a.Action != "list" {
		t.Errorf("unexpected suggestListProjects result: %+v", a)
	}
	if a := suggestActions("domains", "list|get"); a.Tool != "domains" || a.Action != "list|get" {
		t.Errorf("unexpected suggestActions result: %+v", a)
	}
}
