package middleware

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const defaultPolicy = "default-src 'self'; base-uri 'self'; object-src 'none'; frame-ancestors 'none'; " +
	"form-action 'self'; img-src 'self' data: blob: https:; connect-src 'self'; script-src 'self'; " +
	"style-src 'self' 'unsafe-inline'"

const framingOnly = "frame-ancestors 'none'; object-src 'none'; base-uri 'self'"

func serveSecure(t *testing.T, mw func(http.Handler) http.Handler, handler http.HandlerFunc, request *http.Request) http.Header {
	t.Helper()
	if handler == nil {
		handler = func(http.ResponseWriter, *http.Request) {}
	}
	recorder := httptest.NewRecorder()
	mw(handler).ServeHTTP(recorder, request)
	return recorder.Header()
}

func TestSecurityHeadersDefaults(t *testing.T) {
	header := serveSecure(t, SecurityHeaders(SecurityConfig{}), nil, httptest.NewRequest(http.MethodGet, "/", nil))

	want := map[string]string{
		HeaderCSPReportOnly:     defaultPolicy,
		HeaderCSP:               framingOnly,
		HeaderFrameOptions:      "DENY",
		HeaderContentTypeOpts:   "nosniff",
		HeaderReferrerPolicy:    "strict-origin-when-cross-origin",
		HeaderPermissionsPolicy: "camera=(), microphone=(), geolocation=()",
		HeaderCOOP:              "same-origin",
	}
	for key, value := range want {
		if got := header.Get(key); got != value {
			t.Errorf("%s = %q, want %q", key, got, value)
		}
	}
	if got := header.Get(HeaderHSTS); got != "" {
		t.Errorf("plain HTTP got HSTS: %q", got)
	}
}

func TestSecurityHeadersEnforceSendsTheRealHeader(t *testing.T) {
	header := serveSecure(t, SecurityHeaders(SecurityConfig{CSPEnforce: true}), nil, httptest.NewRequest(http.MethodGet, "/", nil))

	if got := header.Get(HeaderCSP); got != defaultPolicy {
		t.Errorf("CSP = %q, want %q", got, defaultPolicy)
	}
	if got := header.Get(HeaderCSPReportOnly); got != "" {
		t.Errorf("enforced mode also sent report-only: %q", got)
	}
}

func TestBuildCSPExtendsReplacesAndAppends(t *testing.T) {
	policy, err := BuildCSP(map[string][]string{
		"connect-src":     {"wss://live.example.test", "'self'"},
		"frame-ancestors": {"https://ardoise.example.test"},
		"script-src":      {"'sha256-abc='"},
		"media-src":       {"'self'", "blob:"},
		"frame-src":       {"blob:"},
	}, "/api/csp-report")
	if err != nil {
		t.Fatal(err)
	}

	for _, directive := range []string{
		"connect-src 'self' wss://live.example.test;",
		"frame-ancestors https://ardoise.example.test;",
		"script-src 'self' 'sha256-abc=';",
		"style-src 'self' 'unsafe-inline'; frame-src blob:; media-src 'self' blob:; report-uri /api/csp-report",
	} {
		if !strings.Contains(policy, directive) {
			t.Errorf("policy %q lacks %q", policy, directive)
		}
	}
	if strings.Contains(policy, "'none' https://") {
		t.Errorf("extending a 'none' directive kept 'none': %q", policy)
	}
}

func TestBuildCSPRefusesAnInjectedSource(t *testing.T) {
	for _, extra := range []map[string][]string{
		{"connect-src": {"https://a.example; script-src *"}},
		{"connect-src": {"https://a.example\r\nSet-Cookie: x"}},
		{"connect-src": {"https://a.example https://b.example"}},
		{"connect-src": {""}},
		{"Script-Src;": {"'self'"}},
	} {
		if policy, err := BuildCSP(extra, ""); err == nil {
			t.Errorf("BuildCSP(%v) = %q, want an error", extra, policy)
		}
	}
	if _, err := BuildCSP(nil, "/report; script-src *"); err == nil {
		t.Error("an injected report-uri was accepted")
	}
}

func TestPermissionsPolicyGrantsOnlyWhatIsAllowed(t *testing.T) {
	header := serveSecure(t, SecurityHeaders(SecurityConfig{Allow: []string{"camera", "microphone", "display-capture"}}),
		nil, httptest.NewRequest(http.MethodGet, "/", nil))

	want := "camera=(self), microphone=(self), geolocation=(), display-capture=(self)"
	if got := header.Get(HeaderPermissionsPolicy); got != want {
		t.Errorf("Permissions-Policy = %q, want %q", got, want)
	}
}

