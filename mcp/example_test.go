package mcp_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	stderrors "errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/FacileStudio/tronc/errors"
	"github.com/FacileStudio/tronc/mcp"
	"github.com/FacileStudio/tronc/mcp/mcptest"
	"github.com/go-chi/chi/v5"
	"github.com/modelcontextprotocol/go-sdk/auth"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// notes is a toy app: notes in memory, a read tool, a write tool and a
// sensitive delete.
type notes struct {
	mu      sync.Mutex
	text    map[string]string
	deleted map[string]int
	confirm *mcp.Confirmations
}

// noteRef names one note.
type noteRef struct {
	ID string `json:"id" jsonschema:"the note id, from notes_list"`
}

// noteList is the output of notes_list.
type noteList struct {
	IDs []string `json:"ids"`
}

// deleteOutput is the output of notes_delete.
type deleteOutput struct {
	Status     string `json:"status"`
	ConfirmURL string `json:"confirm_url,omitempty"`
	Message    string `json:"message,omitempty"`
}

// noteInput is the input of notes_write.
type noteInput struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

// verify accepts tokens of the form user|scope,scope.
func verify(_ context.Context, token string, _ *http.Request) (*auth.TokenInfo, error) {
	user, scopes, ok := strings.Cut(token, "|")
	if !ok || user == "" {
		return nil, auth.ErrInvalidToken
	}
	return &auth.TokenInfo{UserID: user, Scopes: strings.Split(scopes, ","), Expiration: time.Now().Add(time.Hour),
		Extra: map[string]any{mcp.ExtraClientID: "test-client"}}, nil
}

// fingerprint is the digest of a note, or not_found.
func (n *notes) fingerprint(_ context.Context, a mcp.Action) (string, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	text, ok := n.text[a.Target]
	if !ok {
		return "", errors.NotFound("note not found")
	}
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:]), nil
}

// remove deletes a note.
func (n *notes) remove(_ context.Context, a mcp.Action) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	delete(n.text, a.Target)
	n.deleted[a.Target]++
	return nil
}

// list is notes_list.
func (n *notes) list(context.Context, *sdk.CallToolRequest, struct{}) (*sdk.CallToolResult, noteList, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	out := noteList{IDs: []string{}}
	for id := range n.text {
		out.IDs = append(out.IDs, id)
	}
	return nil, out, nil
}

// write is notes_write.
func (n *notes) write(_ context.Context, _ *sdk.CallToolRequest, in noteInput) (*sdk.CallToolResult, noteRef, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.text[in.ID] = in.Text
	return nil, noteRef{ID: in.ID}, nil
}

// delete is notes_delete, the sensitive tool.
func (n *notes) delete(ctx context.Context, req *sdk.CallToolRequest, in noteRef) (*sdk.CallToolResult, deleteOutput, error) {
	if mcp.Retrying(req) {
		outcome, err := n.confirm.Resume(ctx, req, "delete")
		return nil, deleteOutput{Status: outcome.Status, Message: outcome.Message}, mcp.Failure(err, "Call notes_delete again.")
	}
	result, outcome, err := n.confirm.Request(ctx, req, "delete", in.ID, fmt.Sprintf("Delete note %s?", in.ID))
	if err != nil {
		return nil, deleteOutput{}, mcp.Failure(err, "Call notes_list for the note ids.")
	}
	return result, deleteOutput{Status: outcome.Status, ConfirmURL: outcome.ConfirmURL, Message: outcome.Message}, nil
}

// tools is the curated set.
func (n *notes) tools() []mcp.Tool {
	return []mcp.Tool{
		mcp.NewTool(mcp.ToolSpec{Name: "notes_write", Title: "Write a note", Description: "Create or replace a note.",
			Annotations: &mcp.Annotations{Idempotent: true}}, n.write),
		mcp.NewTool(mcp.ToolSpec{Name: "notes_list", Title: "List notes", Description: "List note ids.",
			Annotations: &mcp.Annotations{ReadOnly: true, Idempotent: true}}, n.list),
		mcp.NewTool(mcp.ToolSpec{Name: "notes_delete", Title: "Delete a note", Description: "Delete a note for good, once the user confirms.",
			Annotations: &mcp.Annotations{Destructive: true, Idempotent: true}}, n.delete),
	}
}

// noteItem is a confirm page entry of the toy app.
type noteItem struct {
	mcp.PendingItem
	Text string `json:"text,omitempty"`
}

// delegated is the header the toy app's sessions use to mark an agent.
const delegated = "X-Delegated"

// pageUser is the toy app's signed-in person: the X-User header, refused
// when X-Delegated is set.
func pageUser(r *http.Request) (string, error) {
	if r.Header.Get(delegated) != "" {
		return "", mcp.ErrDelegated
	}
	return r.Header.Get("X-User"), nil
}

