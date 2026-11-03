package main

import (
	"bytes"
	"cmp"
	_ "embed"
	"fmt"
	"html/template"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	chromahtml "github.com/alecthomas/chroma/v2/formatters/html"
	"github.com/microcosm-cc/bluemonday"
	"github.com/yuin/goldmark"
	emoji "github.com/yuin/goldmark-emoji"
	highlighting "github.com/yuin/goldmark-highlighting/v2"
	meta "github.com/yuin/goldmark-meta"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer/html"
	"github.com/yuin/goldmark/text"
	"go.abhg.dev/goldmark/toc"
)

// defaultTemplate lays out the page: a top bar, the table of contents, and
// the article
//
//go:embed page.html.tmpl
var defaultTemplate string

// options control how a page is rendered
type options struct {
	template   string // alternate template file; "" for the default
	theme      theme
	lightStyle string // Chroma style for code in light mode
	darkStyle  string // Chroma style for code in dark mode
	toc        bool   // start with the table of contents open
	served     bool   // the page is served, so relative links are left for the server
}

// content represents the template data model passed to page.html.tmpl or an alternate template
type content struct {
	Title     string
	Theme     theme // starting colors: auto, light, or dark
	CSS       template.CSS
	Styles    []codeStyle // choices for the code style menu
	CodeStyle string      // ID of the chosen one
	TOC       template.HTML
	TOCOpen   bool
	Body      template.HTML

	// libraries for the page's math and diagrams, if it has any
	KaTeXCSS template.CSS
	KaTeX    template.JS
	Mermaid  template.JS
}

// md converts GitHub Flavored Markdown to HTML, with GitHub's additions:
// footnotes, :emoji:, front matter, math, alerts, and Mermaid diagrams.
// Fenced code blocks are highlighted with CSS classes (styled by stylesheet)
// rather than inline styles, since the sanitizer strips style attributes.
// Raw HTML is passed through, like on GitHub, for policy to make safe.
var md = goldmark.New(
	goldmark.WithRendererOptions(html.WithUnsafe()),
	goldmark.WithExtensions(
		// GFM, with table alignment as align attributes, as GitHub writes
		// them, rather than style attributes, which policy removes
		extension.Linkify,
		extension.NewTable(extension.WithTableCellAlignMethod(extension.TableCellAlignAttribute)),
		extension.Strikethrough,
		extension.TaskList,
		extension.Footnote,
		emoji.Emoji,
		meta.Meta,
		githubExtras{},
		highlighting.NewHighlighting(
			highlighting.WithStyle(defaultStyle),
			highlighting.WithFormatOptions(chromahtml.WithClasses(true)),
		),
	),
)

// policy sanitizes the rendered HTML, keeping what md adds, and the raw HTML
// GitHub allows
var policy = func() *bluemonday.Policy {
	p := bluemonday.UGCPolicy()
	// keep the classes Chroma puts on highlighted code blocks
	p.AllowAttrs("class").Matching(bluemonday.SpaceSeparatedTokens).OnElements("pre", "code", "span")
	// keep GFM task list checkboxes
	p.AllowAttrs("type").Matching(regexp.MustCompile(`^checkbox$`)).OnElements("input")
	p.AllowAttrs("checked", "disabled").Matching(regexp.MustCompile(`^$`)).OnElements("input")
	p.AllowDataURIImages()
	// heading ids from addHeadingIDs, which can be all non-ASCII; the
	// default rule for ids wants at least one ASCII letter or digit
	p.AllowAttrs("id").Matching(regexp.MustCompile(`^[\p{L}\p{M}\p{N}_-]+$`)).
		OnElements("h1", "h2", "h3", "h4", "h5", "h6")
	// keyboard keys, which GitHub allows too
	p.AllowElements("kbd")
	// alignment, as in <p align="center"> around a logo
	p.AllowAttrs("align").Matching(regexp.MustCompile(`^(?i:left|center|right|justify)$`)).
		OnElements("p", "div", "img", "table", "td", "th", "h1", "h2", "h3", "h4", "h5", "h6")
	// images that change with the color scheme
	p.AllowElements("picture")
	p.AllowAttrs("srcset", "media", "type", "width", "height").OnElements("source")
	// footnotes
	p.AllowAttrs("class").Matching(regexp.MustCompile(`^footnote-(ref|backref)$`)).OnElements("a")
	p.AllowAttrs("role").Matching(regexp.MustCompile(`^doc-[a-z]+$`)).OnElements("a", "div")
	// footnotes section, display math, and alerts
	p.AllowAttrs("class").Matching(regexp.MustCompile(`^(footnotes|math math-display)$`)).OnElements("div")
	p.AllowAttrs("class").Matching(regexp.MustCompile(
		`^markdown-alert markdown-alert-(note|tip|important|warning|caution)$`)).OnElements("blockquote")
	p.AllowAttrs("class").Matching(regexp.MustCompile(`^markdown-alert-title$`)).OnElements("p")
	p.AllowAttrs("class").Matching(regexp.MustCompile(`^front-matter$`)).OnElements("table")
	return p
}()

