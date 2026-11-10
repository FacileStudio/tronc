package conformance_test

import (
	"context"
	stderrors "errors"
	"net/http/httptest"
	"testing"

	"github.com/FacileStudio/tronc/health"
	"github.com/FacileStudio/tronc/httpx"
)

type healthInput struct {
	Method string `json:"method"`
	Path   string `json:"path"`
	DBOk   *bool  `json:"db_ok"`
}

type healthWant struct {
	Status      int    `json:"status"`
	ContentType string `json:"content_type,omitempty"`
	Body        string `json:"body"`
}

func healthVectors(t *testing.T) []vector {
	t.Helper()
	up, down := true, false
	inputs := []struct {
		name  string
		input healthInput
	}{
		{"live", healthInput{"GET", "/health", &up}},
		{"live_ignores_a_down_db", healthInput{"GET", "/health", &down}},
		{"ready", healthInput{"GET", "/ready", &up}},
		{"ready_db_down", healthInput{"GET", "/ready", &down}},
		{"ready_without_checks", healthInput{"GET", "/ready", nil}},
		{"api_live", healthInput{"GET", "/api/health", &up}},
		{"api_live_ignores_a_down_db", healthInput{"GET", "/api/health", &down}},
		{"api_ready", healthInput{"GET", "/api/ready", &up}},
		{"api_ready_db_down", healthInput{"GET", "/api/ready", &down}},
		{"post_refused", healthInput{"POST", "/health", &up}},
		{"head_refused", healthInput{"HEAD", "/ready", &up}},
		{"unknown_path", healthInput{"GET", "/healthz", &up}},
		{"trailing_slash_not_mounted", healthInput{"GET", "/health/", &up}},
	}

	cases := make([]vector, 0, len(inputs))
	for _, entry := range inputs {
		router := httpx.NewRouter(httpx.Config{Logger: discardLogger()})
		health.Mount(router, dbChecks(entry.input.DBOk)...)
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(entry.input.Method, entry.input.Path, nil))
		cases = append(cases, vector{entry.name, entry.input, healthWant{
			Status:      recorder.Code,
			ContentType: recorder.Header().Get("Content-Type"),
			Body:        recorder.Body.String(),
		}})
	}
	return cases
}

func dbChecks(ok *bool) []health.Check {
	if ok == nil {
		return nil
	}
	up := *ok
	return []health.Check{func(context.Context) error {
		if up {
			return nil
		}
		return stderrors.New("connection refused")
	}}
}
