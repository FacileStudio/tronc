package middleware

import (
	"bytes"
	"encoding/json"
	stderrors "errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(new(bytes.Buffer), nil))
}

func TestRedactQuery(t *testing.T) {
	cases := map[string]string{
		"":                          "",
		"page=2&limit=50":           "page=2&limit=50",
		"token=abc123":              "token=%5Bredacted%5D",
		"TOKEN=abc123":              "TOKEN=%5Bredacted%5D",
		"code=oidc-secret&state=xy": "code=%5Bredacted%5D&state=xy",
		"%zz":                       "[unparsable]",
	}

	for input, want := range cases {
		if got := RedactQuery(input); got != want {
			t.Errorf("RedactQuery(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestRedactQueryNeverLeaksASecret(t *testing.T) {
	for _, key := range SensitiveQueryKeys {
		raw := url.Values{key: {"s3cr3t"}, "page": {"1"}}.Encode()
		if strings.Contains(RedactQuery(raw), "s3cr3t") {
			t.Errorf("%s survived redaction: %s", key, RedactQuery(raw))
		}
	}
}

func TestRequestLoggerRedactsAndRecordsStatus(t *testing.T) {
	var buffer bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buffer, nil))

	handler := RequestLogger(logger)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte("nope"))
	}))

	request := httptest.NewRequest(http.MethodGet, "/documents?token=leaked&page=1", nil)
	request.Header.Set("User-Agent", "tronc-test")
	request.Header.Set("X-Forwarded-For", "203.0.113.7, 10.0.0.1")
	handler.ServeHTTP(httptest.NewRecorder(), request)

	if strings.Contains(buffer.String(), "leaked") {
		t.Fatalf("token reached the log: %s", buffer.String())
	}

	var record map[string]any
	if err := json.Unmarshal(buffer.Bytes(), &record); err != nil {
		t.Fatalf("log line is not JSON: %v", err)
	}
	if record["status"] != float64(http.StatusTeapot) {
		t.Errorf("status = %v, want 418", record["status"])
	}
	if record["bytes"] != float64(4) {
		t.Errorf("bytes = %v, want 4", record["bytes"])
	}
	if record["client_ip"] != "203.0.113.7" {
		t.Errorf("client_ip = %v, want the first forwarded hop", record["client_ip"])
	}
	if record["user_agent"] != "tronc-test" {
		t.Errorf("user_agent = %v", record["user_agent"])
	}
}

func TestCORSPanicsOnWildcardWithCredentials(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("wildcard origin with credentials was accepted")
		}
	}()
	CORS(CORSConfig{AllowedOrigins: []string{"*"}, AllowCredentials: true})
}

func TestCORSAllowsOnlyListedOrigins(t *testing.T) {
	handler := CORS(CORSConfig{
		AllowedOrigins:   []string{"https://sablier.facile.studio"},
		AllowCredentials: true,
	})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	allowed := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodOptions, "/", nil)
	request.Header.Set("Origin", "https://sablier.facile.studio")
	handler.ServeHTTP(allowed, request)

	if allowed.Code != http.StatusNoContent {
		t.Errorf("preflight status = %d, want 204", allowed.Code)
	}
	if got := allowed.Header().Get("Access-Control-Allow-Origin"); got != "https://sablier.facile.studio" {
		t.Errorf("allow-origin = %q", got)
	}
	if got := allowed.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Errorf("allow-credentials = %q", got)
	}

	denied := httptest.NewRecorder()
	evil := httptest.NewRequest(http.MethodOptions, "/", nil)
	evil.Header.Set("Origin", "https://evil.example")
	handler.ServeHTTP(denied, evil)

	if denied.Code != http.StatusForbidden {
		t.Errorf("unlisted preflight status = %d, want 403", denied.Code)
	}
	if denied.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Error("an unlisted origin was echoed back")
	}
}

func TestCORSWithNoOriginsDeniesEveryone(t *testing.T) {
	handler := CORS(CORSConfig{})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("Origin", "http://localhost:5173")
	handler.ServeHTTP(recorder, request)

	if recorder.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Error("an empty origin list still granted access")
	}
}

func TestRecovererAnswersWithTheEnvelope(t *testing.T) {
	handler := Recoverer(discardLogger())(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		panic("boom")
	}))

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))

	if recorder.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", recorder.Code)
	}

	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("panic response is not JSON: %v (%s)", err, recorder.Body.String())
	}
	if body.Error.Code != "internal" {
		t.Errorf("code = %q, want internal", body.Error.Code)
	}
}

func TestRecovererRepanicsAbortHandler(t *testing.T) {
	defer func() {
		recovered, ok := recover().(error)
		if !ok || !stderrors.Is(recovered, http.ErrAbortHandler) {
			t.Error("ErrAbortHandler was swallowed")
		}
	}()

	handler := Recoverer(discardLogger())(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		panic(http.ErrAbortHandler)
	}))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
}
