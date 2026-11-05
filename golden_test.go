// File responsibilities (golden_test.go):
//   - Golden file test runner that renders each `testdata/golden/*.md` input
//     and compares the structural HTML (`<title>`, `<nav>`, `<article>`)
//     against the `.html` file beside it, supporting `-update` to regenerate
//     expected outputs.
//
// Go vs. Java note on `*_test.go` files:
//
//	Files ending in `_test.go` are compiled ONLY by `go test` and are completely
//	excluded from production builds (`go build` / `go install`), even though
//	they live in the same directory and share `package main` (giving them direct
//	access to unexported functions like `parseContent`). Likewise, the Go tool
//	ignores any directory named `testdata`.
package main

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// update rewrites the golden files from the current output:
//
//	go test -run TestGolden -update
//
// Then read the diff before committing it.
//
// Go testing idiom (custom CLI flags in tests):
//
//	Because `go test` compiles a standalone test binary that uses the standard
//	library `flag` package for its own flags, registering `flag.Bool("update", ...)`
//	in a test file automatically adds `-update` to `go test`! Note that
//	`flag.Bool` returns a pointer (`*bool`), dereferenced below as `*update`.
var update = flag.Bool("update", false, "rewrite golden files from current output")

// TestGolden renders each testdata/golden/*.md and compares the parts that
// come from the Markdown with the .html beside it. The CSS and the rest of
// the page are left out, so style changes don't touch these files.
func TestGolden(t *testing.T) {
	inputs, err := filepath.Glob(filepath.Join("testdata", "golden", "*.md"))
	if err != nil {
		t.Fatal(err)
	}
	if len(inputs) == 0 {
		t.Fatal("no golden inputs found")
	}

	for _, input := range inputs {
		name := strings.TrimSuffix(filepath.Base(input), ".md")
		// Subtests (`t.Run`): creates a named child test (e.g. `TestGolden/math`)
		// that can be run in isolation via `go test -run TestGolden/math`.
		// Unlike JUnit, where `assertEquals` throws an exception that aborts
		// the test method immediately, Go distinguishes `t.Errorf` (log failure
		// and keep running the rest of the test) from `t.Fatalf` (abort the
		// current subtest immediately).
		t.Run(name, func(t *testing.T) {
			md, err := os.ReadFile(input)
			if err != nil {
				t.Fatal(err)
			}
			// served, so links to files stay relative and the golden files
			// don't depend on where the repository is; fixLinks' tests cover
			// the absolute links
			page, err := parseContent(md, input, options{served: true})
			if err != nil {
				t.Fatal(err)
			}
			got := markdownParts(page)

			goldenFile := strings.TrimSuffix(input, ".md") + ".html"
			if *update {
				if err := os.WriteFile(goldenFile, got, 0644); err != nil {
					t.Fatal(err)
				}
				return
			}

			want, err := os.ReadFile(goldenFile)
			if err != nil {
				t.Fatalf("%v (run with -update to create it)", err)
			}
			if !bytes.Equal(got, want) {
				t.Errorf("%s doesn't match %s (run with -update if the change is intended)\ngot:\n%s",
					input, goldenFile, got)
			}
		})
	}
}

// markdownParts returns the title, table of contents, and article of a
// rendered page, one after another
func markdownParts(page []byte) []byte {
	var parts bytes.Buffer
	// Notice `[][2]string`: a slice of fixed-size 2-element string arrays!
	// In Go, `[2]string` is a fixed-length value-type array (2 strings side by
	// side in memory with no slice header), making it a lightweight way to
	// represent a pair of `(openTag, closeTag)` strings.
	for _, tags := range [][2]string{
		{"<title>", "</title>"},
		{"<nav", "</nav>"},
		{"<article", "</article>"},
	} {
		start := bytes.Index(page, []byte(tags[0]))
		end := bytes.Index(page, []byte(tags[1]))
		if start < 0 || end < start {
			continue
		}
		parts.Write(page[start : end+len(tags[1])])
		parts.WriteByte('\n')
	}
	return parts.Bytes()
}
