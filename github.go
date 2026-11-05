// File responsibilities (github.go):
//   - Custom Goldmark extensions for GitHub-specific Markdown features that
//     Goldmark doesn't provide out of the box: inline/display LaTeX math
//     ($...$, $$...$$, $`...`$), fenced ```math and ```mermaid blocks,
//     blockquote alerts (> [!NOTE]), raw HTML tag filtering (<script>, etc.),
//     and rendering YAML front matter as a table.
package main

import (
	"bytes"
	"fmt"
	"html"
	"regexp"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
	"gopkg.in/yaml.v2"
)

// githubExtras adds math and the blocks for math and Mermaid diagrams.
//
// `struct{}` is a zero-size type (0 bytes in memory). Attaching methods to it
// lets `githubExtras` implicitly satisfy `goldmark.Extender` with zero allocation.
type githubExtras struct{}

// Extend implements goldmark.Extender to register math parsers, block transformers, and renderers.
//
// Because `githubExtras` has no fields and `Extend` doesn't reference the
// receiver, Go lets you omit the receiver variable name and write `(githubExtras)`.
func (githubExtras) Extend(m goldmark.Markdown) {
	m.Parser().AddOptions(
		parser.WithInlineParsers(util.Prioritized(mathParser{}, 150)),
		parser.WithASTTransformers(
			util.Prioritized(specialBlocks{}, 100),
			util.Prioritized(alerts{}, 100),
		),
	)
	m.Renderer().AddOptions(renderer.WithNodeRenderers(util.Prioritized(extrasRenderer{}, 100)))
}

// Node kinds for custom AST elements representing math and diagrams
var (
	kindMath      = ast.NewNodeKind("Math")
	kindMathBlock = ast.NewNodeKind("MathBlock")
	kindMermaid   = ast.NewNodeKind("Mermaid")
)

// mathNode is TeX inside a paragraph: $inline$, $`inline`$, or $$display$$.
//
// Go struct embedding (`ast.BaseInline`):
//
//	Listing `ast.BaseInline` by type name alone (no field name) embeds it into
//	`mathNode`. All of `BaseInline`'s fields and ~25 `ast.Node` tree-navigation
//	methods (`Parent`, `NextSibling`, `FirstChild`, etc.) are automatically
//	promoted to `mathNode`, so `*mathNode` satisfies `ast.Node` while only
//	needing to define `Kind()` and `Dump()`.
//	Unlike Java `extends`, embedding is composition rather than subtyping:
//	`*mathNode` is not a subtype of `*ast.BaseInline`, and polymorphism happens
//	through the `ast.Node` interface.
type mathNode struct {
	ast.BaseInline
	tex     []byte
	display bool
}

// Kind implements ast.Node.
// Pointer receiver `(n *mathNode)` is used because the promoted tree-mutation
// methods on `ast.BaseInline` have pointer receivers, so `*mathNode` (not value
// `mathNode`) is the type that satisfies `ast.Node`.
func (n *mathNode) Kind() ast.NodeKind { return kindMath }

// Dump implements ast.Node for debugging the syntax tree.
func (n *mathNode) Dump(source []byte, level int) {
	ast.DumpHelper(n, source, level, map[string]string{"TeX": string(n.tex)}, nil)
}

// rawBlock is a fenced code block that's rendered, not shown: ```math or
// ```mermaid.
//
// Like `mathNode` above, `rawBlock` embeds `ast.BaseBlock` (Goldmark's base
// struct for block-level AST nodes) so all block-level `ast.Node` methods are
// promoted onto `*rawBlock`.
type rawBlock struct {
	ast.BaseBlock
	kind ast.NodeKind
	text []byte
}

// Kind implements ast.Node.
func (n *rawBlock) Kind() ast.NodeKind { return n.kind }

// Dump implements ast.Node for debugging the syntax tree.
func (n *rawBlock) Dump(source []byte, level int) {
	ast.DumpHelper(n, source, level, map[string]string{"Text": string(n.text)}, nil)
}

// mathParser finds math the way GitHub does. $inline$ math stays on one line,
// its opening $ must have a non-space after it, and its closing $ a
// non-space before it and no digit after it, so "$5 and $10" stays text.
// $$display$$ and $`inline`$ have no such rules, and display math can run
// over lines in a paragraph.
//
// Implicitly satisfies Goldmark's `parser.InlineParser` interface (`Trigger` + `Parse`).
type mathParser struct{}

// Trigger returns the bytes that start a potential math span.
func (mathParser) Trigger() []byte { return []byte{'$'} }

