package healthcheck

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func serveHealth(t *testing.T, status int) string {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/health" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(status)
	}))
	server.Listener.Close()
	server.Listener = listener
	server.Start()
	t.Cleanup(server.Close)

	_, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatalf("split: %v", err)
	}
	return port
}

func TestProbeAcceptsHealthy(t *testing.T) {
	if err := Probe(serveHealth(t, http.StatusOK)); err != nil {
		t.Errorf("healthy probe failed: %v", err)
	}
}

func TestProbeRejectsUnhealthy(t *testing.T) {
	err := Probe(serveHealth(t, http.StatusServiceUnavailable))
	if err == nil {
		t.Fatal("a 503 was reported healthy")
	}
	if !strings.Contains(err.Error(), "503") {
		t.Errorf("error does not name the status: %v", err)
	}
}

func TestProbeFailsWhenNothingListens(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	_, port, _ := net.SplitHostPort(listener.Addr().String())
	listener.Close()

	if err := Probe(port); err == nil {
		t.Error("a closed port was reported healthy")
	}
}

func TestHandleIgnoresANormalStart(t *testing.T) {
	for _, args := range [][]string{{"/app"}, {"/app", "serve"}, {}} {
		if Handle(args) {
			t.Errorf("Handle(%v) claimed the probe", args)
		}
	}
}
