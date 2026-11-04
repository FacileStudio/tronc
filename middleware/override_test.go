package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func overrideHeader(t *testing.T, outer SecurityConfig, override SecurityOverrideConfig) http.Header {
	t.Helper()
	mw := func(next http.Handler) http.Handler {
		return SecurityHeaders(outer)(SecurityOverride(override)(next))
	}
	return serveSecure(t, mw, nil, httptest.NewRequest(http.MethodGet, "/embed/1", nil))
}

var (
	enforce   = true
	downgrade = false
	hashes    = map[string][]string{"script-src": {"'sha256-abc='"}}
	embedding = map[string][]string{"frame-ancestors": {"https://ardoise.example.test"}}
)

func TestSecurityOverrideKeepsAnEnforcedPolicyAndItsHashes(t *testing.T) {
	for name, override := range map[string]SecurityOverrideConfig{
		"no mode":   {CSPSources: embedding},
		"downgrade": {CSPSources: embedding, CSPEnforce: &downgrade},
	} {
		header := overrideHeader(t, SecurityConfig{CSPEnforce: true, CSPSources: hashes, CSPReportURI: "/csp"}, override)
		policy := header.Get(HeaderCSP)
		for _, want := range []string{"frame-ancestors https://ardoise.example.test;", "script-src 'self' 'sha256-abc=';", "report-uri /csp"} {
			if !strings.Contains(policy, want) {
				t.Errorf("%s: CSP = %q lacks %q", name, policy, want)
			}
		}
		if got := header.Get(HeaderCSPReportOnly); got != "" {
			t.Errorf("%s: the override downgraded to report-only: %q", name, got)
		}
		if got := header.Get(HeaderFrameOptions); got != "" {
			t.Errorf("%s: X-Frame-Options survived an open frame-ancestors: %q", name, got)
		}
	}
}

func TestSecurityOverrideExtendsBothPoliciesInReportOnly(t *testing.T) {
	header := overrideHeader(t, SecurityConfig{CSPSources: hashes}, SecurityOverrideConfig{
		CSPSources: map[string][]string{"frame-ancestors": {"https://ardoise.example.test"}, "frame-src": {"blob:"}},
	})

	full := header.Get(HeaderCSPReportOnly)
	for _, want := range []string{"frame-ancestors https://ardoise.example.test;", "script-src 'self' 'sha256-abc=';", "frame-src blob:"} {
		if !strings.Contains(full, want) {
			t.Errorf("report-only = %q lacks %q", full, want)
		}
	}
	if got := header.Get(HeaderCSP); got != "frame-ancestors https://ardoise.example.test; object-src 'none'; base-uri 'self'" {
		t.Errorf("enforced framing policy = %q; it must extend, never gain new directives", got)
	}
}

func TestSecurityOverrideCanPromoteToEnforced(t *testing.T) {
	header := overrideHeader(t, SecurityConfig{CSPSources: hashes}, SecurityOverrideConfig{CSPEnforce: &enforce})

	if got := header.Get(HeaderCSPReportOnly); got != "" {
		t.Errorf("report-only survived the promotion: %q", got)
	}
	if got := header.Get(HeaderCSP); !strings.Contains(got, "script-src 'self' 'sha256-abc='") || !strings.Contains(got, "frame-ancestors 'none'") {
		t.Errorf("promoted CSP = %q", got)
	}
	if got := header.Get(HeaderFrameOptions); got != "DENY" {
		t.Errorf("X-Frame-Options = %q, want DENY with frame-ancestors still closed", got)
	}
}

func TestSecurityOverrideExtendsPermissionsAndCOOP(t *testing.T) {
	header := overrideHeader(t, SecurityConfig{Allow: []string{"geolocation"}}, SecurityOverrideConfig{
		Allow:                   []string{"camera", "display-capture"},
		CrossOriginOpenerPolicy: "same-origin-allow-popups",
	})

	if got := header.Get(HeaderPermissionsPolicy); got != "camera=(self), microphone=(), geolocation=(self), display-capture=(self)" {
		t.Errorf("Permissions-Policy = %q", got)
	}
	if got := header.Get(HeaderCOOP); got != "same-origin-allow-popups" {
		t.Errorf("COOP = %q", got)
	}
}

func TestSecurityOverrideAddsNoCSPWithoutAnOuterOne(t *testing.T) {
	header := serveSecure(t, SecurityOverride(SecurityOverrideConfig{CSPSources: embedding, CSPEnforce: &enforce}),
		nil, httptest.NewRequest(http.MethodGet, "/", nil))
	if header.Get(HeaderCSP) != "" || header.Get(HeaderCSPReportOnly) != "" {
		t.Errorf("an override with nothing to extend invented a CSP: %v", header)
	}
}

func TestSecurityOverrideRefusesBadValues(t *testing.T) {
	for name, cfg := range map[string]SecurityOverrideConfig{
		"source":  {CSPSources: map[string][]string{"script-src": {"*; img-src *"}}},
		"feature": {Allow: []string{"camera=*"}},
		"coop":    {CrossOriginOpenerPolicy: "nope"},
	} {
		if _, err := newSecurityOverride(cfg); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
