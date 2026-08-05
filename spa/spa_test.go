package spa

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func build(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	write := func(name, body string) {
		full := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	write("index.html", "<!doctype html><title>app</title>")
	write("_app/immutable/chunk.DEADBEEF.js", "export const x = 1;")
	write("favicon.ico", "icon")
	write(".env", "SECRET=leaked")
	return dir
}

func get(t *testing.T, handler http.Handler, target string) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, target, nil))
	return recorder
}

func TestServesFilesAndFallsBackForRoutes(t *testing.T) {
	handler := Handler(Config{Dir: build(t)})

	if got := get(t, handler, "/favicon.ico"); got.Code != http.StatusOK || got.Body.String() != "icon" {
		t.Errorf("/favicon.ico = %d %q", got.Code, got.Body.String())
	}

	for _, route := range []string{"/", "/settings", "/spaces/42/edit"} {
		got := get(t, handler, route)
		if got.Code != http.StatusOK || !strings.Contains(got.Body.String(), "<title>app</title>") {
			t.Errorf("%s = %d, want the index document", route, got.Code)
		}
		if cache := got.Header().Get("Cache-Control"); cache != "no-cache" {
			t.Errorf("%s Cache-Control = %q, want no-cache", route, cache)
		}
	}
}

func TestMissingAssetIs404NotTheIndex(t *testing.T) {
	handler := Handler(Config{Dir: build(t)})

	got := get(t, handler, "/_app/immutable/gone.OLDHASH.js")
	if got.Code != http.StatusNotFound {
		t.Fatalf("missing bundle = %d, want 404 — serving index.html here hides the real error behind a MIME failure", got.Code)
	}
	if strings.Contains(got.Body.String(), "<title>") {
		t.Error("the index document was served for a missing .js")
	}
}

func TestHashedAssetsAreCachedForever(t *testing.T) {
	handler := Handler(Config{Dir: build(t)})

	got := get(t, handler, "/_app/immutable/chunk.DEADBEEF.js")
	if got.Code != http.StatusOK {
		t.Fatalf("hashed asset = %d", got.Code)
	}
	if cache := got.Header().Get("Cache-Control"); !strings.Contains(cache, "immutable") {
		t.Errorf("Cache-Control = %q, want immutable", cache)
	}
}

func TestDotfilesAreRefused(t *testing.T) {
	handler := Handler(Config{Dir: build(t)})

	got := get(t, handler, "/.env")
	if got.Code != http.StatusNotFound || strings.Contains(got.Body.String(), "SECRET") {
		t.Errorf("/.env = %d %q — dotfiles must never be served", got.Code, got.Body.String())
	}
}

func TestTraversalCannotEscapeTheDirectory(t *testing.T) {
	dir := build(t)
	secret := filepath.Join(filepath.Dir(dir), "outside.txt")
	if err := os.WriteFile(secret, []byte("OUTSIDE"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	handler := Handler(Config{Dir: dir})

	for _, target := range []string{"/../outside.txt", "/..%2foutside.txt", "/foo/../../outside.txt"} {
		got := get(t, handler, target)
		if strings.Contains(got.Body.String(), "OUTSIDE") {
			t.Errorf("%s escaped the directory", target)
		}
	}
}

func TestAvailable(t *testing.T) {
	if !Available(build(t)) {
		t.Error("a built client was reported unavailable")
	}
	if Available(t.TempDir()) {
		t.Error("an empty directory was reported as a built client")
	}
}

func TestDirFromEnv(t *testing.T) {
	t.Setenv("CLIENT_DIR", "")
	if got := DirFromEnv(); got != DefaultDir {
		t.Errorf("DirFromEnv() = %q, want %q", got, DefaultDir)
	}
	t.Setenv("CLIENT_DIR", "/srv/client")
	if got := DirFromEnv(); got != "/srv/client" {
		t.Errorf("DirFromEnv() = %q", got)
	}
}

func TestNonReadMethodsAreRefusedNotAnsweredWithTheIndex(t *testing.T) {
	handler := Handler(Config{Dir: build(t)})

	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(method, "/ingest", nil))

		if recorder.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s /ingest = %d, want 405 — a 2xx here makes a log shipper drop its payload silently", method, recorder.Code)
		}
		if strings.Contains(recorder.Body.String(), "<title>") {
			t.Errorf("%s /ingest received the index document", method)
		}
		if allow := recorder.Header().Get("Allow"); allow != "GET, HEAD" {
			t.Errorf("%s Allow = %q", method, allow)
		}
	}

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodHead, "/settings", nil))
	if recorder.Code != http.StatusOK {
		t.Errorf("HEAD /settings = %d, want 200", recorder.Code)
	}
}
