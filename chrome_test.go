// File responsibilities (chrome_test.go):
//   - End-to-end headless Chrome tests verifying that client-side KaTeX and
//     Mermaid JavaScript execute and render formulas and diagrams in the DOM
//     without errors.
package main

import (
	"context"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestRendersInChrome loads golden pages in headless Chrome and checks that
// their scripts did their work, which the golden files can't show: every
// formula typeset by KaTeX without errors, and every diagram drawn by Mermaid
func TestRendersInChrome(t *testing.T) {
	chrome, err := findChrome()
	if err != nil {
		t.Skip(err)
	}
	// `testing.Short()` is true when `go test -short` is passed on the command
	// line, letting slow integration tests opt out during fast test runs.
	if testing.Short() {
		t.Skip("starts Chrome")
	}

	tests := []struct {
		file  string
		theme theme
	}{
		{"math.md", light},
		{"mermaid.md", light},
		{"mermaid.md", dark},
		{"github.md", dark},
	}
	for _, tc := range tests {
		t.Run(tc.file+"/"+string(tc.theme), func(t *testing.T) {
			input := filepath.Join("testdata", "golden", tc.file)
			md, err := os.ReadFile(input)
			if err != nil {
				t.Fatal(err)
			}
			page, err := parseContent(md, input, options{theme: tc.theme})
			if err != nil {
				t.Fatal(err)
			}
			wantMath := strings.Count(string(page), `class="math math-`)
			wantDiagrams := strings.Count(string(page), `<pre class="mermaid">`)

			dom := dumpDOM(t, chrome, page)
			// just the article: the page's embedded libraries mention the
			// errors looked for below
			_, root, _ := strings.Cut(dom, "<html")
			root, _, _ = strings.Cut(root, ">")
			start := strings.Index(dom, `<article class="markdown-body">`)
			end := strings.LastIndex(dom, "</article>")
			if start < 0 || end < start {
				t.Fatal("no article in the page")
			}
			article := dom[start:end]

			if got := strings.Count(article, `<span class="katex">`); got != wantMath {
				t.Errorf("%d formulas typeset, want %d", got, wantMath)
			}
			// parse errors get a class; unknown commands are drawn in red
			if strings.Contains(article, `class="katex-error"`) || strings.Contains(article, `mathcolor="#cc0000"`) {
				t.Errorf("KaTeX reported an error")
			}
			// Mermaid draws a diagram it can't parse as an "error" diagram
			drawn := strings.Count(article, "aria-roledescription=") -
				strings.Count(article, `aria-roledescription="error"`)
			if got := drawn; got != wantDiagrams {
				t.Errorf("%d diagrams drawn, want %d", got, wantDiagrams)
			}
			if strings.Contains(article, "Syntax error") {
				t.Errorf("Mermaid reported a syntax error")
			}
			if want := `data-theme="` + string(tc.theme) + `"`; !strings.Contains(root, want) {
				t.Errorf("page isn't in its %s theme", tc.theme)
			}
		})
	}
}

// dumpDOM loads page in headless Chrome and returns the document as its
// scripts leave it
func dumpDOM(t *testing.T, chrome string, page []byte) string {
	// `t.Helper()` marks this function as a test helper so any `t.Fatal` inside
	// it reports the file and line number of the *caller* in test failure output.
	t.Helper()
	// `t.TempDir()` creates a unique temporary directory that `testing`
	// automatically removes when the test (and all its subtests) complete.
	dir := t.TempDir()
	name := filepath.Join(dir, "page.html")
	if err := os.WriteFile(name, page, 0644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	pageURL := url.URL{Scheme: "file", Path: filepath.ToSlash(name)}
	out, err := exec.CommandContext(ctx, chrome,
		"--headless", "--disable-gpu", "--no-first-run",
		"--user-data-dir="+filepath.Join(dir, "profile"),
		"--virtual-time-budget=10000",
		"--dump-dom", pageURL.String(),
	).Output()
	if err != nil {
		t.Fatalf("chrome: %v", err)
	}
	return string(out)
}
