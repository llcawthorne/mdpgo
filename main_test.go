package main

import (
	"bytes"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// We are using the "golden files" approach where the expected results are
// saved into files that are loaded during the tests to validate output.
// `testdata` is a special directory that is ignored by go build tools.
const (
	inputFile      = "./testdata/test1.md"
	goldenFile     = "./testdata/test1.md.html"
	templateFile   = "./template-fmt.html.tmpl"
	templateOutput = "./testdata/test1-template.md.html"
)

func TestParseContent(t *testing.T) {
	input, err := os.ReadFile(inputFile)
	if err != nil {
		t.Fatal(err)
	}

	result, err := parseContent(input, inputFile, options{})
	if err != nil {
		t.Fatal(err)
	}

	expected, err := os.ReadFile(goldenFile)
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(expected, result) {
		t.Logf("golden:\n%s\n", expected)
		t.Logf("result:\n%s\n", result)
		t.Error("Result content does not match golden file")
	}
}

func TestRun(t *testing.T) {
	var mockStdOut bytes.Buffer

	if err := run(config{filename: inputFile, skipPreview: true}, &mockStdOut); err != nil {
		t.Fatal(err)
	}

	resultFile := strings.TrimSpace(mockStdOut.String())
	defer os.Remove(resultFile)

	result, err := os.ReadFile(resultFile)
	if err != nil {
		t.Fatal(err)
	}

	expected, err := os.ReadFile(goldenFile)
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(expected, result) {
		t.Logf("golden:\n%s\n", expected)
		t.Logf("result:\n%s\n", result)
		t.Error("Result content does not match golden file")
	}
}

func TestRunWithTemplate(t *testing.T) {
	var mockStdOut bytes.Buffer

	if err := run(config{filename: inputFile, skipPreview: true, opts: options{template: templateFile}}, &mockStdOut); err != nil {
		t.Fatal(err)
	}

	resultFile := strings.TrimSpace(mockStdOut.String())
	defer os.Remove(resultFile)

	result, err := os.ReadFile(resultFile)
	if err != nil {
		t.Fatal(err)
	}

	expected, err := os.ReadFile(templateOutput)
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(expected, result) {
		t.Logf("golden:\n%s\n", expected)
		t.Logf("result:\n%s\n", result)
		t.Error("Result content does not match golden file")
	}
}

func TestParseContentHighlightsCode(t *testing.T) {
	input := []byte("```go\nfunc main() {}\n```\n")

	result, err := parseContent(input, inputFile, options{})
	if err != nil {
		t.Fatal(err)
	}

	// Chroma marks "func" as a declaration keyword (kd); the class has to
	// survive sanitizing, and the default template has to style it
	for _, want := range []string{
		`<pre class="chroma">`,
		`<span class="kd">func</span>`,
		`.chroma .kd {`,
	} {
		if !bytes.Contains(result, []byte(want)) {
			t.Errorf("result missing %q:\n%s", want, result)
		}
	}
}

func TestParseContentTitle(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"first h1", "Intro\n\n## Sub\n\n# *Real* `Title`\n\n# Second\n", "Real Title"},
		{"setext h1", "My Doc\n======\n", "My Doc"},
		{"no h1 falls back to file name", "## Only a subheading\n", "notes.md"},
		{"no headings falls back to file name", "just text\n", "notes.md"},
		{"title is escaped", "# a < b & c\n", "a &lt; b &amp; c"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result, err := parseContent([]byte(tc.input), "some/dir/notes.md", options{})
			if err != nil {
				t.Fatal(err)
			}

			want := "<title>" + tc.want + "</title>"
			if !bytes.Contains(result, []byte(want)) {
				t.Errorf("result missing %q:\n%s", want, result)
			}
		})
	}
}

func TestParseContentInlinesLocalImages(t *testing.T) {
	dir := t.TempDir()
	f, err := os.Create(filepath.Join(dir, "dot.png"))
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, image.NewGray(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}
	f.Close()

	input := []byte("![dot](dot.png) ![gone](missing.png) ![remote](https://example.com/x.png)\n")
	result, err := parseContent(input, filepath.Join(dir, "doc.md"), options{})
	if err != nil {
		t.Fatal(err)
	}

	// the local image is embedded; the missing and remote ones are left alone
	for _, want := range []string{
		`src="data:image/png;base64,`,
		`src="missing.png"`,
		`src="https://example.com/x.png"`,
	} {
		if !bytes.Contains(result, []byte(want)) {
			t.Errorf("result missing %q:\n%s", want, result)
		}
	}
}

