package api

import (
	"net/http"
	"strings"

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
		// force a new interactive login if refresh token is invalid
		_ = auth.RunLoginFlow(r2.Context(), false)
		if tkn2, err2 := auth.EnsureAuth(r2.Context()); err2 == nil && tkn2 != "" {
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
