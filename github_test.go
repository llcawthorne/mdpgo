package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMath(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"$x^2$", `<span class="math math-inline">x^2</span>`},
		{"$`a $ b`$", `<span class="math math-inline">a $ b</span>`},
		{"$$x$$", `<span class="math math-display">x</span>`},
		{"$$\nx\n$$", `<span class="math math-display">x</span>`},
		{"$a<b$", `<span class="math math-inline">a&lt;b</span>`},
		// not math: prices, spaces inside the dollars, escapes, line breaks
		{"$5 and $10", "$5 and $10"},
		{"$ x $", "$ x $"},
		{`\$x\$`, "$x$"},
		{"$x\ny$", "$x\ny$"},
		{"$x$5", "$x$5"},
		{"$$", "$$"},
	}

	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			page, err := parseContent([]byte(tc.in+"\n"), "a.md", options{})
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Contains(page, []byte("<p>"+tc.want+"</p>")) {
				t.Errorf("want <p>%s</p> in:\n%s", tc.want, markdownParts(page))
			}
		})
	}
}

func TestScriptsOnlyWhenNeeded(t *testing.T) {
	const katex, mermaid = "katex.render(", "mermaid.run("
	tests := []struct {
		name, in string
		want     []string
		notWant  []string
	}{
		{"plain", "# Hi\n", nil, []string{katex, mermaid}},
		{"math", "$x$\n", []string{katex, "font/woff2;base64,"}, []string{mermaid}},
		{"math block", "```math\nx\n```\n", []string{katex}, []string{mermaid}},
		{"diagram", "```mermaid\ngraph LR\n```\n", []string{mermaid}, []string{katex}},
		{"code that mentions them", "```go\n// $x$ and mermaid\n```\n", nil, []string{katex, mermaid}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			page, err := parseContent([]byte(tc.in), "a.md", options{})
			if err != nil {
				t.Fatal(err)
			}
			for _, s := range tc.want {
				if !bytes.Contains(page, []byte(s)) {
					t.Errorf("page missing %q", s)
				}
			}
			for _, s := range tc.notWant {
				if bytes.Contains(page, []byte(s)) {
					t.Errorf("page has %q", s)
				}
			}
		})
	}
}

func TestFrontMatterError(t *testing.T) {
	page, err := parseContent([]byte("---\nkey: [unclosed\n---\n\n# Hi\n"), "a.md", options{})
	if err != nil {
		t.Fatal(err)
	}
	if want := "<strong>Error in front matter:</strong>"; !bytes.Contains(page, []byte(want)) {
		t.Errorf("missing %q in:\n%s", want, markdownParts(page))
	}
}

func TestFixLinks(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "dot.png"), []byte("\x89PNG\r\n\x1a\n"), 0644); err != nil {
		t.Fatal(err)
	}
	abs := "file://" + filepath.ToSlash(dir)

	tests := []struct {
		name, in, want string
		served         bool
	}{
		{"file link", `<a href="other.md#sec">x</a>`, `<a href="` + abs + `/other.md#sec">x</a>`, false},
		{"folder link keeps its slash", `<a href="docs/">x</a>`, `<a href="` + abs + `/docs/">x</a>`, false},
		{"parent folder", `<a href="../up.md">x</a>`, `<a href="file://` + filepath.ToSlash(filepath.Dir(dir)) + `/up.md">x</a>`, false},
		{"served links stay relative", `<a href="other.md">x</a>`, `<a href="other.md">x</a>`, true},
		{"page anchor", `<a href="#top">x</a>`, `<a href="#top">x</a>`, false},
		{"web link", `<a href="https://example.com/a.md">x</a>`, `<a href="https://example.com/a.md">x</a>`, false},
		{"raw HTML image", `<img src="dot.png">`, `<img src="data:image/png;base64,iVBORw0KGgo=">`, false},
		{"missing image", `<img src="gone.png">`, `<img src="gone.png">`, false},
		{"srcset", `<source srcset="dot.png 2x, gone.png">`, `<source srcset="data:image/png;base64,iVBORw0KGgo= 2x, gone.png">`, false},
		{"dark-only image", `<img src="https://x.test/a.png#gh-dark-mode-only">`,
			`<img src="https://x.test/a.png#gh-dark-mode-only" class="gh-dark-mode-only">`, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := string(fixLinks([]byte(tc.in), dir, tc.served))
			if got != tc.want {
				t.Errorf("got  %s\nwant %s", got, tc.want)
			}
		})
	}
}

func TestTagFilter(t *testing.T) {
	page, err := parseContent([]byte("<textarea>x</textarea>\n\n<kbd>K</kbd> `<script>`\n"), "a.md", options{})
	if err != nil {
		t.Fatal(err)
	}
	body := string(markdownParts(page))
	for _, want := range []string{"&lt;textarea&gt;x&lt;/textarea&gt;", "<kbd>K</kbd>", "<code>&lt;script&gt;</code>"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in:\n%s", want, body)
		}
	}
}
