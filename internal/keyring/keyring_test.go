package keyring

import (
	"errors"
	"testing"
)

func TestMain(m *testing.M) {
	MockInit()
	m.Run()
}

func TestSetGetRoundTrip(t *testing.T) {
	if err := Set("svc", "user1", "secret-value"); err != nil {
		t.Fatalf("Set returned error: %v", err)
	}
	got, err := Get("svc", "user1")
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if got != "secret-value" {
		t.Errorf("expected %q, got %q", "secret-value", got)
	}
}

func TestGetNotFound(t *testing.T) {
	_, err := Get("svc", "does-not-exist")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestDelete(t *testing.T) {
	if err := Set("svc", "user2", "secret-value"); err != nil {
		t.Fatalf("Set returned error: %v", err)
	}
	if err := Delete("svc", "user2"); err != nil {
		t.Fatalf("Delete returned error: %v", err)
	}
	if _, err := Get("svc", "user2"); !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound after delete, got %v", err)
	}
}
