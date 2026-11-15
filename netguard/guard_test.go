package netguard

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"sync/atomic"
	"testing"
	"time"
)

func codeOf(err error) string {
	if b, ok := AsBlocked(err); ok {
		return b.Code
	}
	if err != nil {
		return "other"
	}
	return ""
}

func TestResolveRefusesAnyInternalAnswer(t *testing.T) {
	g := testGuard()
	cases := map[string]string{
		"public.test":  "",
		"mixed.test":   CodeInternal,
		"private.test": CodeInternal,
		"meta.test":    CodeInternal,
		"nowhere.test": CodeUnresolved,
		"glouton-db":   CodeURL,
	}
	for host, want := range cases {
		_, err := g.Resolve(context.Background(), host)
		if got := codeOf(err); got != want {
			t.Errorf("%s: got %q, want %q", host, got, want)
		}
	}
}

func TestControlChecksTheConnectedAddress(t *testing.T) {
	for _, a := range []string{"127.0.0.1:80", "[::1]:443", "10.0.0.1:6379", "169.254.169.254:80", "junk"} {
		if Control("tcp", a, nil) == nil {
			t.Errorf("%s should be refused", a)
		}
	}
	if err := Control("tcp4", fakePublic+":443", nil); err != nil {
		t.Errorf("public address refused: %v", err)
	}
}

func TestDefaultDialerRefusesLoopback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer srv.Close()
	_, err := (&Guard{}).DialContext(context.Background(), "tcp", srv.Listener.Addr().String())
	if codeOf(err) != CodeInternal {
		t.Fatalf("dialer reached loopback: %v", err)
	}
	if _, err := guardedDialer.Dial("tcp", srv.Listener.Addr().String()); err == nil {
		t.Fatal("the Control hook let a plain dial reach loopback")
	}
}

type flippingResolver struct{ calls atomic.Int32 }

func (f *flippingResolver) LookupNetIP(context.Context, string, string) ([]netip.Addr, error) {
	if f.calls.Add(1) == 1 {
		return []netip.Addr{netip.MustParseAddr(fakePublic)}, nil
	}
	return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
}

func TestRebindingAnswerIsNeverUsed(t *testing.T) {
	resolver := &flippingResolver{}
	var dialled string
	g := &Guard{Resolver: resolver, Dial: func(_ context.Context, _, address string) (net.Conn, error) {
		dialled = address
		return nil, io.EOF
	}}
	_, _ = g.DialContext(context.Background(), "tcp", "rebind.test:80")
	if dialled != fakePublic+":80" || resolver.calls.Load() != 1 {
		t.Fatalf("dialled %q after %d lookups: the checked address must be the one connected to", dialled, resolver.calls.Load())
	}
}

func secretServer(t *testing.T) string {
	t.Helper()
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	_, port, _ := net.SplitHostPort(srv.Listener.Addr().String())
	mux.HandleFunc("/ok", func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "public page") })
	mux.HandleFunc("/secret", func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "TOPSECRET") })
	mux.HandleFunc("/away", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://public.test:"+port+"/ok", http.StatusFound)
	})
	mux.HandleFunc("/to-loopback", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://127.0.0.1:"+port+"/secret", http.StatusFound)
	})
	mux.HandleFunc("/to-private-name", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://private.test:"+port+"/secret", http.StatusFound)
	})
	return port
}

func TestClientFollowsNoRedirect(t *testing.T) {
	port := secretServer(t)
	client := testGuard().Client(5 * time.Second)
	response, err := client.Get("http://public.test:" + port + "/ok")
	if err != nil || response.StatusCode != http.StatusOK {
		t.Fatalf("a public page through the guard: %v %v", response, err)
	}
	_ = response.Body.Close()
	if response, err := client.Get("http://public.test:" + port + "/away"); err == nil {
		_ = response.Body.Close()
		t.Fatal("the client followed a redirect")
	}
	if _, err := client.Get("http://private.test:" + port + "/ok"); codeOf(err) != CodeInternal {
		t.Fatalf("a private name was reached: %v", err)
	}
}

func TestClientFollowingRechecksEveryHop(t *testing.T) {
	port := secretServer(t)
	client := testGuard().ClientFollowing(5 * time.Second)
	for _, path := range []string{"/to-loopback", "/to-private-name"} {
		response, err := client.Get("http://public.test:" + port + path)
		if err == nil {
			body, _ := io.ReadAll(response.Body)
			_ = response.Body.Close()
			t.Fatalf("%s followed: %q", path, body)
		}
		if code := codeOf(err); code == "" || code == "other" {
			t.Fatalf("%s: want a guard refusal, got %v", path, err)
		}
	}
	response, err := client.Get("http://public.test:" + port + "/away")
	if err != nil {
		t.Fatalf("a redirect to a public page was refused: %v", err)
	}
	_ = response.Body.Close()
}