// Parse scans for a closing math delimiter and returns a mathNode, or nil if
// the $ is ordinary text.
func (mathParser) Parse(parent ast.Node, block text.Reader, pc parser.Context) ast.Node {
	line, _ := block.PeekLine()
	open, close, display := "$", "$", false
	switch {
	case bytes.HasPrefix(line, []byte("$$")):
		open, close, display = "$$", "$$", true
	case bytes.HasPrefix(line, []byte("$`")):
		open, close = "$`", "`$"
	case len(line) < 2 || isSpace(line[1]):
		return nil
	}

	// Save the reader's current position so we can backtrack if this turns out
	// not to be valid math syntax (e.g. currency like "$5 and $10").
	startLine, startPos := block.Position()
	block.Advance(len(open))
	var tex []byte
	for {
		line, _ := block.PeekLine()
		if line == nil {
			block.SetPosition(startLine, startPos) // backtrack to let other parsers handle the text
			return nil
		}
		if end := closingDelim(line, close, open == "$"); end >= 0 {
			// `line[:end]...` unpacks the slice as variadic byte arguments to `append`.
			tex = append(tex, line[:end]...)
			block.Advance(end + len(close))
			break
		}
		if open == "$" {
			block.SetPosition(startLine, startPos)
			return nil
		}
		tex = append(tex, line...)
		block.AdvanceLine()
	}

	tex = bytes.TrimSpace(tex)
	if len(tex) == 0 {
		block.SetPosition(startLine, startPos)
		return nil
	}
	return &mathNode{tex: tex, display: display}
}

// closingDelim returns where the closing delimiter is in line, or -1. strict
// applies the rules for a single $: a non-space just before it (so not at
// the start of a line), and no digit just after.
func closingDelim(line []byte, close string, strict bool) int {
	for i := 0; i+len(close) <= len(line); i++ {
		if line[i] == '\\' {
			i++ // skip escaped characters, like \$
			continue
		}
		if !bytes.HasPrefix(line[i:], []byte(close)) {
			continue
		}
		if !strict {
			return i
		}
		spaceBefore := i == 0 || isSpace(line[i-1])
		digitAfter := i+1 < len(line) && line[i+1] >= '0' && line[i+1] <= '9'
		if !spaceBefore && !digitAfter {
			return i
		}
	}
	return -1
}

// isSpace reports whether byte c is an ASCII whitespace character
func isSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' }

// specialBlocks turns ```math and ```mermaid code blocks into blocks that are
// rendered in the page, as GitHub does.
//
// Implicitly satisfies Goldmark's `parser.ASTTransformer` interface.
type specialBlocks struct{}

// Transform implements parser.ASTTransformer, collecting math and mermaid code
// blocks during traversal and replacing them afterward so the walk isn't
// disrupted.
func (specialBlocks) Transform(doc *ast.Document, reader text.Reader, pc parser.Context) {
	source := reader.Source()
	var found []*ast.FencedCodeBlock
	ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if code, ok := n.(*ast.FencedCodeBlock); ok && entering {
			switch string(code.Language(source)) {
			case "math", "mermaid":
				found = append(found, code)
			}
		}
		return ast.WalkContinue, nil
	})

	for _, code := range found {
		block := &rawBlock{kind: kindMathBlock}
		if string(code.Language(source)) == "mermaid" {
			block.kind = kindMermaid
		}
		// Go 1.22+ supports ranging directly over an integer:
		// `for i := range N` iterates `i` from 0 to N-1.
		for i := range code.Lines().Len() {
			line := code.Lines().At(i)
			block.text = append(block.text, line.Value(source)...)
		}
		code.Parent().ReplaceChild(code.Parent(), code, block)
	}
}

// alertTypes are GitHub's alerts, from a first line like "> [!NOTE]"
var alertTypes = map[string]string{
	"NOTE":      "Note",
	"TIP":       "Tip",
	"IMPORTANT": "Important",
	"WARNING":   "Warning",
	"CAUTION":   "Caution",
}

// alertMarker matches an alert type indicator on the first line of a blockquote (e.g. "[!NOTE]")
var alertMarker = regexp.MustCompile(`^\[!([A-Za-z]+)\]$`)

// alerts turns blockquotes that start with an alert marker into alerts: the
// marker becomes a title, and a class gives the alert its color and icon.
//
// Implicitly satisfies Goldmark's `parser.ASTTransformer` interface.
type alerts struct{}

// Transform implements parser.ASTTransformer, rewriting blockquotes that start
// with a recognized [!TYPE] marker into styled alert blocks.
func (alerts) Transform(doc *ast.Document, reader text.Reader, pc parser.Context) {
	source := reader.Source()
	ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		quote, ok := n.(*ast.Blockquote)
		if !ok || !entering {
			return ast.WalkContinue, nil
		}
		para, ok := quote.FirstChild().(*ast.Paragraph)
		if !ok || para.Lines().Len() == 0 {
			return ast.WalkContinue, nil
		}
		first := para.Lines().At(0)
		m := alertMarker.FindSubmatch(bytes.TrimSpace(first.Value(source)))
		if m == nil {
			return ast.WalkContinue, nil
		}
		kind := strings.ToUpper(string(m[1]))
		// "Comma-ok" map lookup: `ok` is false if `kind` is not in the map
		// (as opposed to a single-value lookup, which returns `""` either way).
		title, ok := alertTypes[kind]
		if !ok {
			return ast.WalkContinue, nil
		}

		// drop the marker line, and the paragraph if that was all of it
		for c := para.FirstChild(); c != nil; {
			next := c.NextSibling()
			t, ok := c.(*ast.Text)
			if !ok || t.Segment.Start >= first.Stop {
				break
			}
			para.RemoveChild(para, c)
			c = next
		}
		if para.ChildCount() == 0 {
			quote.RemoveChild(quote, para)
		}

		quote.SetAttributeString("class", []byte("markdown-alert markdown-alert-"+strings.ToLower(kind)))
		heading := ast.NewParagraph()
		heading.SetAttributeString("class", []byte("markdown-alert-title"))
		heading.AppendChild(heading, ast.NewString([]byte(title)))
		quote.InsertBefore(quote, quote.FirstChild(), heading)
		return ast.WalkContinue, nil
	})
}

