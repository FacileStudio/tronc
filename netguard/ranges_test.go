package netguard

import (
	"net/netip"
	"testing"
)

func TestIsInternalBlocksEveryRange(t *testing.T) {
	internal := []string{
		"127.0.0.1", "127.255.0.9", "10.0.0.1", "10.128.0.1", "172.16.0.1", "172.31.255.254",
		"192.168.1.1", "169.254.169.254", "100.64.0.1", "100.127.255.254", "0.0.0.0", "224.0.0.1",
		"239.255.255.250", "240.0.0.1", "255.255.255.255", "192.0.0.8", "198.18.0.1",
		"::", "::1", "fe80::1", "fe80::1%eth0", "fc00::1", "fd12:3456::1", "ff02::1",
		"::ffff:127.0.0.1", "::ffff:10.0.0.1", "::ffff:169.254.169.254",
		"64:ff9b::a9fe:a9fe", "64:ff9b::7f00:1", "2002:7f00:1::", "2002:a9fe:a9fe::1",
		"2001:db8::1", "2001::1", "64:ff9b:1::1", "fec0::1",
		"168.63.129.16", "100.100.100.200", "192.0.0.192", "fd00:ec2::254", "0.1.2.3", "5f00::1", "::ffff:100.64.0.1",
	}
	for _, s := range internal {
		if !IsInternal(netip.MustParseAddr(s)) {
			t.Errorf("%s should be internal", s)
		}
	}
}

func TestIsInternalAllowsPublic(t *testing.T) {
	public := []string{
		"93.184.215.14", "8.8.8.8", "1.1.1.1", "172.32.0.1", "100.128.0.1", "2606:4700::1111",
		"::ffff:8.8.8.8", "64:ff9b::808:808", "2002:808:808::1",
	}
	for _, s := range public {
		if IsInternal(netip.MustParseAddr(s)) {
			t.Errorf("%s should be public", s)
		}
	}
	if !IsInternal(netip.Addr{}) {
		t.Error("an invalid address must count as internal")
	}
}

var checkURLCases = map[string]string{
	"https://example.com/a":            "",
	"http://example.com:8080/":         "",
	"http://93.184.215.14/":            "",
	"ftp://example.com/":               CodeURL,
	"file:///etc/passwd":               CodeURL,
	"javascript:alert(1)":              CodeURL,
	"http://user:pass@example.com/":    CodeURL,
	"http://localhost/":                CodeURL,
	"http://LOCALHOST./":               CodeURL,
	"http://glouton-redis:6379/":       CodeURL,
	"http://printer.local/":            CodeURL,
	"http://metadata.google.internal/": CodeURL,
	"http://app.localhost/":            CodeURL,
	"http://2130706433/":               CodeURL,
	"http://0x7f.0.0.1/":               CodeURL,
	"http://127.1/":                    CodeURL,
	"http://127.0.0.1/":                CodeInternal,
	"http://169.254.169.254/latest/":   CodeInternal,
	"http://[::1]:8080/":               CodeInternal,
	"http://[::ffff:a9fe:a9fe]/":       CodeInternal,
	"http://[fe80::1%25eth0]/":         CodeInternal,
	"http://10.128.0.1/":               CodeInternal,
	"http://[64:ff9b::a9fe:a9fe]/":     CodeInternal,
}

func TestCheckURL(t *testing.T) {
	for raw, want := range checkURLCases {
		_, err := CheckURL(raw)
		got := ""
		if b, ok := AsBlocked(err); ok {
			got = b.Code
		} else if err != nil {
			got = "other"
		}
		if got != want {
			t.Errorf("%s: got %q, want %q (%v)", raw, got, want, err)
		}
	}
}
