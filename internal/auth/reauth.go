package auth

import (
	"context"
	"sync"
)

type reauthCall struct {
	done  chan struct{}
	token string
	err   error
}

var (
	reauthMu       sync.Mutex
	reauthInFlight *reauthCall
)

// reauthenticate deduplicates concurrent calls to do: the first caller runs
// do and every other caller that arrives before it finishes waits for and
// shares its result, instead of each running do (and, for EnsureAuth,
// potentially opening its own interactive browser login) independently.
// Exported as a seam so it's testable without a real EnsureAuth call.
func reauthenticate(ctx context.Context, do func(context.Context) (string, error)) (string, error) {
	reauthMu.Lock()
	if reauthInFlight != nil {
		call := reauthInFlight
		reauthMu.Unlock()
		<-call.done
		return call.token, call.err
	}
	call := &reauthCall{done: make(chan struct{})}
	reauthInFlight = call
	reauthMu.Unlock()

	call.token, call.err = do(ctx)

	reauthMu.Lock()
	reauthInFlight = nil
	reauthMu.Unlock()
	close(call.done)

	return call.token, call.err
}

// Reauthenticate is EnsureAuth, deduplicated against concurrent callers (see
// reauthenticate). Intended for a 401 retry path: EnsureAuth already tries a
// silent token refresh before falling back to an interactive login, so this
// never forces a browser login when a refresh would do - and when a refresh
// genuinely isn't enough, concurrent requests share one login attempt
// instead of each opening their own browser window.
func Reauthenticate(ctx context.Context) (string, error) {
	return reauthenticate(ctx, EnsureAuth)
}
