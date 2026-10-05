package server

import (
	"context"
	"net"
	"strconv"
	"testing"
	"time"
)

// freePort finds a currently-unused TCP port on localhost.
func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to find a free port: %v", err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func TestRunHTTPShutsDownGracefullyOnContextCancel(t *testing.T) {
	addr := "127.0.0.1:" + strconv.Itoa(freePort(t))
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() { done <- RunHTTP(ctx, addr) }()

	// Give the listener a moment to come up before we ask it to stop.
	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("expected a clean shutdown, got error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("RunHTTP did not return within 5s of context cancellation")
	}
}
