package netguard

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"syscall"
	"time"
)

const maxRedirects = 10

var guardedDialer = &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second, Control: Control}

// Control is a net.Dialer Control hook: it refuses the connection when the
// address the socket is about to connect to is internal, whatever name led
// there. Install it on any dialer that reaches a caller-supplied host, even
// without a Guard:
//
//	dialer := &net.Dialer{Timeout: 5 * time.Second, Control: netguard.Control}
func Control(_, address string, _ syscall.RawConn) error {
	ap, err := netip.ParseAddrPort(address)
	if err != nil || IsInternal(ap.Addr()) {
		return block(CodeInternal, "connection to an internal address refused")
	}
	return nil
}

// DialContext resolves and checks the host in address, then connects to the
// checked address itself, so a DNS answer that changes between the check and
// the connection (rebinding) is never used. The connection is checked again by
// Control.
func (g *Guard) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, block(CodeURL, "invalid address")
	}
	ip, err := g.Resolve(ctx, host)
	if err != nil {
		return nil, err
	}
	target := net.JoinHostPort(ip.String(), port)
	if g.Dial != nil {
		return g.Dial(ctx, network, target)
	}
	return guardedDialer.DialContext(ctx, network, target)
}

// Transport returns an HTTP transport that ignores proxy settings and dials
// only through the guard. TLS, when set on the Guard, configures it.
func (g *Guard) Transport() *http.Transport {
	return &http.Transport{
		Proxy:                 nil,
		DialContext:           g.DialContext,
		TLSClientConfig:       g.TLS,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          32,
		IdleConnTimeout:       60 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: time.Second,
	}
}

// Client returns an HTTP client that dials through the guard and follows no
// redirect: a redirect is an answer, and the caller decides whether the new
// URL is worth a request of its own. This is the strict default.
func (g *Guard) Client(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout:   timeout,
		Transport: g.Transport(),
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return errors.New("netguard: redirects are not followed")
		},
	}
}

// ClientFollowing is Client for a fetch that must follow redirects, such as a
// link preview. It checks every hop with CheckURL before following it, stops
// after ten, and each hop is dialled through the guard like the first request.
func (g *Guard) ClientFollowing(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout:       timeout,
		Transport:     g.Transport(),
		CheckRedirect: checkRedirect,
	}
}

func checkRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= maxRedirects {
		return errors.New("netguard: stopped after 10 redirects")
	}
	_, err := CheckURL(req.URL.String())
	return err
}
