package spa

import (
	"crypto/sha256"
	"encoding/base64"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func sourceFor(script string) string {
	sum := sha256.Sum256([]byte(script))
	return "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"
}

func TestInlineScriptHashesCoversTheSvelteKitShell(t *testing.T) {
	theme := "\n\t\t\t(function () { document.documentElement.classList.toggle('dark', true); })();\n\t\t"
	boot := "\n\t\t\t\t{\n\t\t\t\t\t__sveltekit_m5uo39 = { base: \"\" };\n\t\t\t\t\tPromise.all([import(\"/_app/immutable/entry/start.js\")]);\n\t\t\t\t}\n\t\t\t"
	page := "<!doctype html><html><head><SCRIPT>" + theme + "</SCRIPT>" +
		"<script type=\"module\" src=\"/_app/x.js\"></script>" +
		"<script data-src=\"no\" async\r\n src='/y.js'>ignored()</script></head>" +
		"<body><div style=\"display: contents\"><script>" + boot + "</script ></div><script></script></body></html>"

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte(page), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "about"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "about", "index.html"), []byte("<script>"+theme+"</script>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "app.js"), []byte("<script>notHtml()</script>"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := InlineScriptHashes(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{sourceFor(theme), sourceFor(boot)}
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("hashes = %v, want %v", got, want)
	}
}

func TestInlineScriptHashesNormalisesNewlines(t *testing.T) {
	got := hashInlineScripts("<script>a();\r\nb();\rc();</script>")
	if len(got) != 1 || got[0] != sourceFor("a();\nb();\nc();") {
		t.Errorf("CRLF was hashed raw: %v", got)
	}
}

func TestInlineScriptHashesReportsAMissingDir(t *testing.T) {
	if _, err := InlineScriptHashes(filepath.Join(t.TempDir(), "absent")); err == nil {
		t.Error("a missing directory returned no error")
	}
}

func TestInlineScriptHashesReadsAttributesLikeTheTokenizer(t *testing.T) {
	cases := map[string][]string{
		`<script data-x="a>b">run()</script>`:                      {sourceFor("run()")},
		`<script data-note='src=/x.js' async>run()</script>`:       {sourceFor("run()")},
		`<script data-x="a>b" src="/x.js">ignored()</script>`:      nil,
		`<script SRC=/x.js>ignored()</script>`:                     nil,
		`<scripty>no()</scripty><script>s="</scripts>";</script >`: {sourceFor(`s="</scripts>";`)},
		`<script type=module>a()</script><script>b()</SCRIPT>`:     {sourceFor("a()"), sourceFor("b()")},
		`<script data-x="never closed>run()</script>`:              nil,
		`<script>unterminated()`:                                   nil,
	}
	for page, want := range cases {
		if got := hashInlineScripts(page); !slices.Equal(got, want) {
			t.Errorf("hashInlineScripts(%q) = %v, want %v", page, got, want)
		}
	}
}
