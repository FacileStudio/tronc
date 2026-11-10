package conformance_test

import (
	"net/http/httptest"
	"testing"

	"github.com/FacileStudio/tronc/spa"
)

type spaInput struct {
	Method string            `json:"method"`
	Path   string            `json:"path"`
	Files  map[string]string `json:"files"`
}

type spaWant struct {
	Status  int               `json:"status"`
	Served  string            `json:"served,omitempty"`
	Body    string            `json:"body,omitempty"`
	Headers map[string]string `json:"headers"`
}

func spaVectors(t *testing.T) []vector {
	t.Helper()
	build := map[string]string{
		"index.html":                       "<!doctype html><title>app</title>",
		"app.css":                          "body{margin:0}",
		"_app/immutable/entry/start.a1.js": "export const start=1",
		"assets/logo.b2.svg":               "<svg xmlns=\"http://www.w3.org/2000/svg\"/>",
		"docs/guide.json":                  "{\"guide\":true}",
		".env":                             "SECRET=1",
	}
	withoutIndex := map[string]string{"app.css": "body{margin:0}"}
	inputs := []struct {
		name  string
		input spaInput
	}{
		{"root_serves_index", spaInput{"GET", "/", build}},
		{"history_fallback", spaInput{"GET", "/sheets/42/edit", build}},
		{"history_fallback_head", spaInput{"HEAD", "/sheets/42", build}},
		{"directory_falls_back", spaInput{"GET", "/docs", build}},
		{"real_file", spaInput{"GET", "/app.css", build}},
		{"immutable_asset", spaInput{"GET", "/_app/immutable/entry/start.a1.js", build}},
		{"immutable_assets_prefix", spaInput{"GET", "/assets/logo.b2.svg", build}},
		{"nested_real_file", spaInput{"GET", "/docs/guide.json", build}},
		{"missing_asset_with_extension", spaInput{"GET", "/_app/immutable/entry/gone.js", build}},
		{"missing_file_with_extension", spaInput{"GET", "/robots.txt", build}},
		{"dotfile_hidden", spaInput{"GET", "/.env", build}},
		{"index_redirects_to_directory", spaInput{"GET", "/index.html", build}},
		{"traversal_contained", spaInput{"GET", "/../../etc/passwd", build}},
		{"post_refused", spaInput{"POST", "/sheets", build}},
		{"post_to_real_file_refused", spaInput{"POST", "/app.css", build}},
		{"delete_refused", spaInput{"DELETE", "/", build}},
		{"no_index_no_fallback", spaInput{"GET", "/sheets", withoutIndex}},
	}

	cases := make([]vector, 0, len(inputs))
	for _, entry := range inputs {
		handler := spa.Handler(spa.Config{Dir: writeTree(t, entry.input.Files)})
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(entry.input.Method, entry.input.Path, nil))
		want := spaWant{
			Status:  recorder.Code,
			Headers: pickHeaders(recorder.Header(), "Allow", "Cache-Control", "Content-Type", "Location"),
		}
		want.Served = servedFile(entry.input.Files, recorder.Body.String())
		if want.Served == "" {
			want.Body = recorder.Body.String()
		}
		cases = append(cases, vector{entry.name, entry.input, want})
	}
	return cases
}

func servedFile(files map[string]string, body string) string {
	for name, content := range files {
		if body != "" && content == body {
			return name
		}
	}
	return ""
}
