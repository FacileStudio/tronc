package conformance_test

import (
	"maps"
	"testing"

	"github.com/FacileStudio/tronc/spa"
)

type inlineScriptInput struct {
	HTML  string            `json:"html"`
	Files map[string]string `json:"files,omitempty"`
}

type inlineScriptWant struct {
	Hashes []string `json:"hashes"`
}

func inlineScriptHashVectors(t *testing.T) []vector {
	t.Helper()
	const boot = "import('/_app/immutable/entry/start.a1.js')"
	page := func(body string) string { return "<!doctype html><html><head>" + body + "</head><body></body></html>" }
	inputs := []struct {
		name  string
		input inlineScriptInput
	}{
		{"one_inline_script", inlineScriptInput{HTML: page("<script>" + boot + "</script>")}},
		{"hashes_sorted", inlineScriptInput{HTML: page("<script>b()</script><script>a()</script>")}},
		{"duplicates_removed", inlineScriptInput{HTML: page("<script>a()</script><script>a()</script>")}},
		{"attributes_allowed", inlineScriptInput{HTML: page(`<script type="module" nonce=x>` + boot + `</script>`)}},
		{"uppercase_tag", inlineScriptInput{HTML: page("<SCRIPT>a()</SCRIPT>")}},
		{"whitespace_kept_in_hash", inlineScriptInput{HTML: page("<script>\n  a()\n</script>")}},
		{"crlf_normalised", inlineScriptInput{HTML: page("<script>\r\n  a()\r\n</script>")}},
		{"lone_cr_normalised", inlineScriptInput{HTML: page("<script>\r  a()\r</script>")}},
		{"closing_tag_with_space", inlineScriptInput{HTML: page("<script>a()</script >")}},
		{"no_scripts", inlineScriptInput{HTML: page("<title>x</title>")}},
		{"src_script_skipped", inlineScriptInput{HTML: page(`<script src="/app.js"></script>`)}},
		{"src_attribute_uppercase_skipped", inlineScriptInput{HTML: page(`<script SRC=/app.js>a()</script>`)}},
		{"empty_script_skipped", inlineScriptInput{HTML: page("<script></script>")}},
		{"quoted_gt_does_not_end_tag", inlineScriptInput{HTML: page(`<script data-x="a>b">a()</script>`)}},
		{"quoted_src_text_is_not_src", inlineScriptInput{HTML: page(`<script data-x='src=/x.js'>a()</script>`)}},
		{"similar_tag_name_ignored", inlineScriptInput{HTML: page("<scripts>a()</scripts><script-x>b()</script-x>")}},
		{"false_closing_tag_skipped", inlineScriptInput{HTML: page("<script>a('</scripts>')</script>")}},
		{"unclosed_script_ignored", inlineScriptInput{HTML: page("<script>a()")}},
		{"unterminated_tag_ignored", inlineScriptInput{HTML: `<script data-x="a()`}},
		{"every_html_file_scanned", inlineScriptInput{HTML: page("<script>a()</script>"), Files: map[string]string{"sheets/index.html": page("<script>b()</script>"), "UPPER.HTML": page("<script>c()</script>")}}},
		{"non_html_files_ignored", inlineScriptInput{HTML: page(""), Files: map[string]string{"app.js": "<script>a()</script>", "legacy.htm": page("<script>b()</script>")}}},
	}

	cases := make([]vector, 0, len(inputs))
	for _, entry := range inputs {
		files := map[string]string{"index.html": entry.input.HTML}
		maps.Copy(files, entry.input.Files)
		hashes, err := spa.InlineScriptHashes(writeTree(t, files))
		if err != nil {
			t.Fatal(err)
		}
		cases = append(cases, vector{entry.name, entry.input, inlineScriptWant{Hashes: append([]string{}, hashes...)}})
	}
	return cases
}
