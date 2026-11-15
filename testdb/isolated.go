package testdb

import (
	"fmt"
	"hash/fnv"
	"os"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"
)

const maxIdentifier = 63

// OpenIsolated gives one test a schema of its own, migrated and dropped when
// the test ends. Use it where Open plus Truncate does not fit: a test that
// calls t.Parallel, a test that must start from an empty database it migrated
// itself, or two runs of one package at the same time (an IDE and a hook).
//
// It costs one full migration per test, so reach for Open and Truncate first.
// The name is unique per call, so concurrent runs never share a schema.
func OpenIsolated(t testing.TB, url string, cfg Config) *gorm.DB {
	t.Helper()
	if cfg.Prefix == "" {
		t.Fatal("testdb: Prefix is required")
	}
	schema := isolatedName(cfg.Prefix, t.Name())
	db, err := openSchema(url, cfg, schema)
	if err != nil {
		t.Fatalf("%v", err)
	}
	t.Cleanup(func() { dropSchema(t, db, schema) })
	return db
}

func openSchema(url string, cfg Config, schema string) (*gorm.DB, error) {
	if cfg.Migrate == nil {
		return nil, fmt.Errorf("testdb: Migrate is required")
	}
	open := cfg.Open
	if open == nil {
		open = defaultOpen
	}

	admin, err := open(withTimeout(url))
	if err != nil {
		return nil, fmt.Errorf("testdb: open %s: %w", EnvVar, err)
	}
	if err := admin.Exec(`CREATE SCHEMA IF NOT EXISTS "` + schema + `"`).Error; err != nil {
		return nil, fmt.Errorf("testdb: create schema %s: %w", schema, err)
	}
	if handle, err := admin.DB(); err == nil {
		_ = handle.Close()
	}

	scoped, err := open(withSearchPath(withTimeout(url), searchPath(schema, cfg.SearchPathTail)))
	if err != nil {
		return nil, fmt.Errorf("testdb: open %s scoped to %s: %w", EnvVar, schema, err)
	}
	if err := cfg.Migrate(scoped); err != nil {
		return nil, fmt.Errorf("testdb: migrate %s: %w", schema, err)
	}
	return scoped, nil
}

func searchPath(schema string, tail []string) string {
	return strings.Join(append([]string{schema}, tail...), ",")
}

func isolatedName(prefix, test string) string {
	hash := fnv.New32a()
	_, _ = fmt.Fprintf(hash, "%s|%d|%d", test, os.Getpid(), time.Now().UnixNano())
	head := fmt.Sprintf("%s_%08x_", sanitize(prefix), hash.Sum32())
	return head + sanitize(test)[:min(len(sanitize(test)), maxIdentifier-len(head))]
}

func dropSchema(t testing.TB, db *gorm.DB, schema string) {
	t.Helper()
	if err := db.Exec(`DROP SCHEMA IF EXISTS "` + schema + `" CASCADE`).Error; err != nil {
		t.Logf("testdb: drop schema %s: %v", schema, err)
	}
	if handle, err := db.DB(); err == nil {
		_ = handle.Close()
	}
}

// LockID is a goose advisory-lock id derived from the schema db is scoped to.
// The lock belongs to the database, not the schema, so test packages that
// migrate at the same time all queue on goose's default id unless each passes
// a distinct one:
//
//	id, err := testdb.LockID(db)
//	cfg := migrate.Config{DB: sqlDB, FS: migrations.FS, LockID: id}
func LockID(db *gorm.DB) (int64, error) {
	var schema string
	if err := db.Raw(`SELECT current_schema()`).Scan(&schema).Error; err != nil {
		return 0, fmt.Errorf("testdb: read current schema: %w", err)
	}
	hash := fnv.New64a()
	_, _ = hash.Write([]byte(schema))
	return int64(hash.Sum64() >> 1), nil
}

func sanitize(name string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_':
			return r
		case r >= 'A' && r <= 'Z':
			return r + 32
		default:
			return '_'
		}
	}, name)
}
