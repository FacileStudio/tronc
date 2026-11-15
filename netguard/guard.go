package netguard

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/netip"
	"net/url"
	"regexp"
	"strings"
)

const (
	// CodeURL marks a URL refused before any lookup: bad scheme, credentials
	// or an internal host name.
	CodeURL = "blocked_url"
	// CodeInternal marks a host or request that reaches an internal address.
	CodeInternal = "blocked_internal"
	// CodeUnresolved marks a host with no address to check.
	CodeUnresolved = "blocked_unresolved"
)

// BlockedError is a refusal by the guard. Code is one of the Code* values.
// The message never carries the resolved address, so a refusal does not leak
// what an internal name resolves to.
type BlockedError struct {
	Code   string
	Reason string
}

// Error renders the code and the reason.
func (e *BlockedError) Error() string { return e.Code + ": " + e.Reason }

// AsBlocked returns the guard refusal wrapped in err, if any.
func AsBlocked(err error) (*BlockedError, bool) {
	var b *BlockedError
	ok := errors.As(err, &b)
	return b, ok
}

func block(code, reason string) error { return &BlockedError{Code: code, Reason: reason} }

var internalSuffixes = []string{
	".localhost", ".local", ".internal", ".lan", ".home.arpa", ".localdomain", ".intranet",
}

var numericLabel = regexp.MustCompile(`^(0x[0-9a-f]*|[0-9]+)$`)

// CheckURL parses raw and refuses non-http(s) schemes, credentials and
// internal host names. It does not resolve DNS: Guard.Resolve does that.
func CheckURL(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, block(CodeURL, "invalid url")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, block(CodeURL, "only http and https urls are allowed")
	}
	if u.User != nil {
		return nil, block(CodeURL, "urls with credentials are not allowed")
	}
	if err := CheckHost(u.Hostname()); err != nil {
		return nil, err
	}
	return u, nil
}

// CheckHost refuses empty, single-label and internal-suffix names, internal
// IP literals, and numeric forms a browser would read as an IPv4 address.
func CheckHost(host string) error {
	h := strings.TrimSuffix(strings.ToLower(strings.Trim(host, "[]")), ".")
	if addr, err := netip.ParseAddr(h); err == nil {
		if IsInternal(addr) {
			return block(CodeInternal, "internal addresses are not allowed")
		}
		return nil
	}
	labels := strings.Split(h, ".")
	switch {
	case h == "localhost" || hasInternalSuffix(h):
		return block(CodeURL, "internal host names are not allowed")
	case len(labels) < 2:
		return block(CodeURL, "single-label hosts are not allowed")
	case numericLabel.MatchString(labels[len(labels)-1]):
		return block(CodeURL, "non-canonical ip hosts are not allowed")
	}
	return nil
}

func hasInternalSuffix(h string) bool {
	for _, s := range internalSuffixes {
		if strings.HasSuffix(h, s) {
			return true
		}
	}
	return false
}

// Resolver looks up the addresses of a host. *net.Resolver satisfies it.
type Resolver interface {
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
}

// Guard checks hosts against the internal ranges and dials only the address
// it checked. The zero value uses the system resolver and a dialer whose
// Control hook re-checks the connected address.
type Guard struct {
	Resolver Resolver
	// Dial replaces the final connection, for tests that must reach a local
	// server. Production code leaves it nil, which dials through Control.
	Dial func(ctx context.Context, network, address string) (net.Conn, error)
	// TLS configures the HTTPS clients the guard builds. Nil in production; a
	// test passes the pool of its own TLS server.
	TLS *tls.Config
}

// Resolve checks host, resolves it and returns one address to connect to. It
// refuses the host when it does not resolve or when ANY address is internal.
func (g *Guard) Resolve(ctx context.Context, host string) (netip.Addr, error) {
	if err := CheckHost(host); err != nil {
		return netip.Addr{}, err
	}
	h := strings.Trim(host, "[]")
	if addr, err := netip.ParseAddr(h); err == nil {
		return addr.Unmap(), nil
	}
	addrs, err := g.resolver().LookupNetIP(ctx, "ip", h)
	if err != nil || len(addrs) == 0 {
		return netip.Addr{}, block(CodeUnresolved, "host does not resolve")
	}
	pick := addrs[0].Unmap()
	for _, a := range addrs {
		if IsInternal(a) {
			return netip.Addr{}, block(CodeInternal, "host resolves to an internal address")
		}
		if a.Unmap().Is4() && !pick.Is4() {
			pick = a.Unmap()
		}
	}
	return pick, nil
}

func (g *Guard) resolver() Resolver {
	if g.Resolver != nil {
		return g.Resolver
	}
	return net.DefaultResolver
}
