package api

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/datum-cloud/datum-mcp/internal/auth"
)

// prefixRoundTripper injects a base path prefix into all requests.
type prefixRoundTripper struct {
	base string
	next http.RoundTripper
}

func (p *prefixRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	if !strings.HasPrefix(r.URL.Path, p.base) {
		r.URL.Path = strings.TrimRight(p.base, "/") + "/" + strings.TrimLeft(r.URL.Path, "/")
	}
	return p.next.RoundTrip(r)
}

// authRoundTripper injects Authorization using the current token and, on a
// 401, retries once after a fresh interactive login.
type authRoundTripper struct{ next http.RoundTripper }

func (a *authRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	if a.next == nil {
		a.next = http.DefaultTransport
	}
	// initial token via EnsureAuth (may trigger login if missing)
	if tkn, err := auth.EnsureAuth(r.Context()); err == nil && tkn != "" {
		r.Header.Set("Authorization", "Bearer "+tkn)
	}
	resp, err := a.next.RoundTrip(r)
	if err != nil {
		return nil, err
	}
	if shouldRetryWithFreshLogin(resp.StatusCode) {
		// retry once with refreshed token
		_ = resp.Body.Close()
		r2 := r.Clone(r.Context())
		if r.GetBody != nil {
			// r.Clone shares the original Body, which the first RoundTrip
			// above already drained; without a fresh reader, a retried
			// POST/PUT/PATCH would send an empty body.
			if body, err := r.GetBody(); err == nil {
				r2.Body = body
			}
		}
		// Reauthenticate tries a silent token refresh before falling back to
		// an interactive login (see EnsureAuth), and dedupes concurrent
		// callers against a single in-flight attempt instead of each
		// potentially opening its own browser login.
		if tkn2, err2 := auth.Reauthenticate(r2.Context()); err2 == nil && tkn2 != "" {
			r2.Header.Set("Authorization", "Bearer "+tkn2)
			return a.next.RoundTrip(r2)
		}
	}
	return resp, nil
}

// shouldRetryWithFreshLogin reports whether a response status means "this
// token is no good, get a new one." Only 401 Unauthorized qualifies. A 403
// Forbidden means the token was accepted but the request isn't authorized
// for it - the caller is who they say they are, they just lack a
// permission, and logging in again as the same user will never change that.
// Retrying a 403 this way used to open a second interactive browser login
// that then hung forever waiting for a callback nobody would provide in a
// non-interactive context (see #87); the caller should see the 403 and its
// message as-is instead.
func shouldRetryWithFreshLogin(statusCode int) bool {
	return statusCode == http.StatusUnauthorized
}

// withResponseHeaderTimeout bounds how long a single attempt waits to start
// receiving a response, without bounding how long reading the body then
// takes. Unlike http.Client.Timeout (which covers the whole round trip,
// body read included), this lets a slow-but-progressing transfer - a large
// OpenAPI document, a long List page - finish instead of being killed
// partway through, while still catching a truly stalled/dead connection
// that never sends a response at all. rt is the concrete *http.Transport
// client-go hands WrapTransport before any further wrapping; if it isn't
// one (e.g. a caller-supplied custom RoundTripper), this is a no-op.
func withResponseHeaderTimeout(rt http.RoundTripper, d time.Duration) http.RoundTripper {
	if t, ok := rt.(*http.Transport); ok {
		t.ResponseHeaderTimeout = d
	}
	return rt
}

const (
	defaultMaxRetries = 5
	retryBaseDelay    = 500 * time.Millisecond
	retryMaxDelay     = 5 * time.Second
)

// retryRoundTripper retries a request on 429 and 5xx responses with capped
// exponential backoff, honoring a Retry-After header when the server sends
// one. This exists for request paths that don't already get client-go's own
// retry-on-429/5xx (that logic lives in rest.Request, which only the
// ctrlclient.Client path goes through - see NewProjectHTTPClient, used for
// OpenAPI discovery via a raw *http.Client instead).
type retryRoundTripper struct {
	next       http.RoundTripper
	maxRetries int
}

func (rr *retryRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	next := rr.next
	if next == nil {
		next = http.DefaultTransport
	}
	maxRetries := rr.maxRetries
	if maxRetries <= 0 {
		maxRetries = defaultMaxRetries
	}

	for attempt := 0; ; attempt++ {
		reqAttempt := r
		if attempt > 0 {
			reqAttempt = r.Clone(r.Context())
			if r.GetBody != nil {
				if body, err := r.GetBody(); err == nil {
					reqAttempt.Body = body
				}
			}
		}
		resp, err := next.RoundTrip(reqAttempt)
		if err != nil {
			return nil, err
		}
		if attempt >= maxRetries || !isRetryableStatus(resp.StatusCode) {
			return resp, nil
		}
		wait := retryDelay(resp, attempt)
		_ = resp.Body.Close()
		select {
		case <-r.Context().Done():
			return nil, r.Context().Err()
		case <-time.After(wait):
		}
	}
}

func isRetryableStatus(code int) bool {
	return code == http.StatusTooManyRequests || (code >= 500 && code <= 599)
}

// retryDelay returns how long to wait before the next attempt: the server's
// Retry-After header if present (seconds or an HTTP-date), otherwise capped
// exponential backoff.
func retryDelay(resp *http.Response, attempt int) time.Duration {
	if ra := resp.Header.Get("Retry-After"); ra != "" {
		if secs, err := strconv.Atoi(ra); err == nil && secs >= 0 {
			return time.Duration(secs) * time.Second
		}
		if t, err := http.ParseTime(ra); err == nil {
			if d := time.Until(t); d > 0 {
				return d
			}
		}
	}
	d := retryBaseDelay * time.Duration(1<<attempt)
	if d > retryMaxDelay {
		return retryMaxDelay
	}
	return d
}