func TestParseContentTheme(t *testing.T) {
	// the page always has both color sets, so its button can switch them;
	// the theme sets the starting one, which a script applies before the
	// page is drawn, with data-theme as a fallback without scripts
	const (
		darkPage  = `:root[data-theme="dark"] {`
		lightCode = `:root[data-theme="light"][data-code-style="github"] .chroma { background-color: #f7f7f7;`
		darkCode  = `:root[data-theme="dark"][data-code-style="github"] .chroma { color: #e6edf3; background-color: #0d1117;`
	)
	tests := []struct {
		theme    theme
		attr     string
		starting string
	}{
		{auto, `data-theme="light"`, `let choice = "auto";`},
		{light, `data-theme="light"`, `let choice = "light";`},
		{dark, `data-theme="dark"`, `let choice = "dark";`},
	}

	for _, tc := range tests {
		t.Run(string(tc.theme), func(t *testing.T) {
			result, err := parseContent([]byte("# Hi\n"), inputFile, options{theme: tc.theme})
			if err != nil {
				t.Fatal(err)
			}
			for _, s := range []string{"<html " + tc.attr, tc.starting, darkPage, lightCode, darkCode} {
				if !bytes.Contains(result, []byte(s)) {
					t.Errorf("result missing %q", s)
				}
			}
		})
	}
}