// parseContent renders the Markdown input as a full HTML page. filename is
// where input came from: it's the title if there's no top-level heading, and
// the folder that relative image paths are read from.
func parseContent(input []byte, filename string, opts options) ([]byte, error) {
	ctx := parser.NewContext()
	doc := md.Parser().Parse(text.NewReader(input), parser.WithContext(ctx))
	addHeadingIDs(doc, input)

	var output bytes.Buffer
	// TryGetItems returns a yaml.MapSlice so keys keep their order in the file
	switch items, err := meta.TryGetItems(ctx); {
	case err != nil:
		fmt.Fprintf(&output, "<p><strong>Error in front matter:</strong> %s</p>\n",
			template.HTMLEscapeString(err.Error()))
	default:
		output.WriteString(frontMatterTable(items))
	}
	if err := md.Renderer().Render(&output, input, doc); err != nil {
		return nil, err
	}
	body := policy.SanitizeBytes(filterTags(output.Bytes()))
	body = fixLinks(body, filepath.Dir(filename), opts.served)

	choices, selected := codeStyles(opts)
	css, err := stylesheet(choices)
	if err != nil {
		return nil, err
	}

	tocHTML, err := renderTOC(doc, input)
	if err != nil {
		return nil, err
	}

	var t *template.Template
	if opts.template != "" {
		t, err = template.ParseFiles(opts.template)
	} else {
		t, err = template.New("mdp").Parse(defaultTemplate)
	}
	if err != nil {
		return nil, err
	}

	c := content{
		Title:     title(doc, input, filename),
		Theme:     cmp.Or(opts.theme, auto),
		CSS:       css,
		Styles:    choices,
		CodeStyle: selected,
		TOC:       template.HTML(tocHTML),
		TOCOpen:   opts.toc && tocHTML != nil,
		Body:      template.HTML(body),
	}
	if math, mermaid := usesKinds(doc); math || mermaid {
		if math {
			c.KaTeXCSS, c.KaTeX = katexCSS(), katexJS()
		}
		if mermaid {
			c.Mermaid = mermaidJS()
		}
	}

	var buffer bytes.Buffer
	if err := t.Execute(&buffer, c); err != nil {
		return nil, err
	}

	return buffer.Bytes(), nil
}

// title returns the text of the first top-level (#) heading, or the base of
// filename if there isn't one
func title(doc ast.Node, source []byte, filename string) string {
	var heading ast.Node
	ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if h, ok := n.(*ast.Heading); ok && entering && h.Level == 1 {
			heading = h
			return ast.WalkStop, nil
		}
		return ast.WalkContinue, nil
	})
	if heading == nil {
		return filepath.Base(filename)
	}

	if t := headingText(heading, source); t != "" {
		return t
	}
	return filepath.Base(filename)
}

// headingText returns a heading's plain text, without markup like emphasis
// or link targets
func headingText(heading ast.Node, source []byte) string {
	var b strings.Builder
	ast.Walk(heading, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch n := n.(type) {
		case *ast.Text:
			b.Write(n.Value(source))
		case *ast.String:
			b.Write(n.Value)
		case *mathNode:
			b.Write(n.tex)
		}
		return ast.WalkContinue, nil
	})
	return strings.TrimSpace(b.String())
}

// addHeadingIDs gives each heading an id, so the table of contents, or a URL
// ending in #heading-id, can link to it. The ids are made the way GitHub
// makes them, so links written for GitHub work here too.
func addHeadingIDs(doc ast.Node, source []byte) {
	seen := map[string]int{}
	ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if h, ok := n.(*ast.Heading); ok && entering {
			// repeats get -1, -2, ..., skipping any a heading already has
			base := slug(headingText(h, source))
			id := base
			for seen[id] > 0 {
				id = base + "-" + strconv.Itoa(seen[base])
				seen[base]++
			}
			seen[id]++
			h.SetAttributeString("id", []byte(id))
		}
		return ast.WalkContinue, nil
	})
}

// slug turns heading text into an id like GitHub's: lowercase, spaces
// become hyphens, and punctuation and symbols (emoji included) are dropped,
// but letters in any language are kept
func slug(text string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(text) {
		switch {
		case r == ' ':
			b.WriteRune('-')
		case r == '-' || r == '_' || unicode.IsLetter(r) || unicode.IsNumber(r) || unicode.IsMark(r):
			b.WriteRune(r)
		}
	}
	return b.String()
}

// renderTOC returns a nested list of links to the document's headings, or
// nil if there are none. A lone # heading is the page title, so then the
// list starts at ##.
func renderTOC(doc ast.Node, source []byte) ([]byte, error) {
	opts := []toc.InspectOption{toc.Compact(true)}
	if countH1(doc) == 1 {
		opts = append(opts, toc.MinDepth(2))
	}

	tree, err := toc.Inspect(doc, source, opts...)
	if err != nil {
		return nil, err
	}
	list := toc.RenderList(tree)
	if list == nil {
		return nil, nil
	}

	var buf bytes.Buffer
	if err := md.Renderer().Render(&buf, source, list); err != nil {
		return nil, err
	}
	return policy.SanitizeBytes(buf.Bytes()), nil
}

// countH1 walks the AST and returns the number of top-level (#) headings in the document.
// ast.Walk calls the callback on both entering and leaving each node; checking entering
// ensures each heading is counted only once.
func countH1(doc ast.Node) int {
	n := 0
	ast.Walk(doc, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if h, ok := node.(*ast.Heading); ok && entering && h.Level == 1 {
			n++
		}
		return ast.WalkContinue, nil
	})
	return n
}
