package mcp

import (
	"context"
	"database/sql"
	stderrors "errors"
	"slices"
	"testing"
)

// TestDigestKeepsTheAppsFormat pins the digest the apps stored before it
// moved here: an open action asked before the move must still confirm.
func TestDigestKeepsTheAppsFormat(t *testing.T) {
	if got := Digest("a", "b"); got != "59b271ae1bbcb1d31d41929817f4b16f" {
		t.Fatalf("the digest format changed: %s", got)
	}
	if Digest("ab") == Digest("a", "b") {
		t.Fatal("field boundaries must change the digest")
	}
}

// TestTargetIDsRefusesNonNumericIDs checks both ids parse, or the app's
// refusal comes back.
func TestTargetIDsRefusesNonNumericIDs(t *testing.T) {
	refusal := stderrors.New("no such target")
	if user, target, err := TargetIDs(Action{UserID: "7", Target: "42"}, refusal); err != nil || user != 7 || target != 42 {
		t.Fatalf("want 7 and 42, got %d %d %v", user, target, err)
	}
	for _, a := range []Action{{UserID: "x", Target: "1"}, {UserID: "1", Target: "1/2"}, {UserID: "", Target: ""}} {
		if _, _, err := TargetIDs(a, refusal); !stderrors.Is(err, refusal) {
			t.Fatalf("want the refusal for %+v, got %v", a, err)
		}
	}
}

// recorder is an Execer that records statements.
type recorder struct {
	args []any
	fail error
}

// ExecContext records the first argument of each statement.
func (r *recorder) ExecContext(_ context.Context, _ string, args ...any) (sql.Result, error) {
	r.args = append(r.args, args[0])
	return nil, r.fail
}

// TestErasePendingDeletesEachPerson checks one statement runs per person and
// a failure stops the erasure with an error.
func TestErasePendingDeletesEachPerson(t *testing.T) {
	exec := &recorder{}
	if err := ErasePending(t.Context(), exec, "1", "2"); err != nil || !slices.Equal(exec.args, []any{"1", "2"}) {
		t.Fatalf("want one delete per person, got %v %v", exec.args, err)
	}
	failing := &recorder{fail: stderrors.New("down")}
	if err := ErasePending(t.Context(), failing, "1", "2"); err == nil || len(failing.args) != 1 {
		t.Fatalf("want the first failure returned, got %v after %d statements", err, len(failing.args))
	}
}

// TestNewAppChecksTheResourceAndKinds checks the resource must be absolute,
// the confirm page sits at the origin's /pending, and a spec without kinds
// has no confirmations.
func TestNewAppChecksTheResourceAndKinds(t *testing.T) {
	cfg := AppConfig{Resource: "/mcp", Issuer: "https://i.example", Verifier: acceptAll, StateKey: []byte("k")}
	if _, err := NewApp(cfg, AppSpec{Name: "app"}); err == nil {
		t.Fatal("a relative resource must be refused")
	}
	cfg.Resource = "https://app.example/mcp"
	readOnly, err := NewApp(cfg, AppSpec{Name: "app"})
	if err != nil || readOnly.Confirmations != nil || readOnly.URL != "https://app.example" {
		t.Fatalf("want a server without confirmations, got %+v %v", readOnly, err)
	}
	kind := Kind{Tool: "app_delete", Fingerprint: func(context.Context, Action) (string, error) { return "", nil }, Run: func(context.Context, Action) error { return nil }}
	if _, err := NewApp(cfg, AppSpec{Name: "app", Kinds: map[string]Kind{"delete": kind}}); err == nil {
		t.Fatal("kinds without a store must be refused")
	}
	cfg.Pending = PostgresPending{}
	app, err := NewApp(cfg, AppSpec{Name: "app", Kinds: map[string]Kind{"delete": kind}})
	if err != nil || app.Confirmations.Link("x") != "https://app.example/pending?action=x" {
		t.Fatalf("want the confirm page at the origin, got %v", err)
	}
	if app.Server.opts.RateLimit.Limit != 2 || app.Server.opts.RateLimit.Burst != DefaultPerMinute {
		t.Fatalf("want %d a minute with as large a burst, got %+v", DefaultPerMinute, app.Server.opts.RateLimit)
	}
}
