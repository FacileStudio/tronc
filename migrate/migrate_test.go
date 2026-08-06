package migrate

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/pressly/goose/v3"
)

// stubConnector stands in for a database that is never reachable. sql.OpenDB is
// lazy, so everything up to the first query works against it — which is exactly
// the boundary this package is designed around, and the reason these tests need
// no Postgres. CI has no service containers.
type stubConnector struct{}

func (stubConnector) Connect(context.Context) (driver.Conn, error) {
	return nil, errors.New("stub: no database")
}

func (stubConnector) Driver() driver.Driver { return stubDriver{} }

type stubDriver struct{}

func (stubDriver) Open(string) (driver.Conn, error) {
	return nil, errors.New("stub: no database")
}

func stubDB(t *testing.T) *sql.DB {
	t.Helper()
	db := sql.OpenDB(stubConnector{})
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func oneMigration() fstest.MapFS {
	return fstest.MapFS{
		"00001_baseline.sql": &fstest.MapFile{
			Data: []byte("-- +goose Up\nCREATE TABLE pastes (id text);\n"),
		},
	}
}

func TestCommandIgnoresANormalStart(t *testing.T) {
	cfg := Config{DB: stubDB(t), FS: oneMigration()}

	for _, args := range [][]string{
		{},
		{"/api"},
		{"/api", "serve"},
		{"/api", "healthcheck"},
		{"/api", "migrations"},
	} {
		handled, err := Command(context.Background(), args, cfg)
		if handled {
			t.Errorf("Command(%v) claimed a normal start", args)
		}
		if err != nil {
			t.Errorf("Command(%v) errored on a normal start: %v", args, err)
		}
	}
}

func TestCommandRejectsAnUnknownSubcommand(t *testing.T) {
	cfg := Config{DB: stubDB(t), FS: oneMigration()}

	handled, err := Command(context.Background(), []string{"/api", "migrate", "sideways"}, cfg)
	if !handled {
		t.Fatal("an unknown subcommand fell through to a normal start")
	}
	if err == nil {
		t.Fatal("an unknown subcommand was accepted")
	}
	if !strings.Contains(err.Error(), "sideways") {
		t.Errorf("the error does not name the subcommand: %v", err)
	}
}

// A bad baseline version must be caught before any database round-trip: these
// run against a connector that cannot connect, so reaching the database would
// surface as "stub: no database" instead.
func TestCommandRejectsABadBaselineVersionBeforeTouchingTheDatabase(t *testing.T) {
	cfg := Config{DB: stubDB(t), FS: oneMigration()}

	for _, args := range [][]string{
		{"/api", "migrate", "baseline"},
		{"/api", "migrate", "baseline", "zero"},
		{"/api", "migrate", "baseline", "0"},
		{"/api", "migrate", "baseline", "-3"},
	} {
		handled, err := Command(context.Background(), args, cfg)
		if !handled {
			t.Errorf("Command(%v) fell through to a normal start", args)
			continue
		}
		if err == nil {
			t.Errorf("Command(%v) accepted an unusable version", args)
			continue
		}
		if strings.Contains(err.Error(), "stub: no database") {
			t.Errorf("Command(%v) reached the database before validating: %v", args, err)
		}
	}
}

func TestNewProviderRequiresADatabaseAndAFilesystem(t *testing.T) {
	if _, err := newProvider(Config{FS: oneMigration()}); err == nil {
		t.Error("a nil DB was accepted")
	}
	if _, err := newProvider(Config{DB: stubDB(t)}); err == nil {
		t.Error("a nil FS was accepted")
	}
}

// An app whose migrations directory failed to embed would otherwise start
// cleanly and quietly serve against whatever schema happened to be there.
func TestNewProviderRejectsAFilesystemWithNoMigrations(t *testing.T) {
	_, err := newProvider(Config{DB: stubDB(t), FS: fstest.MapFS{}})
	if err == nil {
		t.Fatal("an empty migrations filesystem was accepted")
	}
	if !errors.Is(err, goose.ErrNoMigrations) {
		t.Errorf("error does not unwrap to goose.ErrNoMigrations: %v", err)
	}
}

func TestSourcesAreReadFromTheFilesystemRoot(t *testing.T) {
	provider, err := newProvider(Config{DB: stubDB(t), FS: oneMigration()})
	if err != nil {
		t.Fatalf("newProvider: %v", err)
	}

	sources := provider.ListSources()
	if len(sources) != 1 {
		t.Fatalf("found %d migrations, want 1", len(sources))
	}
	if sources[0].Version != 1 {
		t.Errorf("version is %d, want 1", sources[0].Version)
	}
	if sources[0].Type != goose.TypeSQL {
		t.Errorf("type is %q, want %q", sources[0].Type, goose.TypeSQL)
	}
	// fs.Sub is the caller's job: a path carrying a directory prefix means the
	// embed.FS was passed in raw and goose would find nothing.
	if sources[0].Path != "00001_baseline.sql" {
		t.Errorf("path is %q; the filesystem was not rooted at the migrations directory", sources[0].Path)
	}
}

func TestLoggerDefaultsRatherThanPanicking(t *testing.T) {
	// goose rejects a nil *slog.Logger outright, so an app that never set one
	// would fail to construct a provider at all.
	if (Config{}).logger() == nil {
		t.Fatal("the default logger is nil")
	}
	custom := slog.Default().With(slog.String("app", "test"))
	if (Config{Logger: custom}).logger() != custom {
		t.Error("a configured logger was replaced")
	}
}
