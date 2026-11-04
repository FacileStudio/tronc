package middleware

import (
	"net/http"
	"slices"
	"strings"
)

// SecurityOverrideConfig changes the chassis headers for one route. Every
// field extends what the outer SecurityHeaders sent; none replaces it.
type SecurityOverrideConfig struct {
	// CSPEnforce, when it points at true, promotes a report-only policy to
	// an enforced one on this route. A route can never downgrade: false on a
	// route whose outer policy is enforced is ignored.
	CSPEnforce *bool
	// CSPSources extend the outer policy's directives, as in BuildCSP.
	// Opening frame-ancestors also drops X-Frame-Options.
	CSPSources map[string][]string
	// Allow grants these Permissions-Policy features to (self) on top of
	// what the outer policy grants.
	Allow []string
	// CrossOriginOpenerPolicy replaces the outer value when set.
	CrossOriginOpenerPolicy string
}

type securityOverride struct {
	enforce bool
	sources map[string][]string
	allow   []string
	coop    string
}

// SecurityOverride extends the headers set further out, for one route or
// group: a public embed that must be framed, a page that needs the camera.
// Mount it on that route only, under the chassis.
//
// It reads the policy the outer layer sent and writes back into the same
// headers, so the route keeps the app's mode, its script hashes and its
// report-uri. A route with no CSP from the outer layer gets none. It panics
// at construction on the same malformed values SecurityHeaders refuses.
func SecurityOverride(cfg SecurityOverrideConfig) func(http.Handler) http.Handler {
	override := must(newSecurityOverride(cfg))
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
			override.apply(w.Header())
			next.ServeHTTP(w, request)
		})
	}
}

func newSecurityOverride(cfg SecurityOverrideConfig) (*securityOverride, error) {
	if err := validateCSP(cfg.CSPSources, ""); err != nil {
		return nil, err
	}
	if _, err := permissionsPolicy(cfg.Allow); err != nil {
		return nil, err
	}
	if cfg.CrossOriginOpenerPolicy != "" {
		if _, err := openerPolicy(cfg.CrossOriginOpenerPolicy); err != nil {
			return nil, err
		}
	}
	sources := make(map[string][]string, len(cfg.CSPSources))
	for name, values := range cfg.CSPSources {
		sources[name] = slices.Clone(values)
	}
	return &securityOverride{
		enforce: cfg.CSPEnforce != nil && *cfg.CSPEnforce,
		sources: sources,
		allow:   slices.Clone(cfg.Allow),
		coop:    cfg.CrossOriginOpenerPolicy,
	}, nil
}

func (o *securityOverride) apply(header http.Header) {
	o.applyCSP(header)
	if len(o.sources["frame-ancestors"]) > 0 {
		header.Del(HeaderFrameOptions)
	}
	if len(o.allow) > 0 {
		header.Set(HeaderPermissionsPolicy, grantFeatures(header.Get(HeaderPermissionsPolicy), o.allow))
	}
	if o.coop != "" {
		header.Set(HeaderCOOP, o.coop)
	}
}

// applyCSP extends the full policy, which is the report-only one while the
// app has not enforced, and the minimal enforced one only in the directives
// it already has.
func (o *securityOverride) applyCSP(header http.Header) {
	enforced, reportOnly := header.Get(HeaderCSP), header.Get(HeaderCSPReportOnly)
	if reportOnly == "" {
		if enforced != "" {
			header.Set(HeaderCSP, renderPolicy(extendDirectives(parsePolicy(enforced), o.sources, true)))
		}
		return
	}
	full := renderPolicy(extendDirectives(parsePolicy(reportOnly), o.sources, true))
	if o.enforce {
		header.Del(HeaderCSPReportOnly)
		header.Set(HeaderCSP, full)
		return
	}
	header.Set(HeaderCSPReportOnly, full)
	if enforced != "" {
		header.Set(HeaderCSP, renderPolicy(extendDirectives(parsePolicy(enforced), o.sources, false)))
	}
}

// parsePolicy reads a policy back into directives, so an override can extend
// what the outer layer sent rather than rebuilding it from nothing.
func parsePolicy(policy string) []cspDirective {
	var directives []cspDirective
	for _, part := range strings.Split(policy, ";") {
		fields := strings.Fields(part)
		if len(fields) > 0 {
			directives = append(directives, cspDirective{strings.ToLower(fields[0]), fields[1:]})
		}
	}
	return directives
}

func renderPolicy(directives []cspDirective) string {
	parts := make([]string, 0, len(directives))
	for _, directive := range directives {
		if len(directive.sources) == 0 {
			parts = append(parts, directive.name)
			continue
		}
		parts = append(parts, directive.name+" "+strings.Join(directive.sources, " "))
	}
	return strings.Join(parts, "; ")
}
