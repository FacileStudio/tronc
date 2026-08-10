package middleware

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
)

func seenRemoteAddr(t *testing.T, trusted []netip.Prefix, peer, forwarded string) string {
	t.Helper()
	var seen string
	handler := RealIP(trusted)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		seen = r.RemoteAddr
	}))

	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.RemoteAddr = peer
	if forwarded != "" {
		request.Header.Set("X-Forwarded-For", forwarded)
	}
	handler.ServeHTTP(httptest.NewRecorder(), request)
	return seen
}

// The whole point of this middleware, stated as a test: a peer that is not a
// trusted proxy cannot rename itself with a header. chi's RealIP fails this.
func TestRealIPIgnoresForwardedFromAnUntrustedPeer(t *testing.T) {
	got := seenRemoteAddr(t, DefaultTrustedProxies, "203.0.113.7:5555", "9.9.9.9")
	if got != "203.0.113.7:5555" {
		t.Fatalf("RemoteAddr = %q, want the untouched connection address", got)
	}
}

func TestRealIPTakesTheClientFromATrustedProxy(t *testing.T) {
	got := seenRemoteAddr(t, DefaultTrustedProxies, "10.0.0.3:5555", "203.0.113.7")
	if got != "203.0.113.7" {
		t.Fatalf("RemoteAddr = %q, want the forwarded client", got)
	}
}

// Traefik appends the address it received from, so a client that pre-seeds the
// header ends up to the *left* of its own real address. Walking right to left
// is what makes the forged entry unreachable.
func TestRealIPIgnoresAClientSeededHop(t *testing.T) {
	got := seenRemoteAddr(t, DefaultTrustedProxies, "10.0.0.3:5555", "9.9.9.9, 203.0.113.7")
	if got != "203.0.113.7" {
		t.Fatalf("RemoteAddr = %q, want the hop the proxy appended, not the one the client wrote", got)
	}
}

// Two proxies in front of the app: the rightmost hops are the proxies
// themselves, and the client is the first entry that is not one.
func TestRealIPWalksPastChainedProxies(t *testing.T) {
	got := seenRemoteAddr(t, DefaultTrustedProxies, "10.0.0.3:5555", "203.0.113.7, 10.0.0.9, 172.16.4.2")
	if got != "203.0.113.7" {
		t.Fatalf("RemoteAddr = %q, want the first untrusted hop from the right", got)
	}
}

// A garbage hop means somebody is writing the header by hand. Reaching past it
// would read whatever they put to its left, so the walk stops and the
// connection address stands.
func TestRealIPStopsAtAnUnparsableHop(t *testing.T) {
	got := seenRemoteAddr(t, DefaultTrustedProxies, "10.0.0.3:5555", "203.0.113.7, not-an-ip")
	if got != "10.0.0.3:5555" {
		t.Fatalf("RemoteAddr = %q, want the connection address", got)
	}
}

// An all-proxy chain carries no client, so there is nothing to promote.
func TestRealIPKeepsThePeerWhenEveryHopIsTrusted(t *testing.T) {
	got := seenRemoteAddr(t, DefaultTrustedProxies, "10.0.0.3:5555", "10.0.0.9, 172.16.4.2")
	if got != "10.0.0.3:5555" {
		t.Fatalf("RemoteAddr = %q, want the connection address", got)
	}
}

// An empty trust set is the "believe nothing" configuration, and a nil one is
// what a zero-value Config would hand over — neither may widen trust.
func TestRealIPWithoutTrustIgnoresTheHeader(t *testing.T) {
	for name, trusted := range map[string][]netip.Prefix{"nil": nil, "empty": {}} {
		t.Run(name, func(t *testing.T) {
			got := seenRemoteAddr(t, trusted, "10.0.0.3:5555", "203.0.113.7")
			if got != "10.0.0.3:5555" {
				t.Fatalf("RemoteAddr = %q, want the connection address", got)
			}
		})
	}
}

func TestRealIPHandlesIPv6(t *testing.T) {
	got := seenRemoteAddr(t, DefaultTrustedProxies, "[::1]:5555", "2001:db8::1")
	if got != "2001:db8::1" {
		t.Fatalf("RemoteAddr = %q, want the forwarded IPv6 client", got)
	}
}

// An IPv4-mapped IPv6 peer is the same host as its IPv4 form; a trust check
// that missed that would refuse to believe Traefik on a dual-stack network.
func TestRealIPUnmapsIPv4MappedPeers(t *testing.T) {
	got := seenRemoteAddr(t, DefaultTrustedProxies, "[::ffff:10.0.0.3]:5555", "203.0.113.7")
	if got != "203.0.113.7" {
		t.Fatalf("RemoteAddr = %q, want the forwarded client", got)
	}
}

