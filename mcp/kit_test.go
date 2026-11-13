package mcp

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"database/sql/driver"
	"encoding/hex"
	stderrors "errors"
	"log/slog"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/FacileStudio/tronc/errors"
)

// TestStateKeyKeepsTheAppsFormat pins the key every app derived before it
// moved here, so questions signed before an app adopts it still verify.
func TestStateKeyKeepsTheAppsFormat(t *testing.T) {
	want := sha256.Sum256([]byte("courrier-mcp-state:secret"))
	if got := StateKey("courrier", "secret"); string(got) != string(want[:]) {
		t.Fatalf("the state key format changed: %x", got)
	}
	if got := hex.EncodeToString(StateKey("opus", "s3cret")); got != "da630e64c58b3e5aba27eb17e725a008f20b65b06020e090c4b10a9c2735b784" {
		t.Fatalf("the state key format changed: %s", got)
	}
}

// discard is a logger that drops everything.
func discard() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

// limitError stands for porte's forfait.LimitError.
type limitError struct{ key, link string }

func (e *limitError) Error() string                     { return "forfait: plan limit reached" }
func (e *limitError) PlanLimit() (feature, link string) { return e.key, e.link }

// TestFailureMapsPlanLimitsAndOutages checks a PlanLimit names its upgrade
// page, an unsafe page is dropped, an outage keeps its message, and an
// accented first letter is capitalised whole.
func TestFailureMapsPlanLimitsAndOutages(t *testing.T) {
	cases := map[string]struct {
		err  error
		want string
	}{
		"plan limit":         {&limitError{"rooms", "https://sso.example/spaces/1"}, "The plan limit for rooms is reached. Upgrade at https://sso.example/spaces/1"},
		"unsafe page":        {&limitError{"rooms", "javascript:alert(1)"}, "The plan limit for rooms is reached."},
		"wrapped limit":      {fmtWrap(&limitError{"storage", ""}), "The plan limit for storage is reached."},
		"unavailable":        {errors.Unavailable("bulle did not answer"), "Bulle did not answer"},
		"accented":           {errors.Invalid("échec de l'envoi"), "Échec de l'envoi"},
		"accented not_found": {errors.NotFound("élément introuvable"), "Élément introuvable. Call x."},
	}
	for name, c := range cases {
		if got := Failure(c.err, "Call x."); got == nil || got.Error() != c.want {
			t.Errorf("%s: want %q, got %v", name, c.want, got)
		}
	}
}

// fmtWrap wraps err the way a service layer does.
func fmtWrap(err error) error {
	return stderrors.Join(stderrors.New("creating"), err)
}

// TestPageWalksAList checks the offset pager cuts pages, ends with an
// empty cursor, refuses a foreign or negative cursor, and never returns an
// empty page with a next cursor.
func TestPageWalksAList(t *testing.T) {
	items := []int{1, 2, 3, 4, 5}
	var seen []int
	cursor := ""
	for range len(items) {
		page, next, err := Page(items, cursor, 2)
		if err != nil {
			t.Fatal(err)
		}
		seen = append(seen, page...)
		if cursor = next; cursor == "" {
			break
		}
	}
	if !slices.Equal(seen, items) {
		t.Fatalf("want every item once, got %v", seen)
	}
	if page, next, err := Page(items, "", 0); err != nil || len(page) != 1 || next == "" {
		t.Fatalf("a zero size must still advance, got %v %q %v", page, next, err)
	}
	for _, bad := range []string{"!!", EncodeCursor(-1), EncodeCursor("x")} {
		if _, _, err := Page(items, bad, 2); !stderrors.Is(err, ErrCursor) {
			t.Errorf("cursor %q: want ErrCursor, got %v", bad, err)
		}
	}
	if Cursor(5, 5) != "" || Cursor(4, 5) == "" {
		t.Error("the cursor is empty only past the end")
	}
}

