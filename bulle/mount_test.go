package bulle_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/FacileStudio/tronc/bulle"
	"github.com/FacileStudio/tronc/errors"
	"github.com/go-chi/chi/v5"
)

type app struct {
	router http.Handler
	bulle  *fakeBulle
	ai     bool
	token  error
}

func newApp(t *testing.T, answer func(http.ResponseWriter, *http.Request)) *app {
	t.Helper()
	a := &app{bulle: newFakeBulle(t, answer), ai: true}
	router := chi.NewRouter()
	router.Route("/api", func(r chi.Router) {
		err := bulle.MountIdeas(r, bulle.Options{
			App: "agenda",
			URL: a.bulle.server.URL,
			TokenSource: func(context.Context) (string, error) {
				return "tok", a.token
			},
			Space:      func(r *http.Request) string { return r.Header.Get("X-Space") },
			AIAccess:   func(*http.Request) bool { return a.ai },
			UpgradeURL: "https://sso.example/spaces",
		})
		if err != nil {
			t.Fatal(err)
		}
	})
	a.router = router
	return a
}

func (a *app) do(method, path, body string) (int, map[string]any) {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("X-Space", "team-1")
	recorder := httptest.NewRecorder()
	a.router.ServeHTTP(recorder, request)
	var out map[string]any
	_ = json.Unmarshal(recorder.Body.Bytes(), &out)
	return recorder.Code, out
}

func errorCode(body map[string]any) string {
	inner, _ := body["error"].(map[string]any)
	code, _ := inner["code"].(string)
	return code
}

func TestMountedListScopesToAppAndSpace(t *testing.T) {
	a := newApp(t, fixed(200, `{"ideas":[{"id":"i1"}],"generating":false}`))
	status, body := a.do("GET", "/api/ideas", "")
	if status != 200 || body["ai_access"] != true || len(body["ideas"].([]any)) != 1 {
		t.Fatalf("%d %v", status, body)
	}
	if got := a.bulle.seen(); got.Query != "app=agenda&space_id=team-1" {
		t.Fatalf("query = %q", got.Query)
	}
}

func TestMountedFreePlanNeverCallsBulle(t *testing.T) {
	a := newApp(t, fixed(200, `{}`))
	a.ai = false
	status, body := a.do("GET", "/api/ideas", "")
	if status != 200 || body["ai_access"] != false || body["upgrade_url"] != "https://sso.example/spaces" {
		t.Fatalf("list: %d %v", status, body)
	}
	for _, call := range [][2]string{{"/api/ideas/generate", ""}, {"/api/ideas/i1/run", ""}, {"/api/ideas/runs/r1/approvals/c1", `{"approve":true}`}} {
		status, body = a.do("POST", call[0], call[1])
		if status != 402 || errorCode(body) != "payment_required" {
			t.Fatalf("%s: %d %v", call[0], status, body)
		}
	}
	if a.bulle.seen().Requests != 0 {
		t.Fatal("a free plan must not reach Bulle")
	}
	if status, _ = a.do("POST", "/api/ideas/runs/r1/approvals/c1", `{"approve":false}`); status != 204 {
		t.Fatalf("declining needs no AI access: %d", status)
	}
}

func TestMountedErrorsMapForTheFrontend(t *testing.T) {
	cases := []struct {
		status int
		body   string
		want   int
		code   string
	}{
		{429, `{"error":{"code":"rate_limited","message":"at most 3"}}`, 429, "rate_limited"},
		{409, `{"error":{"code":"already_exists","message":"busy"}}`, 409, "already_exists"},
		{402, `{"error":{"code":"payment_required","message":"no","upgrade_url":"/spaces"}}`, 402, "payment_required"},
		{401, `{}`, 503, "unavailable"},
		{500, `{}`, 503, "unavailable"},
	}
	for _, c := range cases {
		a := newApp(t, fixed(c.status, c.body))
		status, body := a.do("POST", "/api/ideas/generate", "")
		if status != c.want || errorCode(body) != c.code {
			t.Fatalf("bulle %d: got %d %v", c.status, status, body)
		}
	}
	a := newApp(t, fixed(200, `{}`))
	a.token = errors.Failed("Sign in again.")
	if status, body := a.do("GET", "/api/ideas", ""); status != 412 || errorCode(body) != "failed_precondition" {
		t.Fatalf("token error: %d %v", status, body)
	}
}

func TestMountedRunAndDismiss(t *testing.T) {
	a := newApp(t, fixed(202, `{"run_id":"r1","session_id":"s1","idea_id":"i1"}`))
	if status, body := a.do("POST", "/api/ideas/i%2F1/run", ""); status != 202 || body["run_id"] != "r1" {
		t.Fatalf("run: %d %v", status, body)
	}
	if got := a.bulle.seen(); got.RawPath != "/api/ideas/i%2F1/run" || got.Query != "space_id=team-1" {
		t.Fatalf("run request = %+v", got)
	}
	if status, _ := a.do("POST", "/api/ideas/i1/dismiss", ""); status != 204 {
		t.Fatalf("dismiss: %d", status)
	}
	if got := a.bulle.seen(); got.Method != "PATCH" || got.Body != `{"status":"dismissed"}` {
		t.Fatalf("dismiss request = %+v", got)
	}
}
