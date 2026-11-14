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
	"github.com/modelcontextprotocol/go-sdk/auth"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
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

// TestRequestReusesOnlyTheSameClientsAction checks a request from the
// client that opened an action reuses it and names the time it has left,
// while another client of the same person opens its own action, so consent
// given through one client never reaches another.
func TestRequestReusesOnlyTheSameClientsAction(t *testing.T) {
	store := mcptest.NewPending()
	now := time.Now()
	if err := store.Create(t.Context(), mcp.Action{
		ID: "old", UserID: "u1", Kind: "delete", Target: "a", Digest: "d", ClientID: "client-a",
		CreatedAt: now.Add(-5 * 24 * time.Hour), ExpiresAt: now.Add(2*24*time.Hour + time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	confirm, err := mcp.NewConfirmations(mcp.ConfirmConfig{
		Store: store, Key: mcp.StateKey("notes", "secret"), ConfirmURL: "https://notes.example/pending",
		Kinds: map[string]mcp.Kind{"delete": {
			Tool:        "notes_delete",
			Fingerprint: func(context.Context, mcp.Action) (string, error) { return "d", nil },
			Run:         func(context.Context, mcp.Action) error { return nil },
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	ask := func(client string) mcp.Outcome {
		t.Helper()
		req := &sdk.CallToolRequest{
			Params: &sdk.CallToolParamsRaw{},
			Extra:  &sdk.RequestExtra{TokenInfo: &auth.TokenInfo{UserID: "u1", Extra: map[string]any{mcp.ExtraClientID: client}}},
		}
		_, out, err := confirm.Request(t.Context(), req, "delete", "a", "Delete a?")
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	same := ask("client-a")
	if same.Action.ID != "old" || !strings.HasSuffix(same.Message, "stays open for 2 days.") {
		t.Fatalf("want the open action of client-a reused with the time it has left, got %+v %q", same.Action, same.Message)
	}
	other := ask("client-b")
	if other.Action.ID == "old" || other.Action.ClientID != "client-b" {
		t.Fatalf("want client-b to open its own action, got %+v", other.Action)
	}
	if again := ask("client-b"); again.Action.ID != other.Action.ID {
		t.Fatalf("want client-b's open action reused, got %s and %s", other.Action.ID, again.Action.ID)
	}
	if open, _ := confirm.Open(t.Context(), "u1"); len(open) != 2 {
		t.Fatalf("want one open action per client, got %d", len(open))
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
