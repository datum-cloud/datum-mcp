package api

import (
	"bytes"
	"io"
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

func TestAuthRoundTripperRetriesOn401WithBodyPreserved(t *testing.T) {
	t.Setenv("DATUM_TOKEN", "test-token") // makes EnsureAuth/Reauthenticate instant, no keyring/login

	const wantBody = `{"hello":"world"}`
	calls := 0
	var secondCallBody []byte
	rt := &authRoundTripper{next: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			// A real http.Transport drains the request body to send it over
			// the wire, leaving r.Body at EOF - reproduce that here so this
			// test actually exercises the GetBody fix instead of happening
			// to pass because the body was never consumed.
			if r.Body != nil {
				_, _ = io.ReadAll(r.Body)
			}
			rec := httptest.NewRecorder()
			rec.WriteHeader(http.StatusUnauthorized)
			return rec.Result(), nil
		}
		secondCallBody, _ = io.ReadAll(r.Body)
		rec := httptest.NewRecorder()
		rec.WriteHeader(http.StatusOK)
		return rec.Result(), nil
	})}

	// http.NewRequest sets GetBody automatically for a *bytes.Reader body,
	// same as any caller building a JSON request this way.
	req, _ := http.NewRequest(http.MethodPost, "http://example.invalid/apis/foo", bytes.NewReader([]byte(wantBody)))
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected the retry to succeed with 200, got %d", resp.StatusCode)
	}
	if calls != 2 {
		t.Fatalf("expected exactly 2 calls (original + retry), got %d", calls)
	}
	if string(secondCallBody) != wantBody {
		t.Errorf("expected the retried request to carry the original body %q, got %q (empty means r.Clone's shared, already-drained Body was used instead of GetBody)", wantBody, secondCallBody)
	}
}
