package org

import (
	"testing"

	"github.com/datum-cloud/datum-mcp/internal/keyring"
)

func TestMain(m *testing.M) {
	keyring.MockInit()
	m.Run()
}

func TestGetActiveDefaultsToEmpty(t *testing.T) {
	got, err := GetActive()
	if err != nil {
		t.Fatalf("GetActive returned error: %v", err)
	}
	if got != "" {
		t.Errorf("expected empty active org before SetActive, got %q", got)
	}
}

func TestSetActiveThenGetActive(t *testing.T) {
	if err := SetActive("my-org"); err != nil {
		t.Fatalf("SetActive returned error: %v", err)
	}
	got, err := GetActive()
	if err != nil {
		t.Fatalf("GetActive returned error: %v", err)
	}
	if got != "my-org" {
		t.Errorf("expected %q, got %q", "my-org", got)
	}
}

func TestSetActiveRejectsEmpty(t *testing.T) {
	if err := SetActive(""); err == nil {
		t.Errorf("expected error when setting an empty organization name")
	}
}