func TestRealIPWithAnExplicitProxyList(t *testing.T) {
	trusted, err := ParseTrustedProxies([]string{"10.0.0.3"})
	if err != nil {
		t.Fatalf("ParseTrustedProxies: %v", err)
	}

	if got := seenRemoteAddr(t, trusted, "10.0.0.3:5555", "203.0.113.7"); got != "203.0.113.7" {
		t.Fatalf("the listed proxy was not believed: %q", got)
	}
	// Private, but not the listed proxy: a neighbour on the same Docker
	// network must not be able to speak for a visitor.
	if got := seenRemoteAddr(t, trusted, "10.0.0.9:5555", "203.0.113.7"); got != "10.0.0.9:5555" {
		t.Fatalf("an unlisted private peer was believed: %q", got)
	}
}

func TestParseTrustedProxies(t *testing.T) {
	prefixes, err := ParseTrustedProxies([]string{"10.0.0.0/8", " 192.168.1.7 ", "", "::1"})
	if err != nil {
		t.Fatalf("ParseTrustedProxies: %v", err)
	}
	if len(prefixes) != 3 {
		t.Fatalf("got %d prefixes, want 3 (the blank entry is skipped)", len(prefixes))
	}
	if !TrustedBy(netip.MustParseAddr("10.4.4.4"), prefixes) {
		t.Error("the CIDR block does not contain an address inside it")
	}
	if !TrustedBy(netip.MustParseAddr("192.168.1.7"), prefixes) {
		t.Error("the bare address did not become a host prefix")
	}
	if TrustedBy(netip.MustParseAddr("192.168.1.8"), prefixes) {
		t.Error("a bare address widened into more than itself")
	}

	if _, err := ParseTrustedProxies([]string{"not-a-network"}); err == nil {
		t.Error("garbage was accepted as a trusted proxy")
	}
}

// The reason this package exists, reproduced at the layer that was broken: a
// rotating header must not hand out a fresh rate-limit identity. Measured on
// Journal before the fix, 70 such requests were all accepted against a 60/min
// bucket.
func TestRotatingForwardedHeaderCannotMintIdentities(t *testing.T) {
	seen := map[string]bool{}
	for i := 1; i <= 70; i++ {
		addr := seenRemoteAddr(t, DefaultTrustedProxies, "203.0.113.7:5555", netip.AddrFrom4([4]byte{198, 51, 100, byte(i)}).String())
		seen[addr] = true
	}
	if len(seen) != 1 {
		t.Fatalf("70 spoofed requests produced %d distinct identities, want 1", len(seen))
	}
}

// Behind Cloudflare the chain is [visitor, cf-edge] once Traefik appends, so
// trusting the edge is what makes the visitor reachable. Without it every
// visitor behind one edge shares a bucket.
func TestRealIPWalksPastCloudflare(t *testing.T) {
	trusted := append(append([]netip.Prefix{}, DefaultTrustedProxies...), CloudflareProxies...)

	got := seenRemoteAddr(t, trusted, "10.0.0.3:5555", "2a02:8428:df25:5601::1, 172.70.108.91")
	if got != "2a02:8428:df25:5601::1" {
		t.Fatalf("RemoteAddr = %q, want the visitor behind the Cloudflare edge", got)
	}

	// Without Cloudflare trusted, the walk stops at the edge — correct, but
	// coarse. This is the behaviour the opt-in exists to change.
	if got := seenRemoteAddr(t, DefaultTrustedProxies, "10.0.0.3:5555", "2a02:8428:df25:5601::1, 172.70.108.91"); got != "172.70.108.91" {
		t.Fatalf("RemoteAddr = %q, want the edge when Cloudflare is not trusted", got)
	}
}

// The attack that trusting a CDN normally invites: the attacker points their
// own Cloudflare zone at this origin, so their traffic arrives from a trusted
// edge carrying a header they wrote. Cloudflare appends the real address after
// theirs, and a right-to-left walk therefore never reaches the forged entries.
func TestCloudflareTrustDoesNotExposeSeededHops(t *testing.T) {
	trusted := append(append([]netip.Prefix{}, DefaultTrustedProxies...), CloudflareProxies...)

	got := seenRemoteAddr(t, trusted, "10.0.0.3:5555", "1.2.3.4, 9.9.9.9, 203.0.113.7, 172.70.108.91")
	if got != "203.0.113.7" {
		t.Fatalf("RemoteAddr = %q, want the address Cloudflare appended, not one the client seeded", got)
	}
}

