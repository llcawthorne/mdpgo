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
