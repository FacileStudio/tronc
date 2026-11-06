package middleware

import (
	"bytes"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFramingIsEnforcedInReportOnlyAndFollowsTheAppsSources(t *testing.T) {
	header := serveSecure(t, SecurityHeaders(SecurityConfig{CSPSources: map[string][]string{
		"frame-ancestors": {"https://ardoise.example.test"},
		"script-src":      {"'sha256-abc='"},
	}}), nil, httptest.NewRequest(http.MethodGet, "/", nil))

	if got := header.Get(HeaderCSP); got != "frame-ancestors https://ardoise.example.test; object-src 'none'; base-uri 'self'" {
		t.Errorf("enforced framing policy = %q", got)
	}
	if got := header.Get(HeaderFrameOptions); got != "" {
		t.Errorf("X-Frame-Options sent although frame-ancestors is open: %q", got)
	}
}

func TestAnExplicitNoneKeepsXFrameOptions(t *testing.T) {
	none := map[string][]string{"frame-ancestors": {"'none'"}}
	header := serveSecure(t, SecurityHeaders(SecurityConfig{CSPSources: none}), nil, httptest.NewRequest(http.MethodGet, "/", nil))
	if got := header.Get(HeaderFrameOptions); got != "DENY" {
		t.Errorf("app-level frame-ancestors 'none' dropped X-Frame-Options: %q", got)
	}

	mw := func(next http.Handler) http.Handler {
		return SecurityHeaders(SecurityConfig{})(SecurityOverride(SecurityOverrideConfig{CSPSources: none})(next))
	}
	header = serveSecure(t, mw, nil, httptest.NewRequest(http.MethodGet, "/", nil))
	if got := header.Get(HeaderFrameOptions); got != "DENY" {
		t.Errorf("override frame-ancestors 'none' dropped X-Frame-Options: %q", got)
	}
}

func TestSecurityHeadersWarnsOnAnOversizedPolicy(t *testing.T) {
	var logged bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logged, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	SecurityHeaders(SecurityConfig{})
	if logged.Len() != 0 {
		t.Fatalf("the default policy warned: %s", logged.String())
	}

	hashes := make([]string, 80)
	for i := range hashes {
		hashes[i] = fmt.Sprintf("'sha256-%044d'", i)
	}
	SecurityHeaders(SecurityConfig{CSPSources: map[string][]string{"script-src": hashes}})
	if !strings.Contains(logged.String(), "CSP header exceeds") || !strings.Contains(logged.String(), HeaderCSPReportOnly) {
		t.Errorf("80 hashes did not warn: %q", logged.String())
	}
}