func TestCloudflareRangesAreSane(t *testing.T) {
	if len(CloudflareProxies) < 20 {
		t.Fatalf("got %d Cloudflare prefixes, want the published ~22", len(CloudflareProxies))
	}
	for _, addr := range []string{"172.70.108.91", "162.158.23.213", "104.16.0.1", "2606:4700::1"} {
		if !TrustedBy(netip.MustParseAddr(addr), CloudflareProxies) {
			t.Errorf("%s is a Cloudflare edge and is not covered", addr)
		}
	}
	for _, addr := range []string{"37.65.43.218", "8.8.8.8", "10.0.0.1"} {
		if TrustedBy(netip.MustParseAddr(addr), CloudflareProxies) {
			t.Errorf("%s is not Cloudflare and is covered", addr)
		}
	}
}

func cfConfig() RealIPConfig {
	return RealIPConfig{
		Trusted: append(append([]netip.Prefix{}, DefaultTrustedProxies...), CloudflareProxies...),
		CDN:     CloudflareProxies,
		Header:  "Cf-Connecting-Ip",
	}
}

func seenWith(t *testing.T, cfg RealIPConfig, peer, forwarded, cdnHeader string) string {
	t.Helper()
	var seen string
	handler := RealIPWith(cfg)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		seen = r.RemoteAddr
	}))
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.RemoteAddr = peer
	if forwarded != "" {
		request.Header.Set("X-Forwarded-For", forwarded)
	}
	if cdnHeader != "" {
		request.Header.Set("Cf-Connecting-Ip", cdnHeader)
	}
	handler.ServeHTTP(httptest.NewRecorder(), request)
	return seen
}

// Traefik replaces X-Forwarded-For with the address it received from rather
// than extending it, so behind Cloudflare the app sees a one-entry chain
// holding the edge and the visitor survives only in Cf-Connecting-Ip. This is
// the shape observed in production, not a hypothetical.
func TestCDNHeaderRecoversTheVisitorBehindAReplacingProxy(t *testing.T) {
	got := seenWith(t, cfConfig(), "10.0.1.30:43516", "172.70.108.91", "2a02:8428:df25:5601::1")
	if got != "2a02:8428:df25:5601::1" {
		t.Fatalf("RemoteAddr = %q, want the visitor from the CDN header", got)
	}
}

// The condition that keeps this from undoing the package: the origin is
// reachable directly, so a request that did not come through the CDN carries
// the sender's own address as the rightmost hop and its header is ignored.
func TestCDNHeaderIsIgnoredWhenTheRequestSkippedTheCDN(t *testing.T) {
	got := seenWith(t, cfConfig(), "10.0.1.30:43516", "203.0.113.7", "9.9.9.9")
	if got != "203.0.113.7" {
		t.Fatalf("RemoteAddr = %q, want the real sender, not the header they wrote", got)
	}
}

// An untrusted peer never reaches the CDN branch at all.
func TestCDNHeaderIsIgnoredFromAnUntrustedPeer(t *testing.T) {
	got := seenWith(t, cfConfig(), "203.0.113.7:5555", "172.70.108.91", "9.9.9.9")
	if got != "203.0.113.7:5555" {
		t.Fatalf("RemoteAddr = %q, want the connection address", got)
	}
}

// A forwarded chain that still carries a real visitor wins over the header:
// the header is the fallback for a chain that lost it, not a preference.
func TestForwardedChainWinsOverTheCDNHeader(t *testing.T) {
	got := seenWith(t, cfConfig(), "10.0.1.30:43516", "198.51.100.4, 172.70.108.91", "9.9.9.9")
	if got != "198.51.100.4" {
		t.Fatalf("RemoteAddr = %q, want the forwarded visitor", got)
	}
}

// Without the CDN configured the behaviour is exactly v0.11.0's.
func TestCDNCaseIsOffByDefault(t *testing.T) {
	got := seenWith(t, RealIPConfig{Trusted: cfConfig().Trusted}, "10.0.1.30:43516", "172.70.108.91", "2a02:8428:df25:5601::1")
	if got != "10.0.1.30:43516" {
		t.Fatalf("RemoteAddr = %q, want the connection address", got)
	}
}

func TestCDNHeaderMustParse(t *testing.T) {
	got := seenWith(t, cfConfig(), "10.0.1.30:43516", "172.70.108.91", "not-an-ip")
	if got != "10.0.1.30:43516" {
		t.Fatalf("RemoteAddr = %q, want the connection address", got)
	}
}
