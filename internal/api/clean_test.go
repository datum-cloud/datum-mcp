package api

import (
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func sampleObject(name string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "networking.datumapis.com/v1alpha",
		"kind":       "Domain",
		"metadata": map[string]any{
			"name":              name,
			"namespace":         "default",
			"uid":               "abc-123",
			"resourceVersion":   "456",
			"generation":        int64(2),
			"creationTimestamp": "2024-01-01T00:00:00Z",
			"managedFields":     []any{map[string]any{"manager": "kubectl"}},
			"labels":            map[string]any{"team": "edge"},
		},
		"spec":   map[string]any{"domainName": "example.com"},
		"status": map[string]any{"ready": true},
	}}
}

func TestCleanObjectStripsInternalMetadata(t *testing.T) {
	cleaned := CleanObject(sampleObject("domain-1"))

	md, ok := cleaned["metadata"].(map[string]any)
	if !ok {
		t.Fatalf("expected metadata map, got %T", cleaned["metadata"])
	}
	for _, f := range internalMetadataFields {
		if _, present := md[f]; present {
			t.Errorf("expected %q to be stripped from metadata, still present: %v", f, md[f])
		}
	}

	if md["name"] != "domain-1" {
		t.Errorf("expected name to be preserved, got %v", md["name"])
	}
	if md["namespace"] != "default" {
		t.Errorf("expected namespace to be preserved, got %v", md["namespace"])
	}
	labels, _ := md["labels"].(map[string]any)
	if labels["team"] != "edge" {
		t.Errorf("expected labels to be preserved, got %v", md["labels"])
	}

	if md["uid"] != "abc-123" {
		t.Errorf("expected uid to be preserved, got %v", md["uid"])
	}
	if md["resourceVersion"] != "456" {
		t.Errorf("expected resourceVersion to be preserved, got %v", md["resourceVersion"])
	}

	spec, _ := cleaned["spec"].(map[string]any)
	if spec["domainName"] != "example.com" {
		t.Errorf("expected spec to be preserved in full, got %v", cleaned["spec"])
	}
	status, _ := cleaned["status"].(map[string]any)
	if status["ready"] != true {
		t.Errorf("expected status to be preserved in full, got %v", cleaned["status"])
	}
	if cleaned["kind"] != "Domain" {
		t.Errorf("expected kind to be preserved, got %v", cleaned["kind"])
	}
}

func TestCleanObjectNil(t *testing.T) {
	if got := CleanObject(nil); got != nil {
		t.Errorf("expected nil for nil input, got %v", got)
	}
}

func TestCleanObjectDoesNotMutateInput(t *testing.T) {
	obj := sampleObject("domain-1")
	_ = CleanObject(obj)

	md := obj.Object["metadata"].(map[string]any)
	if _, present := md["uid"]; !present {
		t.Errorf("CleanObject must not mutate the original object, but 'uid' was removed from the source")
	}
}

func TestCleanList(t *testing.T) {
	list := &unstructured.UnstructuredList{}
	list.Items = []unstructured.Unstructured{*sampleObject("domain-1"), *sampleObject("domain-2")}

	cleaned := CleanList(list)
	if len(cleaned) != 2 {
		t.Fatalf("expected 2 items, got %d", len(cleaned))
	}
	for i, item := range cleaned {
		md := item["metadata"].(map[string]any)
		if _, present := md["resourceVersion"]; !present {
			t.Errorf("item %d: expected resourceVersion to be preserved", i)
		}
		if _, present := md["managedFields"]; present {
			t.Errorf("item %d: expected managedFields stripped", i)
		}
	}
}

func TestCleanListNil(t *testing.T) {
	if got := CleanList(nil); got != nil {
		t.Errorf("expected nil for nil input, got %v", got)
	}
}
