package conformance_test

import (
	"crypto/tls"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/FacileStudio/tronc/httpx"
	"github.com/FacileStudio/tronc/middleware"
)

type securityConfigInput struct {
	CSPEnforce            bool                `json:"csp_enforce,omitempty"`
	CSPSources            map[string][]string `json:"csp_sources,omitempty"`
	CSPReportURI          string              `json:"csp_report_uri,omitempty"`
	Allow                 []string            `json:"allow,omitempty"`
	CrossOriginOpener     string              `json:"cross_origin_opener_policy,omitempty"`
	HSTSExcludeSubdomains bool                `json:"hsts_exclude_subdomains,omitempty"`
}

type securityInput struct {
	Config         securityConfigInput `json:"config"`
	RemoteAddr     string              `json:"remote_addr"`
	TLS            bool                `json:"tls"`
	ForwardedProto []string            `json:"forwarded_proto,omitempty"`
}

type securityWant struct {
	Headers map[string]string `json:"headers,omitempty"`
	Panic   string            `json:"panic,omitempty"`
}

var securityHeaderNames = []string{
	middleware.HeaderCSP,
	middleware.HeaderCSPReportOnly,
	middleware.HeaderFrameOptions,
	middleware.HeaderContentTypeOpts,
	middleware.HeaderReferrerPolicy,
	middleware.HeaderPermissionsPolicy,
	middleware.HeaderCOOP,
	middleware.HeaderHSTS,
}

func securityHeaderVectors(t *testing.T) []vector {
	t.Helper()
	const public, traefik = "203.0.113.9:4444", "10.0.0.2:5555"
	hashes := []string{"'sha256-47DEQpj8HBSa+/TImW+5JCeuQeRkm5NMpJWZG3hSuFU='", "'sha256-RFWPLDbv2BY+rCkDzsE+0fr8ylGr2R2faWMhq4lfEQc='"}
	inputs := []struct {
		name  string
		input securityInput
	}{
		{"default_report_only", securityInput{RemoteAddr: public}},
		{"enforced", securityInput{Config: securityConfigInput{CSPEnforce: true}, RemoteAddr: public}},
		{"extra_script_src", securityInput{Config: securityConfigInput{CSPEnforce: true, CSPSources: map[string][]string{"script-src": hashes}}, RemoteAddr: public}},
		{"extra_sources_deduplicated", securityInput{Config: securityConfigInput{CSPEnforce: true, CSPSources: map[string][]string{"connect-src": {"'self'", "wss://feuilles.facile.studio", "wss://feuilles.facile.studio"}}}, RemoteAddr: public}},
		{"new_directives_appended_in_name_order", securityInput{Config: securityConfigInput{CSPSources: map[string][]string{"worker-src": {"blob:"}, "font-src": {"data:"}, "script-src": {"'wasm-unsafe-eval'"}}}, RemoteAddr: public}},
		{"none_default_replaced", securityInput{Config: securityConfigInput{CSPSources: map[string][]string{"object-src": {"'self'"}}}, RemoteAddr: public}},
		{"framing_opened_drops_frame_options", securityInput{Config: securityConfigInput{CSPSources: map[string][]string{"frame-ancestors": {"https://bulle.facile.studio"}}}, RemoteAddr: public}},
		{"framing_explicit_none_keeps_frame_options", securityInput{Config: securityConfigInput{CSPSources: map[string][]string{"frame-ancestors": {"'none'"}}}, RemoteAddr: public}},
		{"report_uri", securityInput{Config: securityConfigInput{CSPReportURI: "/api/csp-report"}, RemoteAddr: public}},
		{"permissions_allow", securityInput{Config: securityConfigInput{Allow: []string{"camera", "fullscreen"}}, RemoteAddr: public}},
		{"opener_policy_popups", securityInput{Config: securityConfigInput{CrossOriginOpener: "same-origin-allow-popups"}, RemoteAddr: public}},
		{"hsts_on_tls", securityInput{RemoteAddr: public, TLS: true}},
		{"hsts_exclude_subdomains", securityInput{Config: securityConfigInput{HSTSExcludeSubdomains: true}, RemoteAddr: public, TLS: true}},
		{"hsts_from_trusted_proxy", securityInput{RemoteAddr: traefik, ForwardedProto: []string{"https"}}},
		{"hsts_proto_case_insensitive", securityInput{RemoteAddr: traefik, ForwardedProto: []string{"HTTPS"}}},
		{"no_hsts_from_untrusted_peer", securityInput{RemoteAddr: public, ForwardedProto: []string{"https"}}},
		{"no_hsts_when_nearest_hop_is_http", securityInput{RemoteAddr: traefik, ForwardedProto: []string{"https, http"}}},
		{"hsts_reads_the_last_header_line", securityInput{RemoteAddr: traefik, ForwardedProto: []string{"http", "http, https"}}},
		{"no_hsts_over_plain_http", securityInput{RemoteAddr: traefik}},
		{"refuses_source_with_semicolon", securityInput{Config: securityConfigInput{CSPSources: map[string][]string{"script-src": {"'self'; script-src *"}}}, RemoteAddr: public}},
		{"refuses_source_with_comma", securityInput{Config: securityConfigInput{CSPSources: map[string][]string{"img-src": {"a.example,b.example"}}}, RemoteAddr: public}},
		{"refuses_empty_source", securityInput{Config: securityConfigInput{CSPSources: map[string][]string{"img-src": {""}}}, RemoteAddr: public}},
		{"refuses_bad_directive_name", securityInput{Config: securityConfigInput{CSPSources: map[string][]string{"Script-Src": {"'self'"}}}, RemoteAddr: public}},
		{"refuses_bad_report_uri", securityInput{Config: securityConfigInput{CSPReportURI: "/report here"}, RemoteAddr: public}},
		{"refuses_bad_feature", securityInput{Config: securityConfigInput{Allow: []string{"camera=*"}}, RemoteAddr: public}},
		{"refuses_unknown_opener_policy", securityInput{Config: securityConfigInput{CrossOriginOpener: "none"}, RemoteAddr: public}},
	}

	cases := make([]vector, 0, len(inputs))
	for _, entry := range inputs {
		cases = append(cases, vector{entry.name, entry.input, securityHeaders(entry.input)})
	}
	return cases
}

func securityHeaders(input securityInput) (want securityWant) {
	defer func() {
		if recovered := recover(); recovered != nil {
			want = securityWant{Panic: fmt.Sprint(recovered)}
		}
	}()
	router := httpx.NewRouter(httpx.Config{Logger: discardLogger(), Security: middleware.SecurityConfig{
		CSPEnforce:              input.Config.CSPEnforce,
		CSPSources:              input.Config.CSPSources,
		CSPReportURI:            input.Config.CSPReportURI,
		Allow:                   input.Config.Allow,
		CrossOriginOpenerPolicy: input.Config.CrossOriginOpener,
		HSTSExcludeSubdomains:   input.Config.HSTSExcludeSubdomains,
	}})
	router.Get("/", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })

	request := httptest.NewRequest("GET", "/", nil)
	request.RemoteAddr = input.RemoteAddr
	if input.TLS {
		request.TLS = &tls.ConnectionState{}
	}
	for _, value := range input.ForwardedProto {
		request.Header.Add("X-Forwarded-Proto", value)
	}
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return securityWant{Headers: pickHeaders(recorder.Header(), securityHeaderNames...)}
}