// TestWaitingNamesTheLifetime checks the standard text and its lifetime.
func TestWaitingNamesTheLifetime(t *testing.T) {
	c := &Confirmations{cfg: ConfirmConfig{PendingTTL: DefaultPendingTTL}}
	if want := "Nothing happened yet: your client cannot ask the user to confirm. Give the user confirm_url and ask them " +
		"to confirm there. Do not call this tool again for the same target; the request stays open for 7 days."; c.Waiting() != want {
		t.Fatalf("got %q", c.Waiting())
	}
	for ttl, want := range map[time.Duration]string{24 * time.Hour: "1 day", 3 * time.Hour: "3 hours", time.Minute: "1 hour"} {
		if got := lifetime(ttl); got != want {
			t.Errorf("%v: want %q, got %q", ttl, want, got)
		}
	}
}

// pruneExec is an Execer that records the cutoff and answers n rows.
type pruneExec struct {
	cutoff time.Time
	n      int64
	fail   error
}

// ExecContext records the cutoff.
func (p *pruneExec) ExecContext(_ context.Context, query string, args ...any) (sql.Result, error) {
	if !strings.Contains(query, "DELETE FROM mcp_actions") {
		return nil, stderrors.New("unexpected statement")
	}
	p.cutoff = args[0].(time.Time)
	return driver.RowsAffected(p.n), p.fail
}

// TestPrunePendingDeletesOldRows checks the cutoff is olderThan ago, the
// count comes back, and a failure is an internal error.
func TestPrunePendingDeletesOldRows(t *testing.T) {
	exec := &pruneExec{n: 3}
	n, err := PrunePending(t.Context(), exec, time.Hour)
	if err != nil || n != 3 || time.Since(exec.cutoff) < time.Hour || time.Since(exec.cutoff) > time.Hour+time.Minute {
		t.Fatalf("want 3 rows before an hour ago, got %d %v %v", n, exec.cutoff, err)
	}
	if _, err := PrunePending(t.Context(), &pruneExec{fail: stderrors.New("down")}, time.Hour); errors.Status(err) != 500 {
		t.Fatalf("want an internal error, got %v", err)
	}
}

// countingPruner counts calls and cancels after the second.
type countingPruner struct {
	calls  atomic.Int32
	cancel context.CancelFunc
	after  time.Duration
}

// Prune counts the call.
func (c *countingPruner) Prune(_ context.Context, olderThan time.Duration) (int64, error) {
	c.after = olderThan
	if c.calls.Add(1) == 2 {
		c.cancel()
	}
	return 1, nil
}

// TestKeepPrunedRunsAtOnceThenOnTheTicker checks the first prune runs at
// start, the next on the ticker, the default age applies, and the loop ends
// with its context.
func TestKeepPrunedRunsAtOnceThenOnTheTicker(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	store := &countingPruner{cancel: cancel}
	done := make(chan struct{})
	go func() {
		KeepPruned(ctx, store, time.Millisecond, 0, discard())
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("KeepPruned did not stop with its context")
	}
	if store.calls.Load() != 2 || store.after != DefaultPruneAfter {
		t.Fatalf("want two prunes at the default age, got %d %v", store.calls.Load(), store.after)
	}
}

// TestNewAppTakesReadOnlyScopes checks a read-only server can go through
// NewApp with one scope.
func TestNewAppTakesReadOnlyScopes(t *testing.T) {
	cfg := AppConfig{Resource: "https://app.example/mcp", Issuer: "https://i.example", Verifier: acceptAll}
	list := NewTool(ToolSpec{Name: "app_list", Title: "T", Description: "D", Annotations: &Annotations{ReadOnly: true}}, noop)
	app, err := NewApp(cfg, AppSpec{Name: "app", Tools: []Tool{list}, Scopes: Scopes{Read: "app.read"}})
	if err != nil || !slices.Equal(app.Server.opts.Scopes.List(), []string{"app.read"}) || len(app.Server.writes) != 0 {
		t.Fatalf("want a read-only app with one scope, got %v", err)
	}
}
