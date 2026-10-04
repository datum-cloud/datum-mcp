package api

import (
	"reflect"
	"testing"
)

func TestTrimToStructureDropsDescriptiveKeys(t *testing.T) {
	schema := map[string]any{
		"type":        "object",
		"description": "A Domain resource.",
		"required":    []any{"domainName"},
		"properties": map[string]any{
			"domainName": map[string]any{
				"type":        "string",
				"description": "The fully qualified domain name.",
				"format":      "hostname",
				"example":     "example.com",
			},
			"tags": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type":        "string",
					"description": "A single tag.",
				},
			},
		},
		"additionalProperties": false,
	}

	trimmed := trimToStructure(schema).(map[string]any)

	if _, present := trimmed["description"]; present {
		t.Errorf("expected top-level description to be dropped")
	}
	if !reflect.DeepEqual(trimmed["required"], []any{"domainName"}) {
		t.Errorf("expected required to be preserved, got %v", trimmed["required"])
	}

	props := trimmed["properties"].(map[string]any)
	domainName := props["domainName"].(map[string]any)
	if _, present := domainName["description"]; present {
		t.Errorf("expected property description to be dropped")
	}
	if _, present := domainName["format"]; present {
		t.Errorf("expected property format to be dropped")
	}
	if _, present := domainName["example"]; present {
		t.Errorf("expected property example to be dropped")
	}
	if domainName["type"] != "string" {
		t.Errorf("expected type to be preserved, got %v", domainName["type"])
	}

	tags := props["tags"].(map[string]any)
	items := tags["items"].(map[string]any)
	if _, present := items["description"]; present {
		t.Errorf("expected nested items description to be dropped")
	}
	if items["type"] != "string" {
		t.Errorf("expected nested items type to be preserved, got %v", items["type"])
	}

	if trimmed["additionalProperties"] != false {
		t.Errorf("expected scalar additionalProperties to be preserved, got %v", trimmed["additionalProperties"])
	}
}

func TestGetIndexComponentRel(t *testing.T) {
	index := map[string]any{
		"paths": map[string]any{
			"apis/networking.datumapis.com/v1alpha": map[string]any{
				"serverRelativeURL": "/openapi/v3/apis/networking.datumapis.com/v1alpha?hash=abc",
			},
			"/apis/dns.networking.miloapis.com/v1alpha1": map[string]any{
				"url": "/openapi/v3/apis/dns.networking.miloapis.com/v1alpha1",
			},
		},
	}

	rel, ok := getIndexComponentRel(index, "apis/networking.datumapis.com/v1alpha")
	if !ok || rel != "/openapi/v3/apis/networking.datumapis.com/v1alpha?hash=abc" {
		t.Errorf("expected serverRelativeURL match, got rel=%q ok=%v", rel, ok)
	}

	rel, ok = getIndexComponentRel(index, "apis/dns.networking.miloapis.com/v1alpha1")
	if !ok || rel != "/openapi/v3/apis/dns.networking.miloapis.com/v1alpha1" {
		t.Errorf("expected url fallback match (with leading slash lookup), got rel=%q ok=%v", rel, ok)
	}

	if _, ok := getIndexComponentRel(index, "apis/does-not-exist/v1"); ok {
		t.Errorf("expected no match for unknown key")
	}
}

func TestResolveGVKsWithoutMapper(t *testing.T) {
	sharedMapperMu.Lock()
	saved := sharedMapper
	sharedMapper = nil
	sharedMapperMu.Unlock()
	t.Cleanup(func() {
		sharedMapperMu.Lock()
		sharedMapper = saved
		sharedMapperMu.Unlock()
	})

	if _, _, err := resolveGVKs("networking.datumapis.com", "Domain"); err == nil {
		t.Errorf("expected error when rest mapper is not initialized")
	}
}
