package joblock_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/FacileStudio/tronc/joblock"
	_ "github.com/jackc/pgx/v5/stdlib"
)

const fast = 20 * time.Millisecond

func openDB(t *testing.T) *sql.DB {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	db, err := sql.Open("pgx", url)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(10)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func uniqueKey(t *testing.T) string {
	return fmt.Sprintf("%s-%d", t.Name(), time.Now().UnixNano())
}

func lockFree(t *testing.T, db *sql.DB, key string) bool {
	t.Helper()
	conn, err := db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	var got bool
	if err := conn.QueryRowContext(t.Context(), `SELECT pg_try_advisory_lock(hashtextextended($1, 0))`, "joblock:"+key).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got {
		_, _ = conn.ExecContext(t.Context(), `SELECT pg_advisory_unlock(hashtextextended($1, 0))`, "joblock:"+key)
	}
	return got
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

type replica struct {
	cancel  context.CancelFunc
	stopped chan struct{}
	runs    atomic.Int32
}

func startReplica(t *testing.T, db *sql.DB, key string, active, peak *atomic.Int32) *replica {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	r := &replica{cancel: cancel, stopped: make(chan struct{})}
	go func() {
		defer close(r.stopped)
		_ = joblock.Hold(ctx, db, key, func(ctx context.Context) {
			r.runs.Add(1)
			now := active.Add(1)
			for {
				old := peak.Load()
				if now <= old || peak.CompareAndSwap(old, now) {
					break
				}
			}
			<-ctx.Done()
			active.Add(-1)
		}, joblock.WithRetry(fast), joblock.WithCheck(fast))
	}()
	t.Cleanup(func() {
		cancel()
		<-r.stopped
	})
	return r
}

func TestInvalidArguments(t *testing.T) {
	run := func(context.Context) {}
	if err := joblock.Hold(t.Context(), nil, "k", run); err == nil {
		t.Fatal("nil db accepted")
	}
	if err := joblock.Hold(t.Context(), &sql.DB{}, "", run); err == nil {
		t.Fatal("empty key accepted")
	}
	if err := joblock.Hold(t.Context(), &sql.DB{}, "k", nil); err == nil {
		t.Fatal("nil run accepted")
	}
}

func TestTwoHoldersNeverRunAtOnce(t *testing.T) {
	db := openDB(t)
	key := uniqueKey(t)
	var active, peak atomic.Int32
	a := startReplica(t, db, key, &active, &peak)
	b := startReplica(t, db, key, &active, &peak)
	eventually(t, "one holder", func() bool { return active.Load() == 1 })
	time.Sleep(20 * fast)
	if got := active.Load(); got != 1 {
		t.Fatalf("active = %d, want 1", got)
	}
	holder, waiter := a, b
	if a.runs.Load() == 0 {
		holder, waiter = b, a
	}
	if waiter.runs.Load() != 0 {
		t.Fatal("both replicas ran")
	}
	holder.cancel()
	<-holder.stopped
	eventually(t, "the waiter to take over", func() bool { return waiter.runs.Load() == 1 })
	if got := peak.Load(); got != 1 {
		t.Fatalf("peak concurrency = %d, want 1", got)
	}
}

func TestReleaseOnCancel(t *testing.T) {
	db := openDB(t)
	key := uniqueKey(t)
	var active, peak atomic.Int32
	r := startReplica(t, db, key, &active, &peak)
	eventually(t, "the lock to be held", func() bool { return active.Load() == 1 })
	if lockFree(t, db, key) {
		t.Fatal("lock free while held")
	}
	r.cancel()
	<-r.stopped
	if !lockFree(t, db, key) {
		t.Fatal("lock still held after cancel")
	}
}

func TestDifferentKeysAreIndependent(t *testing.T) {
	db := openDB(t)
	var active, peak atomic.Int32
	startReplica(t, db, uniqueKey(t)+"-a", &active, &peak)
	startReplica(t, db, uniqueKey(t)+"-b", &active, &peak)
	eventually(t, "both keys held", func() bool { return active.Load() == 2 })
}

func TestConnectionDropCancelsRunAndHandsOver(t *testing.T) {
	db := openDB(t)
	key := uniqueKey(t)
	var active, peak atomic.Int32
	a := startReplica(t, db, key, &active, &peak)
	eventually(t, "the lock to be held", func() bool { return active.Load() == 1 })
	_, err := db.ExecContext(t.Context(), `SELECT pg_terminate_backend(pid) FROM pg_locks WHERE locktype = 'advisory' AND granted AND pid <> pg_backend_pid() AND objid = (hashtextextended($1, 0) & 4294967295)::oid`, "joblock:"+key)
	if err != nil {
		t.Fatal(err)
	}
	eventually(t, "the holder to see the drop and win the lock again", func() bool { return a.runs.Load() == 2 })
	if got := peak.Load(); got != 1 {
		t.Fatalf("peak concurrency = %d, want 1", got)
	}
}

func TestReturnsWhenRunReturns(t *testing.T) {
	db := openDB(t)
	key := uniqueKey(t)
	done := make(chan error, 1)
	go func() {
		done <- joblock.Hold(t.Context(), db, key, func(context.Context) {}, joblock.WithRetry(fast), joblock.WithCheck(fast))
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Hold did not return after run returned")
	}
	if !lockFree(t, db, key) {
		t.Fatal("lock still held after run returned")
	}
}
