package main

import (
	"bytes"
	"cmp"
	_ "embed"
	"fmt"
	"html/template"
	"regexp"
	"slices"
	"strings"

	"github.com/alecthomas/chroma/v2"
	chromahtml "github.com/alecthomas/chroma/v2/formatters/html"
	"github.com/alecthomas/chroma/v2/styles"
)

// defaultStyle is the default Chroma syntax highlighting theme
const defaultStyle = "github"

// pageCSS styles the page like GitHub, in light colors; darkCSS overrides
// them with dark ones
var (
	//go:embed github.css
	pageCSS string
	//go:embed github-dark.css
	darkCSS string
)

// theme picks the page's starting colors; auto follows the OS setting. The
// top bar's button switches them.
type theme string

const (
	auto  theme = "auto"
	light theme = "light"
	dark  theme = "dark"
)

// codeStyle is a choice in the page's code style menu, naming the Chroma
// styles to use in light and dark mode
type codeStyle struct {
	ID, Label   string
	light, dark string
}

// menuStyles are always in the code style menu
var menuStyles = []codeStyle{
	{ID: "github", Label: "GitHub", light: "github", dark: "github-dark"},
	{ID: "darcula", Label: "Darcula", light: "darcula", dark: "darcula"},
	{ID: "solarized", Label: "Solarized", light: "solarized-light", dark: "solarized-dark"},
	{ID: "xcode", Label: "Xcode", light: "xcode", dark: "xcode-dark"},
}

// codeStyles returns the menu's choices and the ID of the one opts asks for,
// which is added to the front of the menu if it isn't one of menuStyles
func codeStyles(opts options) (choices []codeStyle, selected string) {
	defaultLight, defaultDark := stylesFor(defaultStyle)
	light := cmp.Or(opts.lightStyle, defaultLight)
	dark := cmp.Or(opts.darkStyle, defaultDark)

	for _, s := range menuStyles {
		if s.light == light && s.dark == dark {
			return menuStyles, s.ID
		}
	}

	label := light
	if dark != light {
		label = light + " / " + dark
	}
	custom := codeStyle{ID: "custom", Label: label, light: light, dark: dark}
	return append([]codeStyle{custom}, menuStyles...), custom.ID
}

// stylesheet returns the page CSS plus the Chroma CSS for each code style
// choice, in both light and dark colors, so the page can switch between
// them; data-theme on <html> picks which apply
func stylesheet(choices []codeStyle) (template.CSS, error) {
	var buf bytes.Buffer
	buf.WriteString(pageCSS)
	buf.WriteString(darkCSS)
	for _, c := range choices {
		for _, th := range []theme{light, dark} {
			name := c.light
			if th == dark {
				name = c.dark
			}
			css, err := highlightCSS(name)
			if err != nil {
				return "", err
			}
			// each highlight style only sets colors for some token types,
			// so light and dark are kept apart rather than layered
			buf.WriteString(scopeCSS(css, c.ID, th))
		}
	}
	return template.CSS(buf.String()), nil
}

// highlightCSS returns the CSS for the classes Chroma puts on code, in the
// named style
func highlightCSS(name string) (string, error) {
	var buf strings.Builder
	style := styles.Get(name)
	formatter := chromahtml.New(chromahtml.WithClasses(true))
	if err := formatter.WriteCSS(&buf, style); err != nil {
		return "", err
	}
	buf.WriteString(codeBlockCSS(style))
	return buf.String(), nil
}

// chromaSelector matches the start of each selector in Chroma's CSS
var chromaSelector = regexp.MustCompile(`(^|[\s,])\.(chroma|bg)\b`)

// scopeCSS limits Chroma CSS to pages in theme th whose code style menu is set to id.
// By prefixing selectors with root attribute selectors, multiple syntax themes can be
// embedded in one page without their class rules colliding.
func scopeCSS(css, id string, th theme) string {
	return chromaSelector.ReplaceAllString(css,
		fmt.Sprintf(`${1}:root[data-theme=%q][data-code-style=%q] .$2`, th, id))
}

// codeBlockCSS patches a style's code block colors so it reads well in
// either mode. The github styles get GitHub's own block background, since
// github-dark's matches the page. Styles that set a background but no text
// color get one to suit it, rather than the page's.
func codeBlockCSS(style *chroma.Style) string {
	bg := style.Get(chroma.Background)
	switch {
	case strings.HasPrefix(style.Name, "github"):
		return ".chroma { background-color: var(--pre-bg) }\n"
	case bg.Background.IsSet() && !bg.Colour.IsSet():
		if bg.Background.Brightness() < 0.5 {
			return ".chroma { color: #e6edf3 }\n"
		}
		return ".chroma { color: #1f2328 }\n"
	}
	return ""
}

// styleFamilies groups styles that are light and dark versions of one
// another: the light one first, then the dark one to pair with it
var styleFamilies = [][]string{
	{"github", "github-dark"},
	{"catppuccin-latte", "catppuccin-mocha", "catppuccin-frappe", "catppuccin-macchiato"},
	{"gruvbox-light", "gruvbox"},
	{"kanagawa-lotus", "kanagawa-wave", "kanagawa-dragon"},
	{"modus-operandi", "modus-vivendi"},
	{"monokailight", "monokai"},
	{"paraiso-light", "paraiso-dark"},
	{"rose-pine-dawn", "rose-pine", "rose-pine-moon"},
	{"solarized-light", "solarized-dark", "solarized-dark256"},
	{"tokyonight-day", "tokyonight-night", "tokyonight-moon", "tokyonight-storm"},
	{"xcode", "xcode-dark"},
}

// stylesFor returns the light and dark mode styles for a style name: the
// family's light and dark versions if it has them, otherwise that style for
// both
func stylesFor(name string) (light, dark string) {
	for _, family := range styleFamilies {
		switch i := slices.Index(family, name); {
		case i == 0:
			return name, family[1]
		case i > 0:
			return family[0], name
		}
	}
	return name, name
}

// knownStyle reports whether Chroma has a style by that name; styles.Get
// would quietly fall back to a default instead
func knownStyle(name string) bool {
	return slices.Contains(styles.Names(), name)
}
