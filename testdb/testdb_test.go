package testdb

import (
	"os"
	"strings"
	"testing"

	"gorm.io/gorm"
)

func TestURLReportsWhetherItIsConfigured(t *testing.T) {
	cases := map[string]bool{
		"postgres://localhost/x": true,
		"  ":                     false,
		"":                       false,
	}

	for value, want := range cases {
		t.Setenv(EnvVar, value)
		if _, got := URL(); got != want {
			t.Errorf("URL() with %q reported configured=%v, want %v", value, got, want)
		}
	}
}

func TestSchemaNameIsSafeToInterpolate(t *testing.T) {
	// The schema name is concatenated into DDL, so anything outside
	// [a-z0-9_] has to be gone by the time it gets there.
	name := SchemaName("capsule_test")
	for _, r := range name {
		valid := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_'
		if !valid {
			t.Fatalf("SchemaName produced %q, which contains %q", name, r)
		}
	}
	if !strings.HasPrefix(name, "capsule_test_") {
		t.Errorf("SchemaName produced %q, which does not carry the prefix", name)
	}
}

func TestSchemaNameIsStableAcrossCalls(t *testing.T) {
	// Two calls in one binary must agree, or Open would create one schema and
	// connect to another.
	if SchemaName("x") != SchemaName("x") {
		t.Error("SchemaName is not stable within a binary")
	}
}

func TestSearchPathTravelsInTheConnectionString(t *testing.T) {
	cases := map[string]string{
		"postgres://localhost/db":            "postgres://localhost/db?search_path=s",
		"postgres://localhost/db?sslmode=on": "postgres://localhost/db?sslmode=on&search_path=s",
		"host=localhost dbname=db":           "host=localhost dbname=db search_path=s",
	}

	for input, want := range cases {
		if got := withSearchPath(input, "s"); got != want {
			t.Errorf("withSearchPath(%q) = %q, want %q", input, got, want)
		}
	}
}

// pgx has no default connect timeout, so an unreachable host blocks for ~75s
// per attempt. In a pre-push hook that reads as a hang.
func TestConnectTimeoutIsAlwaysPresent(t *testing.T) {
	for _, input := range []string{
		"postgres://localhost/db",
		"postgres://localhost/db?sslmode=disable",
		"host=localhost dbname=db",
	} {
		if !strings.Contains(withTimeout(input), "connect_timeout") {
			t.Errorf("withTimeout(%q) left no connect timeout", input)
		}
	}
}

func TestConnectTimeoutIsNotOverridden(t *testing.T) {
	input := "postgres://localhost/db?connect_timeout=30"
	if got := withTimeout(input); got != input {
		t.Errorf("withTimeout replaced an explicit timeout: %q", got)
	}
}

func TestOpenRequiresAPrefixAndAMigrateFunc(t *testing.T) {
	migrate := func(*gorm.DB) error { return nil }

	if _, err := Open("postgres://localhost/db", Config{Migrate: migrate}); err == nil {
		t.Error("a missing Prefix was accepted")
	}
	if _, err := Open("postgres://localhost/db", Config{Prefix: "x"}); err == nil {
		t.Error("a missing Migrate was accepted")
	}
}

func TestAnnounceNamesTheEnvironmentVariableAndTheCommand(t *testing.T) {
	reason := SkipReason("sh scripts/postgres-up.sh")
	if !strings.Contains(reason, EnvVar) {
		t.Error("the skip reason does not name the environment variable")
	}
	if !strings.Contains(reason, "sh scripts/postgres-up.sh") {
		t.Error("the skip reason does not name the bootstrap command")
	}
	if SkipReason("") == "" {
		t.Error("an empty bootstrap produced an empty reason")
	}
}

func TestEnvVarIsTheSuiteWideName(t *testing.T) {
	// Casier and Nuage already read this exact name; changing it would strand
	// their CI configuration.
	if EnvVar != "TEST_DATABASE_URL" {
		t.Errorf("EnvVar is %q", EnvVar)
	}
	if LedgerTable != "goose_db_version" {
		t.Errorf("LedgerTable is %q, which no longer matches migrate.TableName", LedgerTable)
	}
}

func TestPackageDoesNotDependOnAnEnvironment(t *testing.T) {
	// A guard against someone adding an os.Getenv default: an unset variable
	// must mean skip, never a silent fallback to a real database.
	os.Unsetenv(EnvVar)
	if value, ok := URL(); ok {
		t.Errorf("URL() invented a default: %q", value)
	}
}
