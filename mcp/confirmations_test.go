package mcp_test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/FacileStudio/tronc/mcp"
	"github.com/FacileStudio/tronc/mcp/mcptest"
)

// TestRequestReusesTheOpenAction checks a second ask about the same note,
// unchanged, returns the open action with the standard text, and an edit
// in between opens a new one.
func TestRequestReusesTheOpenAction(t *testing.T) {
	n, resource := serve(t)
	n.text["a"] = "hello"
	first := askDelete(t, resource, "a")
	if again := askDelete(t, resource, "a"); again != first {
		t.Fatalf("want the open action %s again, got %s", first, again)
	}
	n.text["a"] = "edited"
	if changed := askDelete(t, resource, "a"); changed == first {
		t.Fatal("an edited note must open a new action")
	}
	var out deleteOutput
	session := mcptest.Connect(t, resource, "u1|notes.read,notes.write", mcptest.ClientOptions{})
	mcptest.Decode(t, mcptest.Call(t, session, "notes_delete", map[string]any{"id": "a"}), &out)
	if !strings.HasPrefix(out.Message, "Nothing happened yet") || !strings.HasSuffix(out.Message, "7 days.") {
		t.Fatalf("want the standard waiting text, got %q", out.Message)
	}
}

// TestOpenStopsAtTheLimit checks the confirm page lists at most OpenLimit
// actions, newest first, and says the list is truncated.
func TestOpenStopsAtTheLimit(t *testing.T) {
	store := mcptest.NewPending()
	now := time.Now()
	for i := range mcp.OpenLimit + 5 {
		action := mcp.Action{
			ID: fmt.Sprintf("a%03d", i), UserID: "u1", Kind: "delete", Target: fmt.Sprint(i),
			CreatedAt: now.Add(time.Duration(i) * time.Second), ExpiresAt: now.Add(time.Hour),
		}
		if err := store.Create(t.Context(), action); err != nil {
			t.Fatal(err)
		}
	}
	confirm, err := mcp.NewConfirmations(mcp.ConfirmConfig{
		Store: store, Key: mcp.StateKey("notes", "secret"), ConfirmURL: "https://notes.example/pending",
		Kinds: map[string]mcp.Kind{"delete": {
			Tool:        "notes_delete",
			Fingerprint: func(context.Context, mcp.Action) (string, error) { return "", nil },
			Run:         func(context.Context, mcp.Action) error { return nil },
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	actions, truncated, err := confirm.List(t.Context(), "u1")
	if err != nil || !truncated || len(actions) != mcp.OpenLimit || actions[0].ID != fmt.Sprintf("a%03d", mcp.OpenLimit+4) {
		t.Fatalf("want the %d newest and truncated, got %d %v %v", mcp.OpenLimit, len(actions), truncated, err)
	}
	page := &mcp.PendingPage[mcp.PendingItem]{
		Confirmations: confirm, User: func(*http.Request) (string, error) { return "u1", nil },
		Describe: func(_ context.Context, item mcp.PendingItem, _ mcp.Action) mcp.PendingItem { return item },
	}
	list, err := page.List(t.Context(), "u1")
	if err != nil || !list.Truncated || len(list.Actions) != mcp.OpenLimit {
		t.Fatalf("want a truncated page of %d, got %d %v %v", mcp.OpenLimit, len(list.Actions), list.Truncated, err)
	}
}
