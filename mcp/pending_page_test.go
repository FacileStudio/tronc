package mcp_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/FacileStudio/tronc/apiref"
	"github.com/FacileStudio/tronc/mcp"
	"github.com/FacileStudio/tronc/mcp/mcptest"
	"github.com/go-chi/chi/v5"
)

// askDelete asks to delete note id from a client that cannot elicit and
// returns the pending action id.
func askDelete(t *testing.T, resource, id string) string {
	t.Helper()
	var out deleteOutput
	session := mcptest.Connect(t, resource, "u1|notes.read,notes.write", mcptest.ClientOptions{})
	mcptest.Decode(t, mcptest.Call(t, session, "notes_delete", map[string]any{"id": id}), &out)
	return out.ConfirmURL[strings.LastIndex(out.ConfirmURL, "=")+1:]
}

// agent is the onPage caller that stands for u1's delegated token.
const agent = "agent"

// onPage calls the confirm page API as user, or as u1's delegated token
// when user is agent.
func onPage(t *testing.T, resource, method, path, user string) (int, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), method, strings.TrimSuffix(resource, "/mcp")+"/api/mcp/pending"+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-User", user)
	if user == agent {
		req.Header.Set("X-User", "u1")
		req.Header.Set(delegated, "1")
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	var body json.RawMessage
	_ = json.NewDecoder(res.Body).Decode(&body)
	return res.StatusCode, string(body)
}

// TestPendingPageConfirmsOnce checks the page lists the action with the
// app's detail, refuses an agent and a missing person, runs the action
// once with the app's result, and answers 404 after.
func TestPendingPageConfirmsOnce(t *testing.T) {
	n, resource := serve(t)
	n.text["a"] = "hello"
	id := askDelete(t, resource, "a")
	if code, _ := onPage(t, resource, http.MethodPost, "/"+id+"/confirm", agent); code != http.StatusForbidden || n.deleted["a"] != 0 {
		t.Fatalf("a delegated token must not confirm, got %d", code)
	}
	if code, _ := onPage(t, resource, http.MethodGet, "/", ""); code != http.StatusUnauthorized {
		t.Fatalf("a request with no person is refused, got %d", code)
	}
	code, list := onPage(t, resource, http.MethodGet, "/", "u1")
	for _, want := range []string{`"id":"` + id + `"`, `"state":"ready"`, `"text":"hello"`, `"summary":"Delete note a?"`, `"client_id":"test-client"`} {
		if code != http.StatusOK || !strings.Contains(list, want) {
			t.Fatalf("want %s in the list, got %d %s", want, code, list)
		}
	}
	if code, body := onPage(t, resource, http.MethodPost, "/"+id+"/confirm", "u1"); code != http.StatusOK || body != `{"status":"deleted","url":"a"}` || n.deleted["a"] != 1 {
		t.Fatalf("want the note deleted once with the app's result, got %d %s", code, body)
	}
	if code, _ := onPage(t, resource, http.MethodPost, "/"+id+"/confirm", "u1"); code != http.StatusNotFound || n.deleted["a"] != 1 {
		t.Fatalf("a confirmation is single use, got %d", code)
	}
}

// TestPendingPageRefusesAChangedTarget checks an edit after the question
// answers 412 and closes the action.
func TestPendingPageRefusesAChangedTarget(t *testing.T) {
	n, resource := serve(t)
	n.text["a"] = "before"
	id := askDelete(t, resource, "a")
	n.text["a"] = "after"
	if code, list := onPage(t, resource, http.MethodGet, "/", "u1"); !strings.Contains(list, `"state":"changed"`) {
		t.Fatalf("want the action shown as changed, got %d %s", code, list)
	}
	if code, _ := onPage(t, resource, http.MethodPost, "/"+id+"/confirm", "u1"); code != http.StatusPreconditionFailed || n.deleted["a"] != 0 {
		t.Fatalf("want 412 and the note kept, got %d", code)
	}
}

// TestPendingPageDismisses checks a dismissal runs nothing, answers
// dismissed, and only the owner may dismiss.
func TestPendingPageDismisses(t *testing.T) {
	n, resource := serve(t)
	n.text["a"] = "text"
	id := askDelete(t, resource, "a")
	if code, _ := onPage(t, resource, http.MethodPost, "/"+id+"/dismiss", "u2"); code != http.StatusNotFound {
		t.Fatalf("another person must not dismiss, got %d", code)
	}
	if code, body := onPage(t, resource, http.MethodPost, "/"+id+"/dismiss", "u1"); code != http.StatusOK || body != `{"status":"dismissed"}` || n.deleted["a"] != 0 {
		t.Fatalf("want dismissed and nothing run, got %d %s", code, body)
	}
	if code, list := onPage(t, resource, http.MethodGet, "/", "u1"); code != http.StatusOK || list != `{"actions":[]}` {
		t.Fatalf("want an empty list, got %d %s", code, list)
	}
}

// TestPendingRoutesDocumentWhatThePageServes checks the documentation names
// every route a nil page mounts, and reflects the app's item with the
// shared fields promoted.
func TestPendingRoutesDocumentWhatThePageServes(t *testing.T) {
	router := chi.NewRouter()
	var page *mcp.PendingPage[noteItem]
	router.Route("/mcp/pending", page.Routes)
	doc := mcp.PendingDoc{List: "Open actions.", Confirm: "Deletes the note.", Dismiss: "Keeps the note.", Changed: "The note changed."}
	module := apiref.Module{Name: "mcp", Description: "Pending actions.", Routes: mcp.PendingRoutes[noteItem](doc)}
	cfg := apiref.Config{Registry: apiref.Registry{Modules: []apiref.Module{module}}}
	if missing := apiref.Undocumented(router, cfg); len(missing) != 0 {
		t.Fatalf("undocumented routes: %v", missing)
	}
	schemas := apiref.OpenAPI(cfg)["components"].(map[string]any)["schemas"].(map[string]any)
	item, ok := schemas["noteItem"].(map[string]any)
	if _, listed := schemas["PendingList_noteItem"]; !ok || !listed {
		t.Fatalf("want PendingList_noteItem and noteItem schemas, got %v", schemas)
	}
	props := item["properties"].(map[string]any)
	for _, field := range []string{"id", "kind", "client_id", "summary", "created_at", "expires_at", "state", "text"} {
		if props[field] == nil {
			t.Fatalf("want %s in the item schema, got %v", field, item)
		}
	}
}
