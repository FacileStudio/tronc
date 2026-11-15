package httpx

import (
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestOrDefault(t *testing.T) {
	if orDefault(0, time.Second) != time.Second || orDefault(-1, time.Second) != 0 || orDefault(time.Minute, time.Second) != time.Minute {
		t.Fatal("zero must default, negative must disable, positive must pass through")
	}
}

func TestServeDefaultsAndOverrides(t *testing.T) {
	server := newServer(nil, ServeConfig{WriteTimeout: -1, ReadTimeout: time.Minute})
	if server.WriteTimeout != 0 || server.ReadTimeout != time.Minute {
		t.Fatalf("overrides lost: write=%s read=%s", server.WriteTimeout, server.ReadTimeout)
	}
	if server.ReadHeaderTimeout != DefaultReadHeaderTimeout || server.IdleTimeout != DefaultIdleTimeout {
		t.Fatal("zero fields must take the defaults")
	}
}

func startServe(t *testing.T, handler http.Handler, cfg ServeConfig) (string, context.CancelFunc, chan error) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	cfg.Listener = listener
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, handler, cfg) }()
	t.Cleanup(cancel)
	return "http://" + listener.Addr().String(), cancel, done
}

func TestServeDrainsInFlightRequest(t *testing.T) {
	started := make(chan struct{})
	url, cancel, done := startServe(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		time.Sleep(200 * time.Millisecond)
		_, _ = io.WriteString(w, "finished")
	}), ServeConfig{ShutdownGrace: 5 * time.Second})

	body := make(chan string, 1)
	go func() {
		resp, err := http.Get(url)
		if err != nil {
			body <- err.Error()
			return
		}
		defer resp.Body.Close()
		data, _ := io.ReadAll(resp.Body)
		body <- string(data)
	}()
	<-started
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("clean drain returned %v", err)
	}
	if got := <-body; got != "finished" {
		t.Fatalf("in-flight request was cut: %q", got)
	}
}

func TestServeClosesAfterGrace(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	url, cancel, done := startServe(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		close(started)
		<-release
	}), ServeConfig{ShutdownGrace: 100 * time.Millisecond})

	go func() { _, _ = http.Get(url) }()
	<-started
	cancel()
	if err := <-done; err == nil || !strings.Contains(err.Error(), "shutdown") {
		t.Fatalf("a stuck request must fail the shutdown, got %v", err)
	}
}

func TestServeKeepsStreamingWithoutWriteTimeout(t *testing.T) {
	url, _, _ := startServe(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		rc := http.NewResponseController(w)
		_, _ = io.WriteString(w, "a")
		_ = rc.Flush()
		time.Sleep(300 * time.Millisecond)
		_, _ = io.WriteString(w, "b")
	}), ServeConfig{WriteTimeout: 100 * time.Millisecond})
	if got := fetch(t, url); got == "ab" {
		t.Fatal("control failed: a short WriteTimeout should cut the slow response")
	}

	open, _, _ := startServe(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "a")
		time.Sleep(300 * time.Millisecond)
		_, _ = io.WriteString(w, "b")
	}), ServeConfig{WriteTimeout: -1})
	if got := fetch(t, open); got != "ab" {
		t.Fatalf("WriteTimeout -1 must not cut a slow response, got %q", got)
	}
}

func TestWriteBudgetLiftsServerDeadline(t *testing.T) {
	slow := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(300 * time.Millisecond)
		_, _ = io.WriteString(w, "done")
	})
	url, _, _ := startServe(t, WriteBudget(5*time.Second)(slow), ServeConfig{WriteTimeout: 100 * time.Millisecond})
	if got := fetch(t, url); got != "done" {
		t.Fatalf("WriteBudget must outlast the server WriteTimeout, got %q", got)
	}
}

func fetch(t *testing.T, url string) string {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return string(data)
}
