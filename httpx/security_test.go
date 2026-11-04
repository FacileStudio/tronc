package httpx

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"

	"github.com/FacileStudio/tronc/middleware"
)

func TestSecurityHeadersAreOnByDefault(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	router := NewRouter(Config{Logger: slog.New(slog.DiscardHandler)})
	router.Get("/", ok)

	for name, handler := range map[string]http.Handler{
		"router": router,
		"chain":  Chain(Config{Logger: slog.New(slog.DiscardHandler)}, ok),
	} {
		request := httptest.NewRequest(http.MethodGet, "/", nil)
		request.RemoteAddr = "172.18.0.2:5000"
		request.Header.Set("X-Forwarded-Proto", "https")
		request.Header.Set("X-Forwarded-For", "203.0.113.7")
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)

		header := recorder.Header()
		if header.Get(middleware.HeaderCSPReportOnly) == "" || header.Get(middleware.HeaderCSP) != "" {
			t.Errorf("%s: CSP is not report-only by default: %v", name, header)
		}
		if header.Get(middleware.HeaderContentTypeOpts) != "nosniff" {
			t.Errorf("%s: nosniff missing", name)
		}
		if header.Get(middleware.HeaderHSTS) != middleware.HSTSValue {
			t.Errorf("%s: HSTS missing behind the default trusted proxy", name)
		}
	}
}

func TestSecurityHonoursAnEmptyTrustedProxyList(t *testing.T) {
	router := NewRouter(Config{Logger: slog.New(slog.DiscardHandler), TrustedProxies: []netip.Prefix{}})
	router.Get("/", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })

	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.RemoteAddr = "172.18.0.2:5000"
	request.Header.Set("X-Forwarded-Proto", "https")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)

	if got := recorder.Header().Get(middleware.HeaderHSTS); got != "" {
		t.Errorf("HSTS sent on a forwarded claim with no proxy trusted: %q", got)
	}
}

func TestSecurityConfigReachesTheChain(t *testing.T) {
	router := NewRouter(Config{
		Logger:   slog.New(slog.DiscardHandler),
		Security: middleware.SecurityConfig{CSPEnforce: true, Allow: []string{"camera", "microphone"}},
	})
	router.Get("/", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))

	if recorder.Header().Get(middleware.HeaderCSP) == "" {
		t.Error("CSPEnforce did not reach the router")
	}
	if got := recorder.Header().Get(middleware.HeaderPermissionsPolicy); got != "camera=(self), microphone=(self), geolocation=()" {
		t.Errorf("Permissions-Policy = %q", got)
	}
}
