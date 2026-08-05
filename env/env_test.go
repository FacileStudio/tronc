package env

import (
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
