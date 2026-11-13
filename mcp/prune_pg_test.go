package mcp_test

import (
	"database/sql"
	"fmt"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/FacileStudio/tronc/mcp"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// pendingDB opens TEST_DATABASE_URL on one connection, inside a fresh schema
// holding PendingSchema, and drops the schema when the test ends. It skips
// without TEST_DATABASE_URL.
func pendingDB(t *testing.T) *sql.DB {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	db, err := sql.Open("pgx", url)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	schema := fmt.Sprintf("tronc_mcp_test_%d", time.Now().UnixNano())
	for _, statement := range []string{"CREATE SCHEMA " + schema, "SET search_path TO " + schema, mcp.PendingSchema} {
		if _, err := db.ExecContext(t.Context(), statement); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { _, _ = db.Exec("DROP SCHEMA " + schema + " CASCADE") })
	return db
}

// pruneRow is one seeded action: when it expires, and when it was resolved
// (zero when open), relative to now.
type pruneRow struct {
	id       string
	expires  time.Duration
	resolved time.Duration
}

// seedActions stores one action of u1 per row.
func seedActions(t *testing.T, db *sql.DB, rows []pruneRow) {
	t.Helper()
	now := time.Now()
	for _, row := range rows {
		action := mcp.Action{
			ID: row.id, UserID: "u1", Kind: "delete", Target: row.id, Digest: "d",
			CreatedAt: now.Add(-40 * 24 * time.Hour), ExpiresAt: now.Add(row.expires),
		}
		if err := (mcp.PostgresPending{DB: db}).Create(t.Context(), action); err != nil {
			t.Fatal(err)
		}
		if row.resolved == 0 {
			continue
		}
		if _, err := db.ExecContext(t.Context(), `UPDATE mcp_actions SET resolved_at = $1, outcome = 'done' WHERE id = $2`, now.Add(row.resolved), row.id); err != nil {
			t.Fatal(err)
		}
	}
}

// actionIDs lists the stored action ids in order.
func actionIDs(t *testing.T, db *sql.DB) []string {
	t.Helper()
	rows, err := db.QueryContext(t.Context(), `SELECT id FROM mcp_actions ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return ids
}

// TestPruneSparesOpenAndRecentActions runs Prune against Postgres: an open
// action, one resolved yesterday and one that expired yesterday stay; one
// resolved 31 days ago, though not yet expired, and one that expired 31 days
// ago, though never resolved, go.
func TestPruneSparesOpenAndRecentActions(t *testing.T) {
	db := pendingDB(t)
	day := 24 * time.Hour
	seedActions(t, db, []pruneRow{
		{"open", 7 * day, 0},
		{"resolved-recently", 6 * day, -day},
		{"expired-recently", -day, 0},
		{"resolved-long-ago", day, -31 * day},
		{"expired-long-ago", -31 * day, 0},
	})
	store := mcp.PostgresPending{DB: db}
	if n, err := store.Prune(t.Context(), mcp.DefaultPruneAfter); err != nil || n != 2 {
		t.Fatalf("want 2 actions pruned, got %d %v", n, err)
	}
	if left, want := actionIDs(t, db), []string{"expired-recently", "open", "resolved-recently"}; !slices.Equal(left, want) {
		t.Fatalf("want %v left, got %v", want, left)
	}
	if open, err := store.Open(t.Context(), "u1"); err != nil || len(open) != 1 || open[0].ID != "open" {
		t.Fatalf("want the open action still confirmable, got %v %v", open, err)
	}
}
