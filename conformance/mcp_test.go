package conformance_test

import (
	"context"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/FacileStudio/tronc/mcp"
	"github.com/go-chi/chi/v5"
	"github.com/modelcontextprotocol/go-sdk/auth"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	mcpResource = "https://notes.example/mcp"
	mcpIssuer   = "https://sso.example"
)

type mcpHTTPInput struct {
	Method  string            `json:"method"`
	Path    string            `json:"path"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    string            `json:"body,omitempty"`
}

type mcpHTTPWant struct {
	Status          int    `json:"status"`
	ContentType     string `json:"content_type,omitempty"`
	WWWAuthenticate string `json:"www_authenticate,omitempty"`
	Body            string `json:"body,omitempty"`
}

// mcpToken accepts "user|scope,scope" tokens; anything else is invalid.
func mcpToken(_ context.Context, token string, _ *http.Request) (*auth.TokenInfo, error) {
	user, scopes, ok := strings.Cut(token, "|")
	if !ok || user == "" {
		return nil, auth.ErrInvalidToken
	}
	return &auth.TokenInfo{UserID: user, Scopes: strings.Split(scopes, ","), Expiration: time.Now().Add(time.Hour)}, nil
}

func mcpNoop(context.Context, *sdk.CallToolRequest, struct{}) (*sdk.CallToolResult, struct{}, error) {
	return nil, struct{}{}, nil
}

// mcpRouter mounts a notes server with one read and one write tool, and the
// extra origin https://app.notes.example.
func mcpRouter(t *testing.T, perMinute int) http.Handler {
	t.Helper()
	tools := []mcp.Tool{
		mcp.NewTool(mcp.ToolSpec{Name: "notes_list", Title: "List", Description: "Lists notes.", Annotations: &mcp.Annotations{ReadOnly: true}}, mcpNoop),
		mcp.NewTool(mcp.ToolSpec{Name: "notes_write", Title: "Write", Description: "Writes a note.", Annotations: &mcp.Annotations{Destructive: true}}, mcpNoop),
	}
	app, err := mcp.NewApp(mcp.AppConfig{
		Resource: mcpResource, Issuer: mcpIssuer, Version: "test", Verifier: mcpToken,
		Origins: []string{"https://app.notes.example"}, PerMinute: perMinute, Logger: discardLogger(),
	}, mcp.AppSpec{Name: "notes", Title: "Notes", Tools: tools})
	if err != nil {
		t.Fatal(err)
	}
	router := chi.NewRouter()
	app.Server.Mount(router)
	return router
}

func mcpRequest(handler http.Handler, in mcpHTTPInput) *httptest.ResponseRecorder {
	req := httptest.NewRequest(in.Method, in.Path, strings.NewReader(in.Body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	for name, value := range in.Headers {
		req.Header.Set(name, value)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	return recorder
}

func mcpHTTPVectors(t *testing.T) []vector {
	t.Helper()
	bearer := func(token string) map[string]string { return map[string]string{"Authorization": "Bearer " + token} }
	call := func(tool string) string {
		return `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"` + tool + `","arguments":{}}}`
	}
	inputs := []struct {
		name  string
		input mcpHTTPInput
	}{
		{"no_token_401", mcpHTTPInput{Method: "POST", Path: "/mcp", Body: call("notes_list")}},
		{"invalid_token_401", mcpHTTPInput{Method: "POST", Path: "/mcp", Headers: bearer("garbage"), Body: call("notes_list")}},
		{"missing_read_scope_403", mcpHTTPInput{Method: "POST", Path: "/mcp", Headers: bearer("u1|other.read"), Body: call("notes_list")}},
		{"write_tool_with_read_scope_403", mcpHTTPInput{Method: "POST", Path: "/mcp", Headers: bearer("u1|notes.read"), Body: call("notes_write")}},
		{"write_tool_in_batch_403", mcpHTTPInput{Method: "POST", Path: "/mcp", Headers: bearer("u1|notes.read"),
			Body: `[{"jsonrpc":"2.0","id":1,"method":"tools/list"},` + call("notes_write") + `]`}},
		{"foreign_origin_403", mcpHTTPInput{Method: "POST", Path: "/mcp", Headers: map[string]string{"Origin": "https://evil.example", "Authorization": "Bearer u1|notes.read"}, Body: call("notes_list")}},
		{"foreign_origin_before_token", mcpHTTPInput{Method: "POST", Path: "/mcp", Headers: map[string]string{"Origin": "https://evil.example"}, Body: call("notes_list")}},
		{"get_405", mcpHTTPInput{Method: "GET", Path: "/mcp", Headers: bearer("u1|notes.read")}},
		{"own_origin_get_405", mcpHTTPInput{Method: "GET", Path: "/mcp", Headers: map[string]string{"Origin": "https://notes.example", "Authorization": "Bearer u1|notes.read"}}},
		{"extra_origin_get_405", mcpHTTPInput{Method: "GET", Path: "/mcp", Headers: map[string]string{"Origin": "https://app.notes.example", "Authorization": "Bearer u1|notes.read"}}},
		{"metadata_at_resource_path", mcpHTTPInput{Method: "GET", Path: "/.well-known/oauth-protected-resource/mcp"}},
		{"metadata_at_bare_path", mcpHTTPInput{Method: "GET", Path: "/.well-known/oauth-protected-resource"}},
	}
	handler := mcpRouter(t, 0)
	cases := make([]vector, 0, len(inputs))
	for _, entry := range inputs {
		recorder := mcpRequest(handler, entry.input)
		want := mcpHTTPWant{
			Status: recorder.Code, ContentType: recorder.Header().Get("Content-Type"),
			WWWAuthenticate: strings.Join(recorder.Header().Values("WWW-Authenticate"), ", "),
		}
		if recorder.Code != http.StatusMethodNotAllowed {
			want.Body = recorder.Body.String()
		}
		cases = append(cases, vector{entry.name, entry.input, want})
	}
	return cases
}

type mcpRateInput struct {
	PerMinute int      `json:"per_minute"`
	Users     []string `json:"users"`
	Requests  int      `json:"requests_each"`
}

type mcpRateWant struct {
	Allowed     map[string]int `json:"allowed"`
	Status      int            `json:"refused_status"`
	ContentType string         `json:"refused_content_type"`
	Body        string         `json:"refused_body"`
	RetryAfter  bool           `json:"retry_after"`
}

// mcpRateLimitVectors pins the per-user bucket against the real clock, so
// every case must hold however slowly a port replays it. The default case
// only spends the burst, which a refill can never refuse; the refusals come
// from the small limits, whose next token is 20 seconds or more away.
func mcpRateLimitVectors(t *testing.T) []vector {
	t.Helper()
	inputs := []struct {
		name  string
		input mcpRateInput
	}{
		{"default_burst_admits_120_at_once", mcpRateInput{PerMinute: 0, Users: []string{"u1"}, Requests: mcp.DefaultPerMinute}},
		{"burst_is_per_minute", mcpRateInput{PerMinute: 3, Users: []string{"u1"}, Requests: 5}},
		{"one_bucket_per_user", mcpRateInput{PerMinute: 2, Users: []string{"u1", "u2"}, Requests: 3}},
	}
	cases := make([]vector, 0, len(inputs))
	for _, entry := range inputs {
		handler := mcpRouter(t, entry.input.PerMinute)
		want := mcpRateWant{Allowed: map[string]int{}}
		for _, user := range entry.input.Users {
			spendBucket(handler, user, entry.input.Requests, &want)
		}
		cases = append(cases, vector{entry.name, entry.input, want})
	}
	return cases
}

// spendBucket sends requests GETs as user and records how many passed and
// what the refusal looked like.
func spendBucket(handler http.Handler, user string, requests int, want *mcpRateWant) {
	for range requests {
		recorder := mcpRequest(handler, mcpHTTPInput{Method: "GET", Path: "/mcp", Headers: map[string]string{"Authorization": "Bearer " + user + "|notes.read"}})
		if recorder.Code != http.StatusTooManyRequests {
			want.Allowed[user]++
			continue
		}
		want.Status, want.ContentType, want.Body = recorder.Code, recorder.Header().Get("Content-Type"), recorder.Body.String()
		want.RetryAfter = recorder.Header().Get("Retry-After") != ""
	}
}

func mcpDigestVectors(t *testing.T) []vector {
	t.Helper()
	inputs := []struct {
		name   string
		fields []string
	}{
		{"none", []string{}},
		{"one_empty", []string{""}},
		{"one", []string{"hello"}},
		{"two", []string{"a", "b"}},
		{"joined_differs", []string{"ab"}},
		{"unicode", []string{"réunion", "été"}},
		{"embedded_nul", []string{"a\x00b"}},
	}
	cases := make([]vector, 0, len(inputs))
	for _, entry := range inputs {
		cases = append(cases, vector{entry.name, map[string][]string{"fields": entry.fields}, map[string]string{"digest": mcp.Digest(entry.fields...)}})
	}
	return cases
}

func mcpStateKeyVectors(t *testing.T) []vector {
	t.Helper()
	inputs := []struct{ name, app, secret string }{
		{"courrier", "courrier", "encryption-key"},
		{"opus", "opus", "s3cret"},
		{"empty_secret", "agenda", ""},
		{"unicode_secret", "plume", "clé secrète"},
	}
	cases := make([]vector, 0, len(inputs))
	for _, entry := range inputs {
		cases = append(cases, vector{entry.name, map[string]string{"app": entry.app, "secret": entry.secret},
			map[string]string{"key_hex": hex.EncodeToString(mcp.StateKey(entry.app, entry.secret))}})
	}
	return cases
}
