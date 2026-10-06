package api

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
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

func TestWithResponseHeaderTimeout(t *testing.T) {
	tr := &http.Transport{}
	got := withResponseHeaderTimeout(tr, 7*time.Second)
	if got != http.RoundTripper(tr) {
		t.Errorf("expected the same *http.Transport back, got a different RoundTripper")
	}
	if tr.ResponseHeaderTimeout != 7*time.Second {
		t.Errorf("expected ResponseHeaderTimeout set to 7s, got %v", tr.ResponseHeaderTimeout)
	}

	// A non-*http.Transport RoundTripper (e.g. a caller-supplied custom one)
	// must pass through unchanged rather than panic or be silently dropped.
	custom := roundTripFunc(func(r *http.Request) (*http.Response, error) { return nil, nil })
	if got := withResponseHeaderTimeout(custom, 7*time.Second); got == nil {
		t.Errorf("expected the custom RoundTripper back unchanged, got nil")
	}
}

func TestIsRetryableStatus(t *testing.T) {
	cases := []struct {
		status int
		want   bool
	}{
		{http.StatusOK, false},
		{http.StatusNotFound, false},
		{http.StatusUnauthorized, false}, // authRoundTripper's job, not retryRoundTripper's
		{http.StatusForbidden, false},
		{http.StatusTooManyRequests, true},
		{http.StatusInternalServerError, true},
		{http.StatusBadGateway, true},
		{http.StatusServiceUnavailable, true},
		{http.StatusGatewayTimeout, true},
	}
	for _, c := range cases {
		if got := isRetryableStatus(c.status); got != c.want {
			t.Errorf("status %d: expected %v, got %v", c.status, c.want, got)
		}
	}
}

func TestRetryDelayHonorsRetryAfterSeconds(t *testing.T) {
	resp := &http.Response{Header: http.Header{"Retry-After": []string{"2"}}}
	if got := retryDelay(resp, 0); got != 2*time.Second {
		t.Errorf("expected 2s from Retry-After, got %v", got)
	}
}

func TestRetryDelayFallsBackToExponentialBackoff(t *testing.T) {
	resp := &http.Response{Header: http.Header{}}
	cases := []struct {
		attempt int
		want    time.Duration
	}{
		{0, retryBaseDelay},
		{1, retryBaseDelay * 2},
		{2, retryBaseDelay * 4},
	}
	for _, c := range cases {
		if got := retryDelay(resp, c.attempt); got != c.want {
			t.Errorf("attempt %d: expected %v, got %v", c.attempt, c.want, got)
		}
	}
	// Large enough attempt count must cap at retryMaxDelay, not overflow or
	// grow unbounded.
	if got := retryDelay(resp, 10); got != retryMaxDelay {
		t.Errorf("expected capped at %v, got %v", retryMaxDelay, got)
	}
}

func TestRetryRoundTripperRetriesOnServiceUnavailableThenSucceeds(t *testing.T) {
	calls := 0
	rt := &retryRoundTripper{
		maxRetries: 3,
		next: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			calls++
			rec := httptest.NewRecorder()
			if calls < 3 {
				rec.WriteHeader(http.StatusServiceUnavailable)
			} else {
				rec.WriteHeader(http.StatusOK)
			}
			return rec.Result(), nil
		}),
	}

	req, _ := http.NewRequest(http.MethodGet, "http://example.invalid/foo", nil)
	start := time.Now()
	resp, err := rt.RoundTrip(req)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected eventual 200, got %d", resp.StatusCode)
	}
	if calls != 3 {
		t.Errorf("expected 3 calls (2 failures + 1 success), got %d", calls)
	}
	// Backoff for attempts 0 and 1 (500ms + 1s) with no Retry-After header;
	// just check it actually waited rather than hammering the server.
	if elapsed < retryBaseDelay+2*retryBaseDelay {
		t.Errorf("expected backoff delay between retries, elapsed only %v", elapsed)
	}
}

func TestRetryRoundTripperGivesUpAfterMaxRetries(t *testing.T) {
	calls := 0
	rt := &retryRoundTripper{
		maxRetries: 2,
		next: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			calls++
			rec := httptest.NewRecorder()
			rec.WriteHeader(http.StatusServiceUnavailable)
			return rec.Result(), nil
		}),
	}

	req, _ := http.NewRequest(http.MethodGet, "http://example.invalid/foo", nil)
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("expected the final 503 returned as-is, got %d", resp.StatusCode)
	}
	if calls != 3 { // the initial attempt plus 2 retries
		t.Errorf("expected 3 total calls (1 initial + maxRetries=2), got %d", calls)
	}
}

func TestRetryRoundTripperDoesNotRetryNonRetryableStatus(t *testing.T) {
	calls := 0
	rt := &retryRoundTripper{
		next: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			calls++
			rec := httptest.NewRecorder()
			rec.WriteHeader(http.StatusNotFound)
			return rec.Result(), nil
		}),
	}

	req, _ := http.NewRequest(http.MethodGet, "http://example.invalid/foo", nil)
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404 passed through, got %d", resp.StatusCode)
	}
	if calls != 1 {
		t.Errorf("expected exactly 1 call (no retry on a non-retryable status), got %d", calls)
	}
}

func TestRetryRoundTripperPreservesBodyAcrossRetries(t *testing.T) {
	const wantBody = `{"hello":"world"}`
	calls := 0
	var lastBody []byte
	rt := &retryRoundTripper{
		maxRetries: 2,
		next: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			calls++
			lastBody, _ = io.ReadAll(r.Body)
			rec := httptest.NewRecorder()
			if calls < 2 {
				rec.WriteHeader(http.StatusInternalServerError)
			} else {
				rec.WriteHeader(http.StatusOK)
			}
			return rec.Result(), nil
		}),
	}

	req, _ := http.NewRequest(http.MethodPost, "http://example.invalid/foo", bytes.NewReader([]byte(wantBody)))
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected eventual 200, got %d", resp.StatusCode)
	}
	if string(lastBody) != wantBody {
		t.Errorf("expected the retried request to carry the original body %q, got %q", wantBody, lastBody)
	}
}