// serve mounts the toy app, its MCP server and its confirm page, and
// returns its resource URL.
func serve(t *testing.T) (*notes, string) {
	t.Helper()
	n := &notes{text: map[string]string{}, deleted: map[string]int{}}
	router := chi.NewRouter()
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)
	app, err := mcp.NewApp(mcp.AppConfig{
		Resource: srv.URL + "/mcp", Issuer: "https://issuer.example", Version: "test", StateKey: []byte("key"),
		Verifier: verify, Pending: mcptest.NewPending(),
	}, mcp.AppSpec{
		Name: "notes", Title: "Notes", Tools: n.tools(),
		Kinds: map[string]mcp.Kind{"delete": {Tool: "notes_delete", Fingerprint: n.fingerprint, Run: n.remove}},
	})
	if err != nil {
		t.Fatal(err)
	}
	n.confirm = app.Confirmations
	app.Server.Mount(router)
	page := &mcp.PendingPage[noteItem]{
		Confirmations: app.Confirmations, Actor: "notes-web", User: pageUser,
		Describe: func(_ context.Context, item mcp.PendingItem, a mcp.Action) noteItem {
			n.mu.Lock()
			defer n.mu.Unlock()
			return noteItem{PendingItem: item, Text: n.text[a.Target]}
		},
		Done: func(_ context.Context, o mcp.Outcome) mcp.PendingResult {
			return mcp.PendingResult{Status: "deleted", URL: o.Action.Target}
		},
	}
	router.Route("/api", func(r chi.Router) { page.Mount(r) })
	return n, srv.URL + "/mcp"
}

// TestConformance runs the kit against the toy app.
func TestConformance(t *testing.T) {
	n, resource := serve(t)
	mcptest.Run(t, mcptest.Config{
		URL: resource,
		Token: func(_ testing.TB, scopes ...string) string {
			if len(scopes) == 0 {
				scopes = []string{"notes.read", "notes.write"}
			}
			return "u1|" + strings.Join(scopes, ",")
		},
		Confirm: []mcptest.ConfirmCase{{Tool: "notes_delete", Setup: n.fresh}},
	})
}

// fresh adds a note for one confirmation case and counts its deletes.
func (n *notes) fresh(testing.TB) (map[string]any, func() int) {
	n.mu.Lock()
	defer n.mu.Unlock()
	id := fmt.Sprintf("n%d", len(n.text)+len(n.deleted))
	n.text[id] = "hello"
	return map[string]any{"id": id}, func() int {
		n.mu.Lock()
		defer n.mu.Unlock()
		return n.deleted[id]
	}
}

// TestChangedTargetIsNotRun checks the changed-target guard on the confirm
// page path: an edit after the question closes the action.
func TestChangedTargetIsNotRun(t *testing.T) {
	n, resource := serve(t)
	n.text["a"] = "before"
	var out deleteOutput
	mcptest.Decode(t, mcptest.Call(t, mcptest.Connect(t, resource, "u1|notes.read,notes.write", mcptest.ClientOptions{}), "notes_delete", map[string]any{"id": "a"}), &out)
	n.text["a"] = "after"
	id := out.ConfirmURL[strings.LastIndex(out.ConfirmURL, "=")+1:]
	if _, err := n.confirm.Confirm(t.Context(), mcp.Caller{UserID: "u1"}, id); !stderrors.Is(err, mcp.ErrChanged) || n.deleted["a"] != 0 {
		t.Fatalf("want ErrChanged and no delete, got %v and %d deletes", err, n.deleted["a"])
	}
	if _, err := n.confirm.Confirm(t.Context(), mcp.Caller{UserID: "u1"}, id); !stderrors.Is(err, mcp.ErrGone) {
		t.Fatalf("a stale action is closed, want ErrGone, got %v", err)
	}
}

// TestConfirmPageIsPersonal checks another person cannot confirm or
// dismiss an action, and its owner can confirm it once.
func TestConfirmPageIsPersonal(t *testing.T) {
	n, resource := serve(t)
	n.text["a"] = "text"
	var out deleteOutput
	mcptest.Decode(t, mcptest.Call(t, mcptest.Connect(t, resource, "u1|notes.read,notes.write", mcptest.ClientOptions{}), "notes_delete", map[string]any{"id": "a"}), &out)
	id := out.ConfirmURL[strings.LastIndex(out.ConfirmURL, "=")+1:]
	if _, err := n.confirm.Confirm(t.Context(), mcp.Caller{UserID: "u2"}, id); !stderrors.Is(err, mcp.ErrGone) {
		t.Fatalf("another person must not confirm, got %v", err)
	}
	if err := n.confirm.Dismiss(t.Context(), mcp.Caller{UserID: "u2"}, id); !stderrors.Is(err, mcp.ErrGone) {
		t.Fatalf("another person must not dismiss, got %v", err)
	}
	open, _ := n.confirm.Open(t.Context(), "u1")
	if len(open) != 1 || n.confirm.State(t.Context(), open[0]) != mcp.StateReady {
		t.Fatalf("want one ready action, got %+v", open)
	}
	if _, err := n.confirm.Confirm(t.Context(), mcp.Caller{UserID: "u1"}, id); err != nil || n.deleted["a"] != 1 {
		t.Fatalf("the owner confirms once, got %v and %d deletes", err, n.deleted["a"])
	}
}

// TestReadOnlyConformance runs the kit against a server whose only scope is
// the read scope, the shape of a search engine's MCP server.
func TestReadOnlyConformance(t *testing.T) {
	n := &notes{text: map[string]string{}, deleted: map[string]int{}}
	router := chi.NewRouter()
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)
	err := mcp.Mount(router, mcp.Options{
		Name: "notes", Title: "Notes", Version: "test", Resource: srv.URL + "/mcp", Issuer: "https://issuer.example",
		Verifier: verify, Scopes: mcp.Scopes{Read: "notes.read"}, Tools: n.tools()[1:2],
	})
	if err != nil {
		t.Fatal(err)
	}
	mcptest.Run(t, mcptest.Config{URL: srv.URL + "/mcp", Token: func(_ testing.TB, _ ...string) string { return "u1|notes.read" }})
}