// extrasRenderer writes math and Mermaid as escaped text in elements the
// page's scripts find and render.
//
// Implicitly satisfies Goldmark's `renderer.NodeRenderer` interface.
type extrasRenderer struct{}

// RegisterFuncs implements renderer.NodeRenderer, registering HTML writers for
// math and Mermaid AST nodes.
func (extrasRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	// Notice `_ []byte` in the callback signature: the blank identifier `_`
	// discards an interface-required parameter (`source []byte`) that this
	// function doesn't use, keeping the compiler happy.
	reg.Register(kindMath, func(w util.BufWriter, _ []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
		if entering {
			// Unchecked type assertion `n.(*mathNode)`: safe here because this
			// callback is registered exclusively for `kindMath` nodes.
			m := n.(*mathNode)
			class := "math math-inline"
			if m.display {
				class = "math math-display"
			}
			fmt.Fprintf(w, `<span class="%s">%s</span>`, class, html.EscapeString(string(m.tex)))
		}
		return ast.WalkSkipChildren, nil
	})
	reg.Register(kindMathBlock, func(w util.BufWriter, _ []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
		if entering {
			fmt.Fprintf(w, "<div class=\"math math-display\">%s</div>\n", html.EscapeString(string(n.(*rawBlock).text)))
		}
		return ast.WalkSkipChildren, nil
	})
	reg.Register(kindMermaid, func(w util.BufWriter, _ []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
		if entering {
			fmt.Fprintf(w, "<pre class=\"mermaid\">%s</pre>\n", html.EscapeString(string(n.(*rawBlock).text)))
		}
		return ast.WalkSkipChildren, nil
	})
}

// usesKinds reports whether doc has math, and whether it has Mermaid
// diagrams, so the page only carries the scripts it needs.
//
// Go note on named return parameters `(math, mermaid bool)`:
//
//	Declaring names on the return types initializes local variables `math` and
//	`mermaid` to their zero value (`false`). The closure inside `ast.Walk`
//	mutates them directly, and `return math, mermaid` returns the final flags.
func usesKinds(doc ast.Node) (math, mermaid bool) {
	ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		switch n.Kind() {
		case kindMath, kindMathBlock:
			math = true
		case kindMermaid:
			mermaid = true
		}
		return ast.WalkContinue, nil
	})
	return math, mermaid
}

// tagFilter escapes the raw HTML tags GitHub doesn't allow, so they show as
// text, as the GFM spec's tagfilter extension says. Tags in code are already
// escaped by then, so only raw HTML is affected.
var tagFilter = regexp.MustCompile(`(?i)<(/?(?:title|textarea|style|xmp|iframe|noembed|noframes|script|plaintext))\b`)

// filterTags escapes the raw HTML tags disallowed by GitHub (such as <script> and <style>) so they display as text
func filterTags(page []byte) []byte {
	return tagFilter.ReplaceAll(page, []byte("&lt;$1"))
}

// frontMatterTable renders YAML front matter as GitHub does: a table with a
// column per key, and lists and maps as tables within it
func frontMatterTable(items yaml.MapSlice) string {
	if len(items) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(`<table class="front-matter"><thead><tr>`)
	for _, item := range items {
		fmt.Fprintf(&b, "<th>%s</th>", html.EscapeString(fmt.Sprint(item.Key)))
	}
	b.WriteString("</tr></thead><tbody><tr>")
	for _, item := range items {
		fmt.Fprintf(&b, "<td>%s</td>", frontMatterValue(item.Value))
	}
	b.WriteString("</tr></tbody></table>\n")
	return b.String()
}

// frontMatterValue renders a front matter YAML value to an HTML string,
// recursively formatting nested maps and lists as tables.
//
// `any` is a built-in alias for `interface{}` (the empty interface satisfied by
// every Go type, including primitives).
func frontMatterValue(v any) string {
	switch v := v.(type) {
	case yaml.MapSlice:
		return strings.TrimSpace(frontMatterTable(v))
	case []any:
		var b strings.Builder
		b.WriteString("<table><tbody><tr>")
		for _, item := range v {
			fmt.Fprintf(&b, "<td>%s</td>", frontMatterValue(item))
		}
		b.WriteString("</tr></tbody></table>")
		return b.String()
	case nil:
		return ""
	default:
		return html.EscapeString(fmt.Sprint(v))
	}
}
