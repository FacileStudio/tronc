package bulle_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/FacileStudio/tronc/bulle"
	"github.com/go-chi/chi/v5"
)

func TestStartRunSendsTheCallingAppAndThePrompt(t *testing.T) {
	f := newFakeBulle(t, fixed(202, `{"run_id":"r1","session_id":"s1"}`))
	page := &bulle.PageContext{App: "agenda", ID: "week", Title: "Week", URL: "https://agenda.test/calendar"}
	start, err := newClient(t, f).StartRun(context.Background(), "agenda", bulle.StartRunRequest{SpaceID: "sp", Title: "Plan", Prompt: "Plan my week", Page: page})
	if err != nil {
		t.Fatal(err)
	}
	got := f.seen()
	want := `{"app":"agenda","space_id":"sp","title":"Plan","prompt":"Plan my week","page":{"app":"agenda","id":"week","title":"Week","url":"https://agenda.test/calendar"}}`
	if got.Method != "POST" || got.RawPath != "/api/runs" || got.Body != want || got.Auth != "Bearer tok" {
		t.Fatalf("request = %+v", got)
	}
	if start.RunID != "r1" || start.SessionID != "s1" || start.IdeaID != "" {
		t.Fatalf("start = %+v", start)
	}
}

func TestStartRunOmitsWhatIsEmpty(t *testing.T) {
	f := newFakeBulle(t, fixed(202, `{"run_id":"r1","session_id":"s1"}`))
	if _, err := newClient(t, f).StartRun(context.Background(), "agenda", bulle.StartRunRequest{Prompt: "Hi"}); err != nil {
		t.Fatal(err)
	}
	if body := f.seen().Body; body != `{"app":"agenda","prompt":"Hi"}` {
		t.Fatalf("body = %s", body)
	}
}

func TestStartRunWithoutAIAccessIsPlanRequired(t *testing.T) {
	f := newFakeBulle(t, fixed(402, `{"error":{"code":"payment_required","message":"No AI","upgrade_url":"https://sso.example/spaces","key":"ai.access"}}`))
	_, err := newClient(t, f).StartRun(context.Background(), "agenda", bulle.StartRunRequest{Prompt: "Hi"})
	var plan *bulle.PlanRequired
	if !errors.As(err, &plan) || plan.UpgradeURL != "https://sso.example/spaces" {
		t.Fatalf("err = %v, want *PlanRequired", err)
	}
}

