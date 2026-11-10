package mcptest

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/FacileStudio/tronc/mcp"
)

// TestPendingClaimsOnce checks that of many racing claims of one action
// exactly one wins, and that another person never claims it.
func TestPendingClaimsOnce(t *testing.T) {
	store := NewPending()
	action := mcp.Action{ID: "a1", UserID: "u1", Kind: "k", ExpiresAt: time.Now().Add(time.Hour)}
	if err := store.Create(t.Context(), action); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Claim(t.Context(), "u2", "a1"); !errors.Is(err, mcp.ErrGone) {
		t.Fatalf("another person must not claim, got %v", err)
	}
	var wins atomic.Int32
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			if _, err := store.Claim(t.Context(), "u1", "a1"); err == nil {
				wins.Add(1)
			}
		})
	}
	wg.Wait()
	if wins.Load() != 1 || store.Outcome("a1") != mcp.OutcomeRunning {
		t.Fatalf("want one winning claim, got %d and outcome %q", wins.Load(), store.Outcome("a1"))
	}
	if err := store.Release(t.Context(), "a1"); err != nil {
		t.Fatal(err)
	}
	if open, _ := store.Open(t.Context(), "u1"); len(open) != 1 {
		t.Fatalf("a released action is open again, got %d", len(open))
	}
}