func TestParseArgs(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		want    config
		wantErr string
	}{
		{
			name: "defaults",
			args: []string{"a.md"},
			want: config{filename: "a.md", opts: options{theme: auto,
				lightStyle: "github", darkStyle: "github-dark"}},
		},
		{
			name: "short flags",
			args: []string{"-s", "-w", "-d", "-t", "a.md"},
			want: config{filename: "a.md", skipPreview: true, watch: true,
				opts: options{toc: true, theme: dark,
					lightStyle: "github", darkStyle: "github-dark"}},
		},
		{
			name: "long flags after the file",
			args: []string{"a.md", "--skip", "--watch", "--light", "--toc", "--template=x.tmpl"},
			want: config{filename: "a.md", skipPreview: true, watch: true,
				opts: options{template: "x.tmpl", toc: true, theme: light,
					lightStyle: "github", darkStyle: "github-dark"}},
		},
		{
			name: "combined short flags",
			args: []string{"-swl", "a.md"},
			want: config{filename: "a.md", skipPreview: true, watch: true,
				opts: options{theme: light,
					lightStyle: "github", darkStyle: "github-dark"}},
		},
		{
			name: "style without a dark version is used for both",
			args: []string{"--style", "friendly", "a.md"},
			want: config{filename: "a.md", opts: options{theme: auto,
				lightStyle: "friendly", darkStyle: "friendly"}},
		},
		{
			name: "light style brings its dark version",
			args: []string{"--style", "solarized-light", "a.md"},
			want: config{filename: "a.md", opts: options{theme: auto,
				lightStyle: "solarized-light", darkStyle: "solarized-dark"}},
		},
		{
			name: "dark style brings its light version",
			args: []string{"--style=tokyonight-storm", "a.md"},
			want: config{filename: "a.md", opts: options{theme: auto,
				lightStyle: "tokyonight-day", darkStyle: "tokyonight-storm"}},
		},
		{
			name: "output",
			args: []string{"-o", "out/notes.html", "a.md"},
			want: config{filename: "a.md", output: "out/notes.html", opts: options{theme: auto,
				lightStyle: "github", darkStyle: "github-dark"}},
		},
		{
			name: "pdf",
			args: []string{"-pt", "a.md"},
			want: config{filename: "a.md", pdf: true, opts: options{theme: auto, toc: true,
				lightStyle: "github", darkStyle: "github-dark"}},
		},
		{
			name: "a .pdf output implies --pdf",
			args: []string{"--output", "notes.PDF", "a.md"},
			want: config{filename: "a.md", output: "notes.PDF", pdf: true, opts: options{theme: auto,
				lightStyle: "github", darkStyle: "github-dark"}},
		},
		{
			name: "dark-style overrides the dark version",
			args: []string{"--style", "github", "--dark-style", "dracula", "a.md"},
			want: config{filename: "a.md", opts: options{theme: auto,
				lightStyle: "github", darkStyle: "dracula"}},
		},
		{name: "no file", args: []string{"-s"}, wantErr: "exactly one"},
		{name: "two files", args: []string{"a.md", "b.md"}, wantErr: "exactly one"},
		{name: "watch and output", args: []string{"-w", "-o", "x.html", "a.md"}, wantErr: "--watch can't"},
		{name: "watch and pdf", args: []string{"-wp", "a.md"}, wantErr: "--watch can't"},
		{name: "dark and light", args: []string{"-d", "-l", "a.md"}, wantErr: "together"},
		{name: "unknown style", args: []string{"--style", "nope", "a.md"}, wantErr: `unknown style "nope"`},
		{name: "unknown dark style", args: []string{"--dark-style", "nope", "a.md"}, wantErr: `unknown style "nope"`},
		{name: "unknown flag", args: []string{"--nope", "a.md"}, wantErr: "unknown flag"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var stderr bytes.Buffer
			got, err := parseArgs(tc.args, &stderr)

			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want one containing %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Errorf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestParseContentCodeStyleMenu(t *testing.T) {
	tests := []struct {
		name     string
		opts     options
		selected string
		want     []string
	}{
		{
			name:     "a menu style is selected, not added",
			opts:     options{lightStyle: "solarized-light", darkStyle: "solarized-dark"},
			selected: "solarized",
			want:     []string{`<option value="solarized" selected>Solarized</option>`},
		},
		{
			name:     "other styles go first in the menu",
			opts:     options{lightStyle: "friendly", darkStyle: "monokai"},
			selected: "custom",
			want: []string{
				`<option value="custom" selected>friendly / monokai</option>`,
				`<option value="github">GitHub</option>`,
				// friendly's function names are dark blue, monokai's green
				`:root[data-theme="light"][data-code-style="custom"] .chroma .nf { color: #06287e }`,
				`:root[data-theme="dark"][data-code-style="custom"] .chroma .nf { color: #a6e22e }`,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result, err := parseContent([]byte("# Hi\n"), inputFile, tc.opts)
			if err != nil {
				t.Fatal(err)
			}

			want := append(tc.want,
				` data-code-style="`+tc.selected+`"`,
				// every menu choice has its CSS, so the menu can switch to it
				`:root[data-theme="light"][data-code-style="github"] .chroma`,
				`:root[data-theme="dark"][data-code-style="github"] .chroma`,
				`:root[data-theme="light"][data-code-style="darcula"] .chroma`,
				`:root[data-theme="dark"][data-code-style="darcula"] .chroma`,
				`:root[data-theme="light"][data-code-style="solarized"] .chroma`,
				`:root[data-theme="dark"][data-code-style="solarized"] .chroma`,
				`:root[data-theme="light"][data-code-style="xcode"] .chroma`,
				`:root[data-theme="dark"][data-code-style="xcode"] .chroma`,
			)
			for _, s := range want {
				if !bytes.Contains(result, []byte(s)) {
					t.Errorf("result missing %q", s)
				}
			}
		})
	}
}

func TestScopeCSS(t *testing.T) {
	css := "/* Background */ .bg { color: #000 }\n.chroma .k, .chroma .kd { color: red }\n"
	scope := `:root[data-theme="dark"][data-code-style="x"]`
	want := `/* Background */ ` + scope + ` .bg { color: #000 }` + "\n" +
		scope + ` .chroma .k, ` + scope + ` .chroma .kd { color: red }` + "\n"

	if got := scopeCSS(css, "x", dark); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestStyleFamiliesAreKnownStyles(t *testing.T) {
	for _, family := range styleFamilies {
		for _, name := range family {
			if !knownStyle(name) {
				t.Errorf("%q in styleFamilies isn't a Chroma style", name)
			}
		}
	}
}

func TestParseContentCodeBlockColors(t *testing.T) {
	tests := []struct {
		style string
		want  string
	}{
		// github-dark's background matches the page, so GitHub's is used
		{"github-dark", ".chroma { background-color: var(--pre-bg) }"},
		// friendly sets a light background but no text color
		{"friendly", ".chroma { color: #1f2328 }"},
	}

	for _, tc := range tests {
		t.Run(tc.style, func(t *testing.T) {
			result, err := parseContent([]byte("# Hi\n"), inputFile,
				options{theme: dark, darkStyle: tc.style})
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Contains(result, []byte(tc.want)) {
				t.Errorf("result missing %q", tc.want)
			}
		})
	}
}

func TestParseContentTOC(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		toc     bool
		want    []string
		notWant []string
	}{
		{
			name:    "lone h1 is the title, so the list starts at h2",
			input:   "# Title\n\n## One\n\n### One A\n\n## Two\n",
			want:    []string{`<nav class="toc"`, `href="#one"`, `href="#one-a"`, `href="#two"`},
			notWant: []string{`href="#title"`},
		},
		{
			name:  "several h1s are all listed",
			input: "# Part 1\n\n## A\n\n# Part 2\n",
			want:  []string{`href="#part-1"`, `href="#a"`, `href="#part-2"`},
		},
		{
			name:    "closed unless asked for",
			input:   "# Title\n\n## One\n",
			want:    []string{`<nav class="toc"`, `id="toc-toggle"`, `aria-expanded="false"`},
			notWant: []string{`class="toc-open"`},
		},
		{
			name:  "--toc opens it",
			input: "# Title\n\n## One\n",
			toc:   true,
			want:  []string{` data-code-style="github" class="toc-open">`, `aria-expanded="true"`},
		},
		{
			name:    "no headings, no contents or button",
			input:   "just text\n",
			toc:     true,
			notWant: []string{`<nav`, `id="toc-toggle"`, `class="toc-open"`},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result, err := parseContent([]byte(tc.input), inputFile, options{toc: tc.toc})
			if err != nil {
				t.Fatal(err)
			}
			for _, s := range tc.want {
				if !bytes.Contains(result, []byte(s)) {
					t.Errorf("result missing %q:\n%s", s, result)
				}
			}
			for _, s := range tc.notWant {
				if bytes.Contains(result, []byte(s)) {
					t.Errorf("result has %q", s)
				}
			}
		})
	}
}

func TestRunWritesOutputFile(t *testing.T) {
	outName := filepath.Join(t.TempDir(), "notes.html")
	var out bytes.Buffer

	if err := run(config{filename: inputFile, output: outName, skipPreview: true}, &out); err != nil {
		t.Fatal(err)
	}

	if got := strings.TrimSpace(out.String()); got != outName {
		t.Errorf("printed %q, want %q", got, outName)
	}
	page, err := os.ReadFile(outName)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(page, []byte("Test Markdown File")) {
		t.Errorf("output isn't the rendered page:\n%s", page)
	}
}

func TestPDFPath(t *testing.T) {
	for in, want := range map[string]string{
		"notes.md":          "notes.pdf",
		"dir/book.markdown": "dir/book.pdf",
		"README":            "README.pdf",
	} {
		if got := pdfPath(in); got != want {
			t.Errorf("pdfPath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRunMakesPDF(t *testing.T) {
	if _, err := findChrome(); err != nil {
		t.Skip(err)
	}
	if testing.Short() {
		t.Skip("starts Chrome")
	}

	pdfName := filepath.Join(t.TempDir(), "notes.pdf")
	var out bytes.Buffer
	cfg := config{filename: inputFile, output: pdfName, pdf: true, skipPreview: true}

	if err := run(cfg, &out); err != nil {
		t.Fatal(err)
	}

	pdf, err := os.ReadFile(pdfName)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(pdf, []byte("%PDF-")) {
		t.Errorf("%s isn't a PDF; starts %q", pdfName, pdf[:min(len(pdf), 16)])
	}
	// the outline viewers show in their sidebar, built from the headings
	if !bytes.Contains(pdf, []byte("/Outlines")) {
		t.Errorf("%s has no outline", pdfName)
	}
}

func TestSlug(t *testing.T) {
	for in, want := range map[string]string{
		"Hello, World!":                "hello-world",
		"Chapter 2 - A Guessing Game":  "chapter-2---a-guessing-game",
		"Café & Crème":                 "café--crème",
		"日本語の見出し":                      "日本語の見出し",
		"🚀 Emoji launch":               "-emoji-launch",
		"snake_case and kebab-case":    "snake_case-and-kebab-case",
		"Rust's Option<T>: 100% safe?": "rusts-optiont-100-safe",
		"  ":                           "--",
	} {
		if got := slug(in); got != want {
			t.Errorf("slug(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPDFTheme(t *testing.T) {
	defer func(f func() bool) { osPrefersDark = f }(osPrefersDark)

	for _, tc := range []struct {
		asked  theme
		osDark bool
		want   theme
	}{
		{auto, false, light},
		{auto, true, dark},
		{light, true, light},
		{dark, false, dark},
	} {
		osPrefersDark = func() bool { return tc.osDark }
		if got := pdfTheme(tc.asked); got != tc.want {
			t.Errorf("pdfTheme(%s) with OS dark=%v = %s, want %s", tc.asked, tc.osDark, got, tc.want)
		}
	}
}
