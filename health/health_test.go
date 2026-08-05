package health

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
)

func status(t *testing.T, handler http.Handler, path string) (int, string) {
	t.Helper()
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))

	var body struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("%s did not answer JSON: %v (%s)", path, err, recorder.Body.String())
	}
	if got := recorder.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("%s content-type = %q, want application/json", path, got)
	}
	return recorder.Code, body.Status
}

func TestMountAnswersAtBothPrefixes(t *testing.T) {
	router := chi.NewRouter()
	Mount(router)

	for _, path := range []string{"/health", "/api/health"} {
		if code, state := status(t, router, path); code != http.StatusOK || state != "ok" {
			t.Errorf("%s = %d %q, want 200 ok", path, code, state)
		}
	}
	for _, path := range []string{"/ready", "/api/ready"} {
		if code, state := status(t, router, path); code != http.StatusOK || state != "ready" {
			t.Errorf("%s = %d %q, want 200 ready", path, code, state)
		}
	}
}

func TestReadyFailsWhenACheckFails(t *testing.T) {
	router := chi.NewRouter()
	Mount(router,
		func(context.Context) error { return nil },
		func(context.Context) error { return fmt.Errorf("database is asleep") },
	)

	code, state := status(t, router, "/ready")
	if code != http.StatusServiceUnavailable || state != "not_ready" {
		t.Errorf("/ready = %d %q, want 503 not_ready", code, state)
	}

	if code, state := status(t, router, "/health"); code != http.StatusOK || state != "ok" {
		t.Errorf("/health = %d %q; liveness must not depend on a check", code, state)
	}
}

func TestReadyBoundsSlowChecks(t *testing.T) {
	router := chi.NewRouter()
	Mount(router, func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	})

	if code, _ := status(t, router, "/ready"); code != http.StatusServiceUnavailable {
		t.Errorf("a hanging check produced %d, want 503", code)
	}
}
