package server

import (
	"context"
	"net"
	"net/http"
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
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() { done <- RunHTTP(ctx, "127.0.0.1", freePort(t), false) }()

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

func TestIsLoopbackHost(t *testing.T) {
	cases := []struct {
		host string
		want bool
	}{
		{"localhost", true},
		{"LOCALHOST", true},
		{"127.0.0.1", true},
		{"127.1.2.3", true},
		{"::1", true},
		{"", false}, // http.Server's "all interfaces" convention
		{"0.0.0.0", false},
		{"::", false},
		{"10.0.0.5", false},
		{"192.168.1.1", false},
		{"example.com", false},
		{"not-an-ip", false},
	}
	for _, c := range cases {
		if got := isLoopbackHost(c.host); got != c.want {
			t.Errorf("isLoopbackHost(%q) = %v, want %v", c.host, got, c.want)
		}
	}
}

func TestRunHTTPRefusesNonLoopbackHostByDefault(t *testing.T) {
	err := RunHTTP(context.Background(), "0.0.0.0", freePort(t), false)
	if err == nil {
		t.Fatal("expected an error refusing the non-loopback host, got nil")
	}
}

func TestRunHTTPAllowsNonLoopbackHostWhenOverridden(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() { done <- RunHTTP(ctx, "0.0.0.0", freePort(t), true) }()

	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("expected a clean shutdown (override should have let it bind), got error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("RunHTTP did not return within 5s of context cancellation")
	}
}

func TestGracefulShutdownForceClosesAfterGracePeriod(t *testing.T) {
	release := make(chan struct{})
	defer close(release)

	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			<-release // simulates a request still in flight when shutdown begins
		}),
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	go srv.Serve(ln)

	client := &http.Client{}
	go func() {
		resp, err := client.Get("http://" + ln.Addr().String() + "/")
		if err == nil {
			resp.Body.Close()
		}
	}()
	time.Sleep(50 * time.Millisecond) // let the request actually reach the blocked handler

	start := time.Now()
	shutdownErr := gracefulShutdown(srv, 100*time.Millisecond)
	elapsed := time.Since(start)

	if shutdownErr != nil {
		t.Errorf("expected nil (force-close should make an in-flight request non-fatal), got %v", shutdownErr)
	}
	if elapsed > 2*time.Second {
		t.Errorf("expected shutdown to return promptly after the grace period, took %v", elapsed)
	}
}
