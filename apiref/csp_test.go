package apiref_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/FacileStudio/tronc/apiref"
	"github.com/FacileStudio/tronc/middleware"
)

func docsHeader(t *testing.T, security func(http.Handler) http.Handler) http.Header {
	t.Helper()
	router := chi.NewRouter()
	if security != nil {
		router.Use(security)
	}
	apiref.Mount(router, apiref.Config{Title: "Test API"})
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, apiref.BasePath, nil))
	return recorder.Header()
}

func TestDocsPageAllowsScalarInTheAppsCSPMode(t *testing.T) {
	for name, cfg := range map[string]middleware.SecurityConfig{
		middleware.HeaderCSP:           {CSPEnforce: true},
		middleware.HeaderCSPReportOnly: {},
	} {
		header := docsHeader(t, middleware.SecurityHeaders(cfg))
		policy := header.Get(name)
		for _, want := range []string{"script-src 'self' https://cdn.jsdelivr.net;", "font-src 'self' " + apiref.ScalarFontOrigin, "frame-ancestors 'none'"} {
			if !strings.Contains(policy, want) {
				t.Errorf("%s = %q, lacks %q", name, policy, want)
			}
		}
		other := middleware.HeaderCSPReportOnly
		if name == other {
			other = middleware.HeaderCSP
		}
		if got := header.Get(other); got != "" {
			t.Errorf("the docs page switched the CSP mode: %s = %q", other, got)
		}
	}
}

func TestDocsPageAddsNoCSPWhenTheAppSentNone(t *testing.T) {
	header := docsHeader(t, nil)
	if header.Get(middleware.HeaderCSP) != "" || header.Get(middleware.HeaderCSPReportOnly) != "" {
		t.Errorf("the docs page invented a CSP: %v", header)
	}
}