func TestSecurityHeadersPanicsOnBadConfig(t *testing.T) {
	for name, cfg := range map[string]SecurityConfig{
		"feature": {Allow: []string{"camera=*"}},
		"coop":    {CrossOriginOpenerPolicy: "same-origin\r\nX: y"},
		"source":  {CSPSources: map[string][]string{"script-src": {"* ; img-src *"}}},
	} {
		if _, err := newSecurityHeaders(cfg); err == nil {
			t.Errorf("%s: newSecurityHeaders accepted it", name)
		}
	}
	defer func() {
		if recover() == nil {
			t.Error("SecurityHeaders did not panic on a bad config")
		}
	}()
	SecurityHeaders(SecurityConfig{Allow: []string{"camera=*"}})
}

func TestCOOPIsConfigurable(t *testing.T) {
	header := serveSecure(t, SecurityHeaders(SecurityConfig{CrossOriginOpenerPolicy: "same-origin-allow-popups"}),
		nil, httptest.NewRequest(http.MethodGet, "/", nil))
	if got := header.Get(HeaderCOOP); got != "same-origin-allow-popups" {
		t.Errorf("COOP = %q", got)
	}
}

func TestHSTSFollowsTheScheme(t *testing.T) {
	trusted := DefaultTrustedProxies
	cases := map[string]struct {
		remote string
		proto  []string
		tls    bool
		want   bool
	}{
		"direct TLS":                    {"203.0.113.9:1234", nil, true, true},
		"plain HTTP":                    {"203.0.113.9:1234", nil, false, false},
		"https from trusted proxy":      {"10.0.0.2:1234", []string{"https"}, false, true},
		"http from trusted proxy":       {"10.0.0.2:1234", []string{"http"}, false, false},
		"https claimed by an outsider":  {"203.0.113.9:1234", []string{"https"}, false, false},
		"forged left hop, real right":   {"10.0.0.2:1234", []string{"https, http"}, false, false},
		"proxy appended https":          {"10.0.0.2:1234", []string{"http", "HTTPS"}, false, true},
		"trusted proxy without a proto": {"10.0.0.2:1234", nil, false, false},
	}
	for name, testCase := range cases {
		request := httptest.NewRequest(http.MethodGet, "/", nil)
		request.RemoteAddr = testCase.remote
		for _, value := range testCase.proto {
			request.Header.Add("X-Forwarded-Proto", value)
		}
		if testCase.tls {
			request.TLS = &tls.ConnectionState{}
		}
		header := serveSecure(t, SecurityHeaders(SecurityConfig{Trusted: trusted}), nil, request)
		if got := header.Get(HeaderHSTS) == HSTSValue; got != testCase.want {
			t.Errorf("%s: HSTS sent = %v, want %v", name, got, testCase.want)
		}
	}
}

func TestHSTSTrustsNoProxyByDefault(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.RemoteAddr = "10.0.0.2:1234"
	request.Header.Set("X-Forwarded-Proto", "https")
	if header := serveSecure(t, SecurityHeaders(SecurityConfig{}), nil, request); header.Get(HeaderHSTS) != "" {
		t.Error("a nil trust set believed X-Forwarded-Proto")
	}
}

func TestHandlerHeadersWin(t *testing.T) {
	handler := func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(HeaderCSPReportOnly, "sandbox")
		w.Header().Set(HeaderReferrerPolicy, "no-referrer")
	}
	header := serveSecure(t, SecurityHeaders(SecurityConfig{}), handler, httptest.NewRequest(http.MethodGet, "/", nil))

	if got := header.Values(HeaderCSPReportOnly); len(got) != 1 || got[0] != "sandbox" {
		t.Errorf("handler CSP was not kept alone: %q", got)
	}
	if got := header.Get(HeaderReferrerPolicy); got != "no-referrer" {
		t.Errorf("handler Referrer-Policy was overwritten: %q", got)
	}
}

func TestUpstreamHeadersAreNotOverwritten(t *testing.T) {
	upstream := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
			w.Header().Set(HeaderCOOP, "unsafe-none")
			next.ServeHTTP(w, request)
		})
	}
	mw := func(next http.Handler) http.Handler { return upstream(SecurityHeaders(SecurityConfig{})(next)) }
	if got := serveSecure(t, mw, nil, httptest.NewRequest(http.MethodGet, "/", nil)).Get(HeaderCOOP); got != "unsafe-none" {
		t.Errorf("COOP set upstream was overwritten: %q", got)
	}
}

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

func TestHSTSCanExcludeSubdomains(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.TLS = &tls.ConnectionState{}
	header := serveSecure(t, SecurityHeaders(SecurityConfig{HSTSExcludeSubdomains: true}), nil, request)
	if got := header.Get(HeaderHSTS); got != "max-age=31536000" {
		t.Errorf("HSTS = %q", got)
	}
}
