package env

import (
	"github.com/FacileStudio/tronc/middleware"
	"net/netip"

	"slices"
	"testing"
	"time"
)

func TestLoadCoreDefaults(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost/tronc")

	core, err := LoadCore()
	if err != nil {
		t.Fatalf("LoadCore: %v", err)
	}
	if core.Port != 8080 || core.LogLevel != "info" || core.AppEnv != Development {
		t.Errorf("unexpected defaults: %+v", core)
	}
	if core.CORSAllowedOrigins != nil {
		t.Errorf("CORS defaulted to %v; an unset list must allow nobody", core.CORSAllowedOrigins)
	}
	if core.IsProduction() {
		t.Error("an unset APP_ENV reported production")
	}
}

func TestLoadCoreRequiresDatabaseURL(t *testing.T) {
	t.Setenv("DATABASE_URL", "")

	if _, err := LoadCore(); err == nil {
		t.Fatal("a missing DATABASE_URL was accepted")
	}
}

func TestLoadCoreRejectsANonNumericPort(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost/tronc")
	t.Setenv("PORT", "eight thousand")

	if _, err := LoadCore(); err == nil {
		t.Fatal("a non-numeric PORT was accepted")
	}
}

func TestCORSOriginsPrefersTheCanonicalName(t *testing.T) {
	t.Setenv("CORS_ALLOWED_ORIGINS", "https://a.example")
	t.Setenv("ALLOWED_ORIGINS", "https://b.example")
	t.Setenv("DOMAINS", "https://d.example")
	t.Setenv("DOMAIN", "https://e.example")
	t.Setenv("TRUSTED_ORIGINS", "https://c.example")

	if got := CORSOrigins(); !slices.Equal(got, []string{"https://a.example"}) {
		t.Errorf("CORSOrigins() = %v, want the CORS_ALLOWED_ORIGINS value", got)
	}
}

func TestCORSOriginsFallsBackToEveryLegacyName(t *testing.T) {
	for index, key := range CORSOriginKeys[1:] {
		for _, other := range CORSOriginKeys {
			t.Setenv(other, "")
		}
		t.Setenv(key, "https://legacy.example, https://second.example")

		got := CORSOrigins()
		want := []string{"https://legacy.example", "https://second.example"}
		if !slices.Equal(got, want) {
			t.Errorf("fallback %d (%s) = %v, want %v", index, key, got, want)
		}
	}
}

func TestParseEnvironment(t *testing.T) {
	cases := map[string]Environment{
		"production":  Production,
		"PROD":        Production,
		" staging ":   Staging,
		"stage":       Staging,
		"development": Development,
		"":            Development,
		"banana":      Development,
	}

	for input, want := range cases {
		if got := ParseEnvironment(input); got != want {
			t.Errorf("ParseEnvironment(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestHelpers(t *testing.T) {
	t.Setenv("BLANK", "   ")
	t.Setenv("NUMBER", "42")
	t.Setenv("FLAG", "true")
	t.Setenv("SPAN", "90s")
	t.Setenv("ITEMS", " a , ,b ,")

	if got := String("BLANK", "fallback"); got != "fallback" {
		t.Errorf("String on a blank value = %q", got)
	}
	if _, err := Required("BLANK"); err == nil {
		t.Error("Required accepted a blank value")
	}
	if value, err := Int("NUMBER", 0); err != nil || value != 42 {
		t.Errorf("Int = %d, %v", value, err)
	}
	if _, err := Int("ITEMS", 0); err == nil {
		t.Error("Int accepted a non-numeric value")
	}
	if value, err := Bool("FLAG", false); err != nil || !value {
		t.Errorf("Bool = %v, %v", value, err)
	}
	if value, err := Duration("SPAN", 0); err != nil || value != 90*time.Second {
		t.Errorf("Duration = %v, %v", value, err)
	}
	if got := List("ITEMS"); !slices.Equal(got, []string{"a", "b"}) {
		t.Errorf("List = %v, want [a b]", got)
	}
	if got := List("BLANK"); got != nil {
		t.Errorf("List on a blank value = %v, want nil", got)
	}
}

func TestTrustedProxies(t *testing.T) {
	t.Setenv("TRUSTED_PROXIES", "")
	unset, err := TrustedProxies()
	if err != nil {
		t.Fatalf("TrustedProxies: %v", err)
	}
	if len(unset) != len(middleware.DefaultTrustedProxies) {
		t.Fatalf("unset gave %d prefixes, want the default set", len(unset))
	}

	// "none" is spelled out because an empty string is what a half-written
	// deployment config produces, and that must not silently select the
	// strictest setting nobody chose.
	t.Setenv("TRUSTED_PROXIES", "none")
	none, err := TrustedProxies()
	if err != nil {
		t.Fatalf("TrustedProxies: %v", err)
	}
	if none == nil || len(none) != 0 {
		t.Fatalf("none gave %v, want a non-nil empty slice", none)
	}

	t.Setenv("TRUSTED_PROXIES", "10.0.0.0/8, 192.168.1.7")
	listed, err := TrustedProxies()
	if err != nil {
		t.Fatalf("TrustedProxies: %v", err)
	}
	if len(listed) != 2 {
		t.Fatalf("got %d prefixes, want 2", len(listed))
	}

	t.Setenv("TRUSTED_PROXIES", "not-a-network")
	if _, err := TrustedProxies(); err == nil {
		t.Error("garbage was accepted; a bad proxy list must fail at boot, not at the first request")
	}
}

// A deployment should say what it is fronted by, not paste two dozen ranges
// into its environment — and the two forms have to compose.
func TestTrustedProxiesNamedSets(t *testing.T) {
	t.Setenv("TRUSTED_PROXIES", "private,cloudflare")
	got, err := TrustedProxies()
	if err != nil {
		t.Fatalf("TrustedProxies: %v", err)
	}
	want := len(middleware.DefaultTrustedProxies) + len(middleware.CloudflareProxies)
	if len(got) != want {
		t.Fatalf("got %d prefixes, want %d", len(got), want)
	}
	if !middleware.TrustedBy(netip.MustParseAddr("172.70.108.91"), got) {
		t.Error("a Cloudflare edge is not trusted under the cloudflare name")
	}
	if !middleware.TrustedBy(netip.MustParseAddr("10.0.0.3"), got) {
		t.Error("Traefik is not trusted under the private name")
	}

	t.Setenv("TRUSTED_PROXIES", "CloudFlare, 198.51.100.4")
	mixed, err := TrustedProxies()
	if err != nil {
		t.Fatalf("TrustedProxies: %v", err)
	}
	if !middleware.TrustedBy(netip.MustParseAddr("198.51.100.4"), mixed) {
		t.Error("a literal address alongside a name was dropped")
	}
	if middleware.TrustedBy(netip.MustParseAddr("10.0.0.3"), mixed) {
		t.Error("naming cloudflare alone silently included the private ranges")
	}
}
