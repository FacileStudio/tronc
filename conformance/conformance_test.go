// Package conformance_test pins what tronc does today in JSON vectors that a
// port to another language reads as its specification.
//
// Each subject builds its cases from the real packages and compares them with
// conformance/vectors/<subject>.json. Run `go test ./conformance -update` to
// rewrite the files after a deliberate behaviour change.
package conformance_test

import (
	"bytes"
	"encoding/json"
	"flag"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	source  = "github.com/FacileStudio/tronc@871cd90"
	release = "github.com/FacileStudio/tronc@v0.23.0"
)

var update = flag.Bool("update", false, "rewrite conformance/vectors from the code")

type vector struct {
	Name  string `json:"name"`
	Input any    `json:"input"`
	Want  any    `json:"want"`
}

type vectorFile struct {
	Source  string   `json:"source"`
	Subject string   `json:"subject"`
	Cases   []vector `json:"cases"`
}

var subjects = map[string]func(*testing.T) []vector{
	"errors":               errorVectors,
	"json_decode":          jsonDecodeVectors,
	"health":               healthVectors,
	"request_log":          requestLogVectors,
	"security_headers":     securityHeaderVectors,
	"client_ip":            clientIPVectors,
	"spa":                  spaVectors,
	"inline_script_hashes": inlineScriptHashVectors,
	"mcp_http":             mcpHTTPVectors,
	"mcp_rate_limit":       mcpRateLimitVectors,
	"mcp_digest":           mcpDigestVectors,
	"mcp_state_key":        mcpStateKeyVectors,
	"mcp_failure":          mcpFailureVectors,
	"bulle_errors":         bulleErrorVectors,
	"bulle_paths":          bullePathVectors,
}

// sources names the release that added a subject; the others keep the
// commit they were first cut from, so their files stay byte-identical.
var sources = map[string]string{
	"mcp_http":       release,
	"mcp_rate_limit": release,
	"mcp_digest":     release,
	"mcp_state_key":  release,
	"mcp_failure":    release,
	"bulle_errors":   release,
	"bulle_paths":    release,
}

func TestVectors(t *testing.T) {
	for subject, build := range subjects {
		t.Run(subject, func(t *testing.T) {
			compare(t, subject, build(t))
		})
	}
}

func TestNoStrayVectors(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join("vectors", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range paths {
		subject := strings.TrimSuffix(filepath.Base(path), ".json")
		if _, ok := subjects[subject]; !ok {
			t.Errorf("%s has no subject building it", path)
		}
	}
}

func compare(t *testing.T, subject string, cases []vector) {
	t.Helper()
	from := source
	if tag, ok := sources[subject]; ok {
		from = tag
	}
	got := encode(t, vectorFile{Source: from, Subject: subject, Cases: cases})
	path := filepath.Join("vectors", subject+".json")
	if *update {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run go test ./conformance -update)", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s differs from the code: %s", path, firstDifference(t, got, want))
	}
}

func encode(t *testing.T, value any) []byte {
	t.Helper()
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func firstDifference(t *testing.T, got, want []byte) string {
	t.Helper()
	var gotFile, wantFile struct {
		Cases []json.RawMessage `json:"cases"`
	}
	if json.Unmarshal(got, &gotFile) != nil || json.Unmarshal(want, &wantFile) != nil {
		return "the file is not valid JSON"
	}
	for i := range min(len(gotFile.Cases), len(wantFile.Cases)) {
		if !bytes.Equal(gotFile.Cases[i], wantFile.Cases[i]) {
			return "case " + string(gotFile.Cases[i])
		}
	}
	if len(gotFile.Cases) != len(wantFile.Cases) {
		return "the case count changed"
	}
	return "the envelope changed"
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func pickHeaders(header map[string][]string, names ...string) map[string]string {
	picked := map[string]string{}
	for _, name := range names {
		if values, ok := header[name]; ok {
			picked[name] = strings.Join(values, ", ")
		}
	}
	return picked
}

func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}
