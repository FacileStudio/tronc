package conformance_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/FacileStudio/tronc/env"
	"github.com/FacileStudio/tronc/httpx"
)

type clientIPInput struct {
	RemoteAddr     string            `json:"remote_addr"`
	Headers        map[string]string `json:"headers,omitempty"`
	TrustedProxies string            `json:"trusted_proxies"`
}

type clientIPWant struct {
	IP    string `json:"ip,omitempty"`
	Error string `json:"error,omitempty"`
}

func clientIPVectors(t *testing.T) []vector {
	t.Helper()
	const traefik, public, edge, visitor = "10.0.0.2:5555", "203.0.113.9:4444", "173.245.48.1", "198.51.100.7"
	xff := func(value string) map[string]string { return map[string]string{"X-Forwarded-For": value} }
	inputs := []struct {
		name  string
		input clientIPInput
	}{
		{"unset_trusts_private", clientIPInput{traefik, xff(visitor), ""}},
		{"private", clientIPInput{traefik, xff(visitor), "private"}},
		{"loopback_peer_trusted", clientIPInput{"127.0.0.1:5555", xff(visitor), "private"}},
		{"ipv4_mapped_peer_trusted", clientIPInput{"[::ffff:10.0.0.2]:5555", xff(visitor), "private"}},
		{"ipv6_private_peer_trusted", clientIPInput{"[fd00::2]:5555", xff("2001:db8::7"), "private"}},
		{"untrusted_peer_forged_header_ignored", clientIPInput{public, xff(visitor), "private"}},
		{"trusted_peer_without_header", clientIPInput{traefik, nil, "private"}},
		{"rightmost_untrusted_hop_wins", clientIPInput{traefik, xff("192.0.2.66, " + visitor + ", 10.0.0.3"), "private"}},
		{"hop_with_port", clientIPInput{traefik, xff(visitor + ":6000"), "private"}},
		{"unparsable_rightmost_hop_stops_the_walk", clientIPInput{traefik, xff(visitor + ", garbage"), "private"}},
		{"unparsable_hop_left_of_client_ignored", clientIPInput{traefik, xff("garbage, " + visitor), "private"}},
		{"only_trusted_hops", clientIPInput{traefik, xff("10.0.0.3, 10.0.0.4"), "private"}},
		{"none_trusts_nothing", clientIPInput{traefik, xff(visitor), "none"}},
		{"literal_cidr", clientIPInput{"192.0.2.10:80", xff(visitor), "192.0.2.0/24"}},
		{"literal_address", clientIPInput{"192.0.2.10:80", xff(visitor), "192.0.2.10"}},
		{"literal_cidr_excludes_private", clientIPInput{traefik, xff(visitor), "192.0.2.0/24"}},
		{"cloudflare_edge_without_cloudflare_set", clientIPInput{traefik, xff(visitor + ", " + edge), "private"}},
		{"cloudflare_chain_walked", clientIPInput{traefik, xff(visitor + ", " + edge), "private,cloudflare"}},
		{"cloudflare_replaced_chain_reads_header", clientIPInput{traefik, map[string]string{"X-Forwarded-For": edge, "Cf-Connecting-Ip": visitor}, "private,cloudflare"}},
		{"cloudflare_header_forged_from_origin", clientIPInput{traefik, map[string]string{"X-Forwarded-For": "203.0.113.50", "Cf-Connecting-Ip": "1.1.1.1"}, "private,cloudflare"}},
		{"cloudflare_header_ignored_without_cloudflare_set", clientIPInput{traefik, map[string]string{"X-Forwarded-For": edge, "Cf-Connecting-Ip": visitor}, "private"}},
		{"cloudflare_header_unparsable", clientIPInput{traefik, map[string]string{"X-Forwarded-For": edge, "Cf-Connecting-Ip": "not-an-ip"}, "private,cloudflare"}},
		{"cloudflare_header_from_untrusted_peer", clientIPInput{public, map[string]string{"X-Forwarded-For": edge, "Cf-Connecting-Ip": visitor}, "private,cloudflare"}},
		{"set_names_ignore_case", clientIPInput{traefik, xff(visitor + ", " + edge), " Private , CLOUDFLARE "}},
		{"invalid_entry_refused", clientIPInput{traefik, xff(visitor), "private,not-an-ip"}},
	}

	cases := make([]vector, 0, len(inputs))
	for _, entry := range inputs {
		cases = append(cases, vector{entry.name, entry.input, resolveClientIP(t, entry.input)})
	}
	return cases
}

func resolveClientIP(t *testing.T, input clientIPInput) clientIPWant {
	t.Helper()
	t.Setenv("TRUSTED_PROXIES", input.TrustedProxies)
	trusted, err := env.TrustedProxies()
	if err != nil {
		return clientIPWant{Error: err.Error()}
	}
	cdn, header := env.CDN()
	router := httpx.NewRouter(httpx.Config{Logger: discardLogger(), TrustedProxies: trusted, CDNProxies: cdn, CDNHeader: header})
	var seen string
	router.Get("/", func(_ http.ResponseWriter, request *http.Request) { seen = request.RemoteAddr })

	request := httptest.NewRequest("GET", "/", nil)
	request.RemoteAddr = input.RemoteAddr
	for name, value := range input.Headers {
		request.Header.Set(name, value)
	}
	router.ServeHTTP(httptest.NewRecorder(), request)
	return clientIPWant{IP: seen}
}