func TestProfileReadsTheAssistantAndKeepsOnlyImageLinks(t *testing.T) {
	cases := map[string]string{
		`https://x.test/a.png`:               "https://x.test/a.png",
		`data:image/svg+xml;base64,PHN2Zz4=`: "data:image/svg+xml;base64,PHN2Zz4=",
		`javascript:alert(1)`:                "",
		`data:text/html,<script>`:            "",
		``:                                   "",
	}
	for avatar, want := range cases {
		f := newFakeBulle(t, fixed(200, `{"name":"Nova","avatar_url":"`+strings.ReplaceAll(avatar, `"`, `\"`)+`"}`))
		profile, err := newClient(t, f).Profile(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if got := f.seen(); got.Method != "GET" || got.RawPath != "/api/assistant/profile" {
			t.Fatalf("request = %+v", got)
		}
		if profile.Name != "Nova" || profile.AvatarURL != want {
			t.Errorf("avatar %q: profile = %+v, want avatar %q", avatar, profile, want)
		}
	}
}

func newBulleApp(t *testing.T, answer func(http.ResponseWriter, *http.Request)) *app {
	t.Helper()
	a := &app{bulle: newFakeBulle(t, answer), ai: true}
	router := chi.NewRouter()
	router.Route("/api", func(r chi.Router) {
		err := bulle.MountBulle(r, bulle.Options{
			App:         "agenda",
			URL:         a.bulle.server.URL,
			TokenSource: func(context.Context) (string, error) { return "tok", a.token },
			Space:       func(r *http.Request) string { return r.Header.Get("X-Space") },
			AIAccess:    func(*http.Request) bool { return a.ai },
			UpgradeURL:  "https://sso.example/spaces",
		})
		if err != nil {
			t.Fatal(err)
		}
	})
	a.router = router
	return a
}

func TestMountBulleRejectsABadAppName(t *testing.T) {
	err := bulle.MountBulle(chi.NewRouter(), bulle.Options{App: "Not An App", URL: "https://bulle.test", TokenSource: staticToken})
	if err == nil {
		t.Fatal("a bad app name must be refused")
	}
}

func TestMountedRunStartsAsTheMountedAppInTheActiveSpace(t *testing.T) {
	a := newBulleApp(t, fixed(202, `{"run_id":"r1","session_id":"s1"}`))
	status, body := a.do("POST", "/api/bulle/runs", `{"prompt":"Plan my week","space_id":"other","page":{"app":"agenda","id":"w","title":"W","url":"https://agenda.test/c"}}`)
	if status != 202 || body["run_id"] != "r1" || body["session_id"] != "s1" {
		t.Fatalf("start: %d %v", status, body)
	}
	got := a.bulle.seen()
	want := `{"app":"agenda","space_id":"team-1","prompt":"Plan my week","page":{"app":"agenda","id":"w","title":"W","url":"https://agenda.test/c"}}`
	if got.RawPath != "/api/runs" || got.Body != want {
		t.Fatalf("request = %+v", got)
	}
	if status, body = a.do("POST", "/api/bulle/runs", `{"prompt":"x","app":"opus"}`); status != 400 {
		t.Fatalf("a browser must not name the app: %d %v", status, body)
	}
}

func TestMountedRunOnAFreePlanNeverCallsBulle(t *testing.T) {
	a := newBulleApp(t, fixed(200, `{}`))
	a.ai = false
	status, body := a.do("POST", "/api/bulle/runs", `{"prompt":"Plan"}`)
	if status != 402 || errorCode(body) != "payment_required" {
		t.Fatalf("free run: %d %v", status, body)
	}
	if a.bulle.seen().Requests != 0 {
		t.Fatal("a free plan must not reach Bulle")
	}
}

func TestMountedProfileIsFree(t *testing.T) {
	a := newBulleApp(t, fixed(200, `{"name":"Nova","avatar_url":"https://x.test/a.png"}`))
	a.ai = false
	status, body := a.do("GET", "/api/bulle/profile", "")
	if status != 200 || body["name"] != "Nova" || body["avatar_url"] != "https://x.test/a.png" {
		t.Fatalf("profile: %d %v", status, body)
	}
}

func TestMountedRunFollowsAndDecides(t *testing.T) {
	a := newBulleApp(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			writeJSON(w, 200, `{"id":"r1","session_id":"s1","status":"waiting","pending_approvals":[{"call_id":"c1","tool":"opus_create_task","args":{}}]}`)
			return
		}
		writeJSON(w, 200, `{}`)
	})
	status, body := a.do("GET", "/api/bulle/runs/r1", "")
	if status != 200 || body["state"] != bulle.StateNeedsApproval || body["continue_url"] == "" {
		t.Fatalf("status: %d %v", status, body)
	}
	if status, _ = a.do("POST", "/api/bulle/runs/r1/approvals/c1", `{"approve":true}`); status != 204 {
		t.Fatalf("approve: %d", status)
	}
	if got := a.bulle.seen(); got.RawPath != "/api/runs/r1/approvals/c1" || got.Body != `{"approve":true}` {
		t.Fatalf("decide request = %+v", got)
	}
	a.ai = false
	if status, body = a.do("POST", "/api/bulle/runs/r1/approvals/c1", `{"approve":true}`); status != 402 {
		t.Fatalf("approving on a free plan: %d %v", status, body)
	}
	if status, _ = a.do("POST", "/api/bulle/runs/r1/approvals/c1", `{"approve":false}`); status != 204 {
		t.Fatalf("declining needs no AI access: %d", status)
	}
}
