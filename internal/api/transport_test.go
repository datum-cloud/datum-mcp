package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestShouldRetryWithFreshLogin(t *testing.T) {
	cases := []struct {
		status int
		want   bool
	}{
		{http.StatusUnauthorized, true},
		{http.StatusForbidden, false}, // the #87 regression: must never trigger a relogin
		{http.StatusOK, false},
		{http.StatusNotFound, false},
		{http.StatusTooManyRequests, false},
		{http.StatusInternalServerError, false},
		{http.StatusBadGateway, false},
	}
	for _, c := range cases {
		if got := shouldRetryWithFreshLogin(c.status); got != c.want {
			t.Errorf("status %d: expected %v, got %v", c.status, c.want, got)
		}
	}
}

// roundTripFunc adapts a function to http.RoundTripper.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestAuthRoundTripperDoesNotRetryOn403(t *testing.T) {
	t.Setenv("DATUM_TOKEN", "test-token") // makes the initial EnsureAuth call instant, no keyring/login

	calls := 0
	rt := &authRoundTripper{next: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		rec := httptest.NewRecorder()
		rec.WriteHeader(http.StatusForbidden)
		return rec.Result(), nil
	})}

	req, _ := http.NewRequest(http.MethodGet, "http://example.invalid/apis/foo", nil)
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("expected the 403 to pass through unchanged, got %d", resp.StatusCode)
	}
	if calls != 1 {
		t.Errorf("expected exactly 1 call to the underlying transport (no retry), got %d", calls)
	}
}
