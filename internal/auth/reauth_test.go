package auth

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestReauthenticateDedupesConcurrentCalls(t *testing.T) {
	var calls int32
	release := make(chan struct{})
	do := func(ctx context.Context) (string, error) {
		atomic.AddInt32(&calls, 1)
		<-release
		return "tok", nil
	}

	const n = 5
	results := make([]string, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			tok, err := reauthenticate(context.Background(), do)
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			results[i] = tok
		}(i)
	}

	// Give every goroutine a chance to arrive and queue behind the first
	// in-flight call before letting it (and therefore all of them) proceed.
	time.Sleep(50 * time.Millisecond)
	close(release)
	wg.Wait()

	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("expected exactly 1 underlying call across %d concurrent callers, got %d", n, got)
	}
	for i, r := range results {
		if r != "tok" {
			t.Errorf("result %d: expected %q, got %q", i, "tok", r)
		}
	}
}

func TestReauthenticateRunsAgainAfterPreviousCallCompletes(t *testing.T) {
	var calls int32
	do := func(ctx context.Context) (string, error) {
		atomic.AddInt32(&calls, 1)
		return "tok", nil
	}

	if _, err := reauthenticate(context.Background(), do); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := reauthenticate(context.Background(), do); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Errorf("expected 2 sequential (non-overlapping) calls to each run do, got %d", got)
	}
}

func TestReauthenticatePropagatesError(t *testing.T) {
	wantErr := context.DeadlineExceeded
	do := func(ctx context.Context) (string, error) {
		return "", wantErr
	}

	tok, err := reauthenticate(context.Background(), do)
	if err != wantErr {
		t.Errorf("expected error %v, got %v", wantErr, err)
	}
	if tok != "" {
		t.Errorf("expected empty token on error, got %q", tok)
	}
}
