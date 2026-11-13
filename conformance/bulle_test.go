package conformance_test

import (
	"context"
	stderrors "errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/FacileStudio/tronc/bulle"
)

type bulleAnswer struct {
	Status   int    `json:"status"`
	Body     string `json:"body,omitempty"`
	Location string `json:"location,omitempty"`
}

type bulleErrorWant struct {
	Kind       string `json:"kind"`
	Status     int    `json:"status,omitempty"`
	Code       string `json:"code,omitempty"`
	Message    string `json:"message,omitempty"`
	UpgradeURL string `json:"upgrade_url,omitempty"`
	Key        string `json:"key,omitempty"`
	Limit      int64  `json:"limit,omitempty"`
}

// bulleServer answers every request with answer.
func bulleServer(t *testing.T, answer bulleAnswer) *bulle.Client {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if answer.Location != "" {
			w.Header().Set("Location", answer.Location)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(answer.Status)
		_, _ = io.WriteString(w, answer.Body)
	}))
	t.Cleanup(server.Close)
	client, err := bulle.New(bulle.Config{URL: server.URL, TokenSource: func(context.Context) (string, error) { return "tok", nil }})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func bulleErrorVectors(t *testing.T) []vector {
	t.Helper()
	inputs := []struct {
		name  string
		input bulleAnswer
	}{
		{"ok", bulleAnswer{Status: 200, Body: `{"text":"hi","model":"m"}`}},
		{"payment_required_envelope", bulleAnswer{Status: 402, Body: `{"error":{"code":"payment_required","message":"No AI","upgrade_url":"https://sso.example/spaces","key":"ai.access"}}`}},
		{"payment_required_bare", bulleAnswer{Status: 402, Body: `{"code":"payment_required","message":"No AI","upgrade_url":"/billing","key":"ai.access"}`}},
		{"payment_required_with_limit", bulleAnswer{Status: 402, Body: `{"error":{"code":"payment_required","message":"Plan limit","key":"rooms","limit":3}}`}},
		{"payment_required_string_error", bulleAnswer{Status: 402, Body: `{"error":"No AI"}`}},
		{"payment_required_javascript_dropped", bulleAnswer{Status: 402, Body: `{"error":{"message":"No AI","upgrade_url":"javascript:alert(1)"}}`}},
		{"payment_required_http_dropped", bulleAnswer{Status: 402, Body: `{"error":{"message":"No AI","upgrade_url":"http://sso.example/spaces"}}`}},
		{"payment_required_protocol_relative_dropped", bulleAnswer{Status: 402, Body: `{"error":{"message":"No AI","upgrade_url":"//evil.example"}}`}},
		{"payment_required_unreadable_body", bulleAnswer{Status: 402, Body: `<html>`}},
		{"bad_request", bulleAnswer{Status: 400, Body: `{"error":{"code":"invalid_argument","message":"prompt is required"}}`}},
		{"unauthorized", bulleAnswer{Status: 401, Body: `{"error":{"code":"unauthenticated","message":"missing auth"}}`}},
		{"forbidden", bulleAnswer{Status: 403, Body: `{"error":{"code":"permission_denied","message":"an exchanged token is required"}}`}},
		{"conflict", bulleAnswer{Status: 409, Body: `{"error":{"code":"already_exists","message":"a batch is running"}}`}},
		{"daily_cap", bulleAnswer{Status: 429, Body: `{"error":{"code":"rate_limited","message":"at most 200 completions per day"}}`}},
		{"plain_text_429", bulleAnswer{Status: 429, Body: `Too Many Requests`}},
		{"server_error_unreachable", bulleAnswer{Status: 500, Body: `{"error":{"code":"internal","message":"boom"}}`}},
		{"unavailable_unreachable", bulleAnswer{Status: 503, Body: `{"error":{"code":"unavailable","message":"the model did not answer"}}`}},
		{"redirect_refused", bulleAnswer{Status: 307, Location: "https://elsewhere.example/api/ai/complete"}},
		{"ok_unreadable_body", bulleAnswer{Status: 200, Body: `not json`}},
	}
	cases := make([]vector, 0, len(inputs))
	for _, entry := range inputs {
		_, err := bulleServer(t, entry.input).Complete(context.Background(), bulle.CompleteRequest{Prompt: "x"})
		cases = append(cases, vector{entry.name, entry.input, classifyBulle(err)})
	}
	return cases
}

func classifyBulle(err error) bulleErrorWant {
	var plan *bulle.PlanRequired
	var refused *bulle.StatusError
	switch {
	case err == nil:
		return bulleErrorWant{Kind: "ok"}
	case stderrors.As(err, &plan):
		return bulleErrorWant{Kind: "plan_required", Message: plan.Message, UpgradeURL: plan.UpgradeURL, Key: plan.Key, Limit: plan.Limit}
	case stderrors.As(err, &refused):
		return bulleErrorWant{Kind: "status", Status: refused.Status, Code: refused.Code, Message: refused.Message}
	case stderrors.Is(err, bulle.ErrUnreachable):
		return bulleErrorWant{Kind: "unreachable"}
	}
	return bulleErrorWant{Kind: "other"}
}

type bullePathInput struct {
	Segments []string          `json:"segments"`
	Query    map[string]string `json:"query,omitempty"`
}

type bullePathWant struct {
	Path  string `json:"path,omitempty"`
	Query string `json:"query,omitempty"`
	Error string `json:"error,omitempty"`
}

func bullePathVectors(t *testing.T) []vector {
	t.Helper()
	inputs := []struct {
		name  string
		input bullePathInput
	}{
		{"plain", bullePathInput{Segments: []string{"ideas", "i1", "run"}}},
		{"slash_stays_in_segment", bullePathInput{Segments: []string{"ideas", "a/b", "run"}}},
		{"dot_dot_inside_segment", bullePathInput{Segments: []string{"ideas", "a/../b?x=1", "run"}}},
		{"space_and_unicode", bullePathInput{Segments: []string{"runs", "r 1é"}}},
		{"percent", bullePathInput{Segments: []string{"runs", "50%"}}},
		{"query_encoded", bullePathInput{Segments: []string{"ideas"}, Query: map[string]string{"app": "agenda", "space_id": "space 1&x"}}},
		{"empty_segment_refused", bullePathInput{Segments: []string{"ideas", ""}}},
		{"dot_segment_refused", bullePathInput{Segments: []string{"ideas", "."}}},
		{"dot_dot_segment_refused", bullePathInput{Segments: []string{"..", "admin"}}},
		{"long_segment_refused", bullePathInput{Segments: []string{"ideas", strings.Repeat("a", 201)}}},
	}
	cases := make([]vector, 0, len(inputs))
	for _, entry := range inputs {
		cases = append(cases, vector{entry.name, entry.input, bullePath(t, entry.input)})
	}
	return cases
}

func bullePath(t *testing.T, in bullePathInput) bullePathWant {
	t.Helper()
	var got bullePathWant
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = bullePathWant{Path: r.URL.EscapedPath(), Query: r.URL.RawQuery}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	client, err := bulle.New(bulle.Config{URL: server.URL, TokenSource: func(context.Context) (string, error) { return "tok", nil }})
	if err != nil {
		t.Fatal(err)
	}
	query := url.Values{}
	for key, value := range in.Query {
		query.Set(key, value)
	}
	if err := client.Call(context.Background(), bulle.Request{Method: http.MethodGet, Segments: in.Segments, Query: query}, nil); err != nil {
		if stderrors.Is(err, bulle.ErrBadSegment) {
			return bullePathWant{Error: "bad_segment"}
		}
		t.Fatal(err)
	}
	return got
}
