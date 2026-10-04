package api

import (
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/labels"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func applyList(t *testing.T, opts []ctrlclient.ListOption) ctrlclient.ListOptions {
	t.Helper()
	var lo ctrlclient.ListOptions
	for _, o := range opts {
		o.ApplyToList(&lo)
	}
	return lo
}

func TestListOptionsNormalizedLimit(t *testing.T) {
	cases := []struct {
		name  string
		limit int64
		want  int64
	}{
		{"unset defaults", 0, DefaultListLimit},
		{"negative defaults", -5, DefaultListLimit},
		{"within range preserved", 42, 42},
		{"exactly max preserved", MaxListLimit, MaxListLimit},
		{"over max clamped", MaxListLimit + 1000, MaxListLimit},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ListOptions{Limit: c.limit}.normalizedLimit()
			if got != c.want {
				t.Errorf("limit=%d: want %d, got %d", c.limit, c.want, got)
			}
		})
	}
}

func TestListOptionsToListOptsDefaults(t *testing.T) {
	opts, err := ListOptions{}.toListOpts()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	lo := applyList(t, opts)
	if lo.Limit != DefaultListLimit {
		t.Errorf("expected default limit %d, got %d", DefaultListLimit, lo.Limit)
	}
	if lo.Continue != "" {
		t.Errorf("expected no continue token, got %q", lo.Continue)
	}
	if lo.LabelSelector != nil {
		t.Errorf("expected no label selector, got %v", lo.LabelSelector)
	}
	if lo.FieldSelector != nil {
		t.Errorf("expected no field selector, got %v", lo.FieldSelector)
	}
}

func TestListOptionsToListOptsWithValues(t *testing.T) {
	opts, err := ListOptions{
		Limit:         10,
		Continue:      "abc123",
		LabelSelector: "team=edge,env!=prod",
		FieldSelector: "metadata.name=foo",
	}.toListOpts()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	lo := applyList(t, opts)
	if lo.Limit != 10 {
		t.Errorf("expected limit 10, got %d", lo.Limit)
	}
	if lo.Continue != "abc123" {
		t.Errorf("expected continue token preserved, got %q", lo.Continue)
	}
	if lo.LabelSelector == nil || !lo.LabelSelector.Matches(labels.Set{"team": "edge", "env": "staging"}) {
		t.Errorf("expected label selector to match team=edge,env!=prod, got %v", lo.LabelSelector)
	}
	if lo.LabelSelector.Matches(labels.Set{"team": "edge", "env": "prod"}) {
		t.Errorf("expected label selector to exclude env=prod")
	}
	if lo.FieldSelector == nil {
		t.Errorf("expected field selector to be set")
	}
}

func TestListOptionsToListOptsInvalidSelectors(t *testing.T) {
	if _, err := (ListOptions{LabelSelector: "not a valid selector!!"}).toListOpts(); err == nil {
		t.Errorf("expected an error for an invalid labelSelector")
	}
	if _, err := (ListOptions{FieldSelector: "==="}).toListOpts(); err == nil {
		t.Errorf("expected an error for an invalid fieldSelector")
	}
}

func TestWriteOptionsCreateUpdateOpts(t *testing.T) {
	if got := (WriteOptions{}).createOpts(); got != nil {
		t.Errorf("expected no create options when DryRun is false, got %v", got)
	}
	if got := (WriteOptions{DryRun: true}).createOpts(); len(got) != 1 {
		t.Errorf("expected exactly one create option when DryRun is true, got %v", got)
	}
	if got := (WriteOptions{}).updateOpts(); got != nil {
		t.Errorf("expected no update options when DryRun is false, got %v", got)
	}
	if got := (WriteOptions{DryRun: true}).updateOpts(); len(got) != 1 {
		t.Errorf("expected exactly one update option when DryRun is true, got %v", got)
	}
}

func TestWriteOptionsDeleteOpts(t *testing.T) {
	if got := (WriteOptions{}).deleteOpts(); len(got) != 0 {
		t.Errorf("expected no delete options by default, got %v", got)
	}

	opts := (WriteOptions{DryRun: true, ExpectedResourceVersion: "42"}).deleteOpts()
	if len(opts) != 2 {
		t.Fatalf("expected dry-run + precondition delete options, got %d: %v", len(opts), opts)
	}
	var do ctrlclient.DeleteOptions
	for _, o := range opts {
		o.ApplyToDelete(&do)
	}
	if do.Preconditions == nil || do.Preconditions.ResourceVersion == nil || *do.Preconditions.ResourceVersion != "42" {
		t.Errorf("expected ResourceVersion precondition '42', got %+v", do.Preconditions)
	}
}

func TestConflictErrorMessage(t *testing.T) {
	err := &ConflictError{Kind: "Domain", Name: "example-com"}
	msg := err.Error()
	if msg == "" {
		t.Fatalf("expected a non-empty error message")
	}
	for _, want := range []string{"Domain", "example-com", "action=get"} {
		if !strings.Contains(msg, want) {
			t.Errorf("expected error message to mention %q, got: %s", want, msg)
		}
	}
}
