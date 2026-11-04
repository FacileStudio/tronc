package middleware

import (
	"net/http"
	"net/netip"
	"strings"
)

// Header names SecurityHeaders writes.
const (
	HeaderHSTS              = "Strict-Transport-Security"
	HeaderCSP               = "Content-Security-Policy"
	HeaderCSPReportOnly     = "Content-Security-Policy-Report-Only"
	HeaderContentTypeOpts   = "X-Content-Type-Options"
	HeaderReferrerPolicy    = "Referrer-Policy"
	HeaderPermissionsPolicy = "Permissions-Policy"
	HeaderCOOP              = "Cross-Origin-Opener-Policy"
)

// HSTSValue is the Strict-Transport-Security value sent on HTTPS requests.
const HSTSValue = "max-age=31536000; includeSubDomains"

// SecurityConfig shapes the security headers. The zero value is the suite
// default, with the CSP in report-only mode.
type SecurityConfig struct {
	// CSPEnforce sends Content-Security-Policy. False sends the same policy
	// as Content-Security-Policy-Report-Only, which is where every app
	// starts: the browser reports what it would block and blocks nothing.
	CSPEnforce bool
	// CSPSources adds sources to a directive, keyed by directive name. See
	// BuildCSP for how they combine with the default policy.
	CSPSources map[string][]string
	// CSPReportURI, when set, appends a report-uri directive.
	CSPReportURI string
	// Allow grants each listed Permissions-Policy feature to the page's own
	// origin, e.g. "camera" and "microphone" for a video app. camera,
	// microphone and geolocation are denied unless listed.
	Allow []string
	// CrossOriginOpenerPolicy defaults to same-origin.
	CrossOriginOpenerPolicy string
	// Trusted are the peers whose X-Forwarded-Proto is believed for the
	// HSTS decision. nil trusts none; httpx fills it with the router's
	// trusted proxies.
	Trusted []netip.Prefix
}

type securityHeaders struct {
	static  [][2]string
	cspDrop string
	trusted []netip.Prefix
}

// SecurityHeaders sets the suite's security headers on every response.
//
// Each header is set before the handler runs and only when nothing upstream
// set it, so a handler that calls Header().Set on one of them wins. Use Set,
// not Add: an added second CSP is enforced alongside the first, not instead.
//
// It panics at construction on a source, directive or feature that would
// corrupt the header, the same stance CORS takes on a credential leak.
func SecurityHeaders(cfg SecurityConfig) func(http.Handler) http.Handler {
	return mustSecurityHeaders(cfg).middleware(false)
}

// SecurityOverride replaces the headers set further out with the ones cfg
// describes, for one route or group: a public embed that must be framed,
// a page that needs the camera. Mount it on that route only.
func SecurityOverride(cfg SecurityConfig) func(http.Handler) http.Handler {
	return mustSecurityHeaders(cfg).middleware(true)
}

func mustSecurityHeaders(cfg SecurityConfig) *securityHeaders {
	headers, err := newSecurityHeaders(cfg)
	if err != nil {
		panic(err.Error())
	}
	return headers
}

func newSecurityHeaders(cfg SecurityConfig) (*securityHeaders, error) {
	coop, err := openerPolicy(cfg.CrossOriginOpenerPolicy)
	if err != nil {
		return nil, err
	}
	policy, err := BuildCSP(cfg.CSPSources, cfg.CSPReportURI)
	if err != nil {
		return nil, err
	}
	permissions, err := permissionsPolicy(cfg.Allow)
	if err != nil {
		return nil, err
	}

	cspKey, cspDrop := HeaderCSPReportOnly, HeaderCSP
	if cfg.CSPEnforce {
		cspKey, cspDrop = HeaderCSP, HeaderCSPReportOnly
	}
	return &securityHeaders{
		static: [][2]string{
			{cspKey, policy},
			{HeaderContentTypeOpts, "nosniff"},
			{HeaderReferrerPolicy, "strict-origin-when-cross-origin"},
			{HeaderPermissionsPolicy, permissions},
			{HeaderCOOP, coop},
		},
		cspDrop: cspDrop,
		trusted: cfg.Trusted,
	}, nil
}

func (s *securityHeaders) middleware(replace bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
			header := w.Header()
			if replace {
				header.Del(s.cspDrop)
			}
			for _, pair := range s.static {
				setHeader(header, pair[0], pair[1], replace)
			}
			if IsHTTPS(request, s.trusted) {
				setHeader(header, HeaderHSTS, HSTSValue, replace)
			}
			next.ServeHTTP(w, request)
		})
	}
}

func setHeader(header http.Header, key, value string, replace bool) {
	if _, present := header[key]; present && !replace {
		return
	}
	header.Set(key, value)
}

// IsHTTPS reports whether the visitor reached this server over HTTPS: the
// connection itself is TLS, or a trusted peer says so in X-Forwarded-Proto.
// Only the rightmost value counts, the one the nearest proxy wrote.
func IsHTTPS(request *http.Request, trusted []netip.Prefix) bool {
	if request.TLS != nil {
		return true
	}
	if len(trusted) == 0 {
		return false
	}
	peer, ok := parseAddr(request.RemoteAddr)
	if !ok || !TrustedBy(peer, trusted) {
		return false
	}
	values := request.Header.Values("X-Forwarded-Proto")
	if len(values) == 0 {
		return false
	}
	hops := strings.Split(values[len(values)-1], ",")
	return strings.EqualFold(strings.TrimSpace(hops[len(hops)-1]), "https")
}
