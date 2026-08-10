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
