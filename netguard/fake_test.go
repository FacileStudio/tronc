package netguard

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"strings"
)

const fakePublic = "93.184.215.14"

type fakeResolver map[string][]string

func (f fakeResolver) LookupNetIP(_ context.Context, _, host string) ([]netip.Addr, error) {
	addrs, ok := f[host]
	if !ok {
		return nil, errors.New("no such host")
	}
	out := make([]netip.Addr, len(addrs))
	for i, a := range addrs {
		out[i] = netip.MustParseAddr(a)
	}
	return out, nil
}

func loopbackDial(ctx context.Context, network, address string) (net.Conn, error) {
	var d net.Dialer
	return d.DialContext(ctx, network, strings.Replace(address, fakePublic, "127.0.0.1", 1))
}

func testGuard() *Guard {
	return &Guard{
		Resolver: fakeResolver{
			"public.test":  {fakePublic},
			"mixed.test":   {fakePublic, "10.0.0.7"},
			"private.test": {"10.128.0.1"},
			"meta.test":    {"::ffff:169.254.169.254"},
		},
		Dial: loopbackDial,
	}
}
