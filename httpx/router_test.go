package httpx

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
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
