package httpx

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"

	"github.com/FacileStudio/tronc/middleware"
)

func TestRecovererCoversTheLoggingMiddleware(t *testing.T) {
	var buffer bytes.Buffer
	router := NewRouter(Config{Logger: slog.New(slog.NewJSONHandler(&buffer, nil))})

	router.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
			panic("a middleware below Recoverer exploded")
		})
	})
	router.Get("/", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", recorder.Code)
	}

	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("panic response is not JSON: %v (%s)", err, recorder.Body.String())
	}
	if body.Error.Code != "internal" {
		t.Errorf("code = %q, want internal", body.Error.Code)
	}
	if !bytes.Contains(buffer.Bytes(), []byte("panic recovered")) {
		t.Error("the panic was not logged")
	}
}

func TestRequestIDReachesTheLog(t *testing.T) {
	var buffer bytes.Buffer
	router := NewRouter(Config{Logger: slog.New(slog.NewJSONHandler(&buffer, nil))})
	router.Get("/api/things", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/things", nil))

	var record map[string]any
	if err := json.Unmarshal(buffer.Bytes(), &record); err != nil {
		t.Fatalf("log line is not JSON: %v", err)
	}
	if record["request_id"] == "" || record["request_id"] == nil {
		t.Error("request_id was empty; RequestID must run before the logger")
	}
}

func TestCORSIsSkippedWithoutOrigins(t *testing.T) {
	router := NewRouter(Config{Logger: slog.New(slog.NewJSONHandler(new(bytes.Buffer), nil))})
	router.Get("/", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("Origin", "https://evil.example")
	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", recorder.Code)
	}
	if got := recorder.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("allow-origin = %q with no origins configured", got)
	}
}

func TestCORSIsAppliedWhenConfigured(t *testing.T) {
	router := NewRouter(Config{
		Logger: slog.New(slog.NewJSONHandler(new(bytes.Buffer), nil)),
		CORS: middleware.CORSConfig{
			AllowedOrigins:   []string{"https://app.facile.studio"},
			AllowCredentials: true,
		},
	})
	router.Get("/", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("Origin", "https://app.facile.studio")
	router.ServeHTTP(recorder, request)

	if got := recorder.Header().Get("Access-Control-Allow-Origin"); got != "https://app.facile.studio" {
		t.Errorf("allow-origin = %q", got)
	}
}

func TestChainAppliesTheSameStackToAPlainMux(t *testing.T) {
	var buffer bytes.Buffer
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/things", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("GET /api/boom", func(_ http.ResponseWriter, _ *http.Request) {
		panic("mux handler exploded")
	})

	handler := Chain(Config{Logger: slog.New(slog.NewJSONHandler(&buffer, nil))}, mux)

	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/things", nil))

	var record map[string]any
	if err := json.Unmarshal(buffer.Bytes(), &record); err != nil {
		t.Fatalf("log line is not JSON: %v (%s)", err, buffer.String())
	}
	if record["request_id"] == "" || record["request_id"] == nil {
		t.Error("request_id was empty through Chain")
	}
	if record["kind"] != "api" {
		t.Errorf("kind = %v, want api", record["kind"])
	}

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/boom", nil))
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("panic through Chain = %d, want 500", recorder.Code)
	}

	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil || body.Error.Code != "internal" {
		t.Errorf("panic response was not the envelope: %s", recorder.Body.String())
	}
}

// A router built with a zero-value Config must still refuse to believe a
// stranger's X-Forwarded-For. This is the regression that matters: every app
// in the suite constructs its router this way, and chi's RealIP — which
// NewRouter used to install — rewrote RemoteAddr on every request.
func TestRouterIgnoresForwardedFromAnUntrustedPeer(t *testing.T) {
	var seen string
	router := NewRouter(Config{Logger: slog.New(slog.NewJSONHandler(new(bytes.Buffer), nil))})
	router.Get("/", func(_ http.ResponseWriter, r *http.Request) { seen = r.RemoteAddr })

	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.RemoteAddr = "203.0.113.7:5555"
	request.Header.Set("X-Forwarded-For", "9.9.9.9")
	router.ServeHTTP(httptest.NewRecorder(), request)

	if seen != "203.0.113.7:5555" {
		t.Fatalf("RemoteAddr = %q, want the untouched connection address", seen)
	}
}

