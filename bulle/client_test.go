package bulle_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/FacileStudio/tronc/bulle"
)

type seen struct {
	Method   string
	RawPath  string
	Query    string
	Auth     string
	Body     string
	Requests int
}

type fakeBulle struct {
	t      *testing.T
	server *httptest.Server
	mu     sync.Mutex
	last   seen
	answer func(w http.ResponseWriter, r *http.Request)
}

func newFakeBulle(t *testing.T, answer func(w http.ResponseWriter, r *http.Request)) *fakeBulle {
	t.Helper()
	f := &fakeBulle{t: t, answer: answer}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.last = seen{
			Method: r.Method, RawPath: r.URL.EscapedPath(), Query: r.URL.RawQuery,
			Auth: r.Header.Get("Authorization"), Body: string(body), Requests: f.last.Requests + 1,
		}
		f.mu.Unlock()
		f.answer(w, r)
	}))
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakeBulle) seen() seen {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.last
}

func writeJSON(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, body)
}

func fixed(status int, body string) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, status, body) }
}

func staticToken(context.Context) (string, error) { return "tok", nil }

func newClient(t *testing.T, f *fakeBulle) *bulle.Client {
	t.Helper()
	client, err := bulle.New(bulle.Config{URL: f.server.URL + "/", TokenSource: staticToken})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func TestIdeasSendsAppSpaceAndToken(t *testing.T) {
	f := newFakeBulle(t, fixed(200, `{"ideas":[{"id":"i1","kind":"task","title":"T","extra":1}],"generating":true,"goals":[]}`))
	list, err := newClient(t, f).Ideas(context.Background(), "agenda", "space 1")
	if err != nil {
		t.Fatal(err)
	}
	got := f.seen()
	if got.Method != "GET" || got.RawPath != "/api/ideas" || got.Query != "app=agenda&space_id=space+1" || got.Auth != "Bearer tok" {
		t.Fatalf("request = %+v", got)
	}
	if len(list.Ideas) != 1 || list.Ideas[0].ID != "i1" || !list.Generating {
		t.Fatalf("list = %+v", list)
	}
}

func TestPaymentRequiredBareAndEnveloped(t *testing.T) {
	for _, body := range []string{
		`{"error":{"code":"payment_required","message":"No AI","upgrade_url":"https://sso.example/spaces","key":"ai.access"}}`,
		`{"code":"payment_required","message":"No AI","upgrade_url":"https://sso.example/spaces","key":"ai.access"}`,
	} {
		f := newFakeBulle(t, fixed(402, body))
		_, err := newClient(t, f).Generate(context.Background(), "opus", "")
		var plan *bulle.PlanRequired
		if !errors.As(err, &plan) || plan.Message != "No AI" || plan.UpgradeURL != "https://sso.example/spaces" || plan.Key != "ai.access" {
			t.Fatalf("body %s: err = %#v", body, err)
		}
	}
	f := newFakeBulle(t, fixed(402, `{"error":{"message":"x","upgrade_url":"javascript:alert(1)"}}`))
	_, err := newClient(t, f).Generate(context.Background(), "opus", "")
	var plan *bulle.PlanRequired
	if !errors.As(err, &plan) || plan.UpgradeURL != "" {
		t.Fatalf("an unsafe upgrade_url must be dropped: %#v", err)
	}
}

func TestStatusesKeepTheirMeaning(t *testing.T) {
	f := newFakeBulle(t, fixed(429, `{"error":{"code":"rate_limited","message":"at most 3"}}`))
	_, err := newClient(t, f).Generate(context.Background(), "opus", "")
	var refused *bulle.StatusError
	if !errors.As(err, &refused) || refused.Status != 429 || refused.Message != "at most 3" {
		t.Fatalf("429: err = %#v", err)
	}
	f = newFakeBulle(t, fixed(502, `oops`))
	if _, err := newClient(t, f).Ideas(context.Background(), "opus", ""); !errors.Is(err, bulle.ErrUnreachable) {
		t.Fatalf("502: err = %v", err)
	}
	slow := newFakeBulle(t, func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(200 * time.Millisecond)
		writeJSON(w, 200, `{}`)
	})
	client, _ := bulle.New(bulle.Config{URL: slow.server.URL, TokenSource: staticToken, HTTPClient: &http.Client{Timeout: 20 * time.Millisecond}})
	if _, err := client.Ideas(context.Background(), "opus", ""); !errors.Is(err, bulle.ErrUnreachable) {
		t.Fatalf("timeout: err = %v", err)
	}
	slow.server.Close()
	if _, err := client.Ideas(context.Background(), "opus", ""); !errors.Is(err, bulle.ErrUnreachable) {
		t.Fatalf("closed: err = %v", err)
	}
}

func TestPathsAreEscapedSegments(t *testing.T) {
	f := newFakeBulle(t, fixed(202, `{"run_id":"r1"}`))
	client := newClient(t, f)
	if _, err := client.Run(context.Background(), "a/../b?x=1", "s"); err != nil {
		t.Fatal(err)
	}
	if got := f.seen(); got.RawPath != "/api/ideas/a%2F..%2Fb%3Fx=1/run" || got.Query != "space_id=s" {
		t.Fatalf("request = %+v", got)
	}
	for _, bad := range []string{"", ".", ".."} {
		if err := client.Decide(context.Background(), bad, "c", true); !errors.Is(err, bulle.ErrBadSegment) {
			t.Fatalf("segment %q: err = %v", bad, err)
		}
	}
	if f.seen().Requests != 1 {
		t.Fatal("a bad segment must not reach Bulle")
	}
}

func TestUnauthorizedForgetsAndRetriesOnce(t *testing.T) {
	f := newFakeBulle(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "Bearer old" {
			writeJSON(w, 401, `{}`)
			return
		}
		writeJSON(w, 200, `{"ideas":[]}`)
	})
	token := "old"
	client, _ := bulle.New(bulle.Config{
		URL:         f.server.URL,
		TokenSource: func(context.Context) (string, error) { return token, nil },
		Forget:      func(context.Context) { token = "new" },
	})
	if _, err := client.Ideas(context.Background(), "opus", ""); err != nil {
		t.Fatal(err)
	}
	if f.seen().Requests != 2 {
		t.Fatalf("requests = %d, want 2", f.seen().Requests)
	}
}

func TestRunStatusDerivesStateAndLinks(t *testing.T) {
	f := newFakeBulle(t, fixed(200, `{"id":"r1","session_id":"s 1","status":"waiting",
		"outputs":[{"url":"javascript:alert(1)"},{"url":"https://opus.example/t/1"}],
		"pending_approvals":[{"call_id":"c1","tool":"opus_create_task","args":{"title":"x"},"decide_path":"/x"}]}`))
	run, err := newClient(t, f).RunStatus(context.Background(), "r1")
	if err != nil {
		t.Fatal(err)
	}
	if run.State != bulle.StateNeedsApproval || run.ContinueURL != f.server.URL+"/s/s%201" {
		t.Fatalf("run = %+v", run)
	}
	if run.Outputs[0].URL != "" || run.Outputs[1].URL != "https://opus.example/t/1" {
		t.Fatalf("outputs = %+v", run.Outputs)
	}
	encoded, _ := json.Marshal(run)
	if strings.Contains(string(encoded), "decide_path") {
		t.Fatal("decide_path must not reach the app's frontend")
	}
}
