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
	scalar := []string{"script-src 'self' https://cdn.jsdelivr.net;", "font-src 'self' " + apiref.ScalarFontOrigin, "frame-ancestors 'none'", "report-uri /csp"}
	for name, cfg := range map[string]middleware.SecurityConfig{
		"enforced":    {CSPEnforce: true, CSPReportURI: "/csp"},
		"report-only": {CSPReportURI: "/csp"},
	} {
		header := docsHeader(t, middleware.SecurityHeaders(cfg))
		full := header.Get(middleware.HeaderCSP)
		if !cfg.CSPEnforce {
			full = header.Get(middleware.HeaderCSPReportOnly)
			if got := header.Get(middleware.HeaderCSP); got != "frame-ancestors 'none'; object-src 'none'; base-uri 'self'" {
				t.Errorf("%s: the enforced framing policy changed: %q", name, got)
			}
		} else if got := header.Get(middleware.HeaderCSPReportOnly); got != "" {
			t.Errorf("%s: the docs page downgraded to report-only: %q", name, got)
		}
		for _, want := range scalar {
			if !strings.Contains(full, want) {
				t.Errorf("%s: policy %q lacks %q", name, full, want)
			}
		}
	}
}

func TestDocsPageAddsNoCSPWhenTheAppSentNone(t *testing.T) {
	header := docsHeader(t, nil)
	if header.Get(middleware.HeaderCSP) != "" || header.Get(middleware.HeaderCSPReportOnly) != "" {
		t.Errorf("the docs page invented a CSP: %v", header)
	}
}