func TestRouterBelievesTheConfiguredProxy(t *testing.T) {
	var seen string
	router := NewRouter(Config{Logger: slog.New(slog.NewJSONHandler(new(bytes.Buffer), nil))})
	router.Get("/", func(_ http.ResponseWriter, r *http.Request) { seen = r.RemoteAddr })

	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.RemoteAddr = "10.0.0.3:5555"
	request.Header.Set("X-Forwarded-For", "203.0.113.7")
	router.ServeHTTP(httptest.NewRecorder(), request)

	if seen != "203.0.113.7" {
		t.Fatalf("RemoteAddr = %q, want the forwarded client", seen)
	}
}

// TrustedProxies distinguishes "unset" from "none": a nil slice takes the
// default, a non-nil empty one is an operator saying trust nothing.
func TestRouterHonoursAnEmptyTrustedProxyList(t *testing.T) {
	var seen string
	router := NewRouter(Config{
		Logger:         slog.New(slog.NewJSONHandler(new(bytes.Buffer), nil)),
		TrustedProxies: []netip.Prefix{},
	})
	router.Get("/", func(_ http.ResponseWriter, r *http.Request) { seen = r.RemoteAddr })

	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.RemoteAddr = "10.0.0.3:5555"
	request.Header.Set("X-Forwarded-For", "203.0.113.7")
	router.ServeHTTP(httptest.NewRecorder(), request)

	if seen != "10.0.0.3:5555" {
		t.Fatalf("RemoteAddr = %q, want the connection address", seen)
	}
}

// Vision's routes sit at the root because the proxy in front strips /api, so
// the default prefix classified every API call as static and logged it at the
// quiet level — which is indistinguishable from an app that logs nothing.
func TestAPIPrefixDecidesWhatCountsAsAnAPIRequest(t *testing.T) {
	var buffer bytes.Buffer
	router := NewRouter(Config{
		Logger:    slog.New(slog.NewJSONHandler(&buffer, &slog.HandlerOptions{Level: slog.LevelInfo})),
		APIPrefix: RootAPI,
	})
	router.Get("/auth/me", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })

	router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/auth/me", nil))

	if !bytes.Contains(buffer.Bytes(), []byte(`"kind":"api"`)) {
		t.Fatalf("a root-mounted route was not classified as api: %s", buffer.String())
	}
}

// Health stays quiet whatever the prefix: it is matched before the prefix is
// consulted, so an empty prefix must not turn probes into log noise.
func TestHealthStaysQuietUnderTheRootPrefix(t *testing.T) {
	var buffer bytes.Buffer
	router := NewRouter(Config{
		Logger:    slog.New(slog.NewJSONHandler(&buffer, &slog.HandlerOptions{Level: slog.LevelInfo})),
		APIPrefix: RootAPI,
	})
	router.Get("/health", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })

	router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/health", nil))

	if bytes.Contains(buffer.Bytes(), []byte("http request")) {
		t.Fatalf("a health probe was logged at info: %s", buffer.String())
	}
}

// The default is unchanged for every app that never sets it.
func TestAPIPrefixDefaultsToApi(t *testing.T) {
	var buffer bytes.Buffer
	router := NewRouter(Config{Logger: slog.New(slog.NewJSONHandler(&buffer, &slog.HandlerOptions{Level: slog.LevelInfo}))})
	router.Get("/api/things", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })

	router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/things", nil))

	if !bytes.Contains(buffer.Bytes(), []byte(`"kind":"api"`)) {
		t.Fatalf("the default prefix stopped working: %s", buffer.String())
	}
}
