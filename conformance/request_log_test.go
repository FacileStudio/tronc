package conformance_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/FacileStudio/tronc/middleware"
)

type requestLogInput struct {
	URL        string            `json:"url"`
	RemoteAddr string            `json:"remote_addr"`
	APIPrefix  *string           `json:"api_prefix"`
	Status     int               `json:"status"`
	Headers    map[string]string `json:"headers,omitempty"`
}

type requestLogWant struct {
	Path     string `json:"path"`
	Query    string `json:"query"`
	Kind     string `json:"kind"`
	Level    string `json:"level"`
	ClientIP string `json:"client_ip"`
}

func requestLogVectors(t *testing.T) []vector {
	t.Helper()
	root := ""
	inputs := []struct {
		name  string
		input requestLogInput
	}{
		{"no_query", requestLogInput{URL: "/api/sheets"}},
		{"plain_query_kept_verbatim", requestLogInput{URL: "/api/sheets?sort=name&page=2"}},
		{"token_redacted", requestLogInput{URL: "/api/sheets?token=s3cr3t"}},
		{"key_match_ignores_case", requestLogInput{URL: "/api/sheets?Access_Token=s3cr3t"}},
		{"every_sensitive_key", requestLogInput{URL: "/api/x?access_token=1&api_key=2&apikey=3&code=4&id_token=5&key=6&password=7&refresh_token=8&secret=9&signature=10&token=11"}},
		{"redaction_reencodes_sorted", requestLogInput{URL: "/api/auth/callback?state=xyz&code=abc&b=2"}},
		{"repeated_sensitive_key_collapsed", requestLogInput{URL: "/api/x?token=a&token=b"}},
		{"sensitive_key_without_value", requestLogInput{URL: "/api/x?token"}},
		{"similar_key_not_redacted", requestLogInput{URL: "/api/x?tokens=a&my_token=b"}},
		{"unparsable_query", requestLogInput{URL: "/api/x?token=%zz"}},
		{"semicolon_query_unparsable", requestLogInput{URL: "/api/x?a=1;token=b"}},
		{"health_is_quiet", requestLogInput{URL: "/health"}},
		{"api_ready_is_quiet", requestLogInput{URL: "/api/ready?token=s3cr3t"}},
		{"static_is_quiet", requestLogInput{URL: "/_app/immutable/entry.js"}},
		{"prefix_needs_a_boundary", requestLogInput{URL: "/apix/sheets"}},
		{"bare_prefix_is_api", requestLogInput{URL: "/api"}},
		{"server_error_logged_at_error", requestLogInput{URL: "/_app/immutable/entry.js", Status: 502}},
		{"client_error_stays_info", requestLogInput{URL: "/api/sheets", Status: 404}},
		{"root_api_prefix", requestLogInput{URL: "/sheets", APIPrefix: &root}},
		{"root_api_prefix_health", requestLogInput{URL: "/ready", APIPrefix: &root}},
		{"client_ip_cloudflare_header_first", requestLogInput{URL: "/api/x", Headers: map[string]string{"Cf-Connecting-Ip": "198.51.100.7", "X-Forwarded-For": "203.0.113.1"}}},
		{"client_ip_first_forwarded_hop", requestLogInput{URL: "/api/x", Headers: map[string]string{"X-Forwarded-For": " 203.0.113.1 , 10.0.0.2"}}},
	}

	cases := make([]vector, 0, len(inputs))
	for _, entry := range inputs {
		entry.input.RemoteAddr = "192.0.2.1:1234"
		if entry.input.Status == 0 {
			entry.input.Status = http.StatusOK
		}
		cases = append(cases, vector{entry.name, entry.input, logRequest(t, entry.input)})
	}
	return cases
}

func logRequest(t *testing.T, input requestLogInput) requestLogWant {
	t.Helper()
	var buffer bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buffer, &slog.HandlerOptions{Level: slog.LevelDebug}))
	handler := middleware.RequestLoggerWith(logger, middleware.RequestLoggerConfig{APIPrefix: input.APIPrefix})(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(input.Status) }))
	request := httptest.NewRequest("GET", input.URL, nil)
	request.RemoteAddr = input.RemoteAddr
	for name, value := range input.Headers {
		request.Header.Set(name, value)
	}
	handler.ServeHTTP(httptest.NewRecorder(), request)

	var record struct {
		Level    string `json:"level"`
		Kind     string `json:"kind"`
		Path     string `json:"path"`
		Query    string `json:"query"`
		ClientIP string `json:"client_ip"`
	}
	if err := json.Unmarshal(buffer.Bytes(), &record); err != nil {
		t.Fatalf("request log record: %v: %s", err, buffer.String())
	}
	return requestLogWant{record.Path, record.Query, record.Kind, record.Level, record.ClientIP}
}
