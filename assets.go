// File responsibilities (assets.go):
//   - Embeds third-party client-side libraries (KaTeX and Mermaid) into the
//     compiled binary via `embed.FS`, and lazily inlines KaTeX's WOFF2 font
//     files as base64 data URIs so math works offline and in PDFs.
package main

import (
	"embed"
	"encoding/base64"
	"html/template"
	"regexp"
	"sync"
)

// assets holds KaTeX (renders math) and Mermaid (renders diagrams), from npm
// via jsDelivr: katex@0.16.11 and mermaid@11.4.1. Pages include them only
// when they have math or diagrams.
//
// Go vs. Java note on `embed.FS`:
//   While `//go:embed` on a `string` embeds a single file, applying one or
//   more `//go:embed` directives (including globs like `*.woff2`) to an
//   `embed.FS` variable embeds an entire read-only virtual file tree directly
//   into the binary (similar to bundling resources inside a Java fat JAR,
//   except verified at compile time and accessed via a standard `io/fs.FS`
//   filesystem API).
//
//go:embed assets/katex/katex.min.js assets/katex/katex.min.css assets/katex/fonts/*.woff2
//go:embed assets/mermaid/mermaid.min.js
var assets embed.FS

// asset returns the string content of an embedded file under assets/.
// It panics if the asset cannot be read, which can only occur if an embed pattern is misconfigured.
func asset(name string) string {
	// embed.FS paths always use forward slashes ('/'), regardless of whether
	// the binary runs on Windows or Unix.
	data, err := assets.ReadFile("assets/" + name)
	if err != nil {
		// Go vs. Java note on `panic`:
		//   Go uses `error` return values for recoverable runtime failures
		//   (missing user files, network errors) and reserves `panic` for
		//   unreachable programmer/build bugs (like Java's `AssertionError`
		//   or `IllegalStateException`).
		panic(err) // embedded, so only a build mistake gets here
	}
	return string(data)
}

// katexFont matches a font in KaTeX's CSS, which lists woff2, woff, and ttf
// versions; only woff2 is kept, embedded
var katexFont = regexp.MustCompile(`url\(fonts/(KaTeX_[\w-]+)\.woff2\) format\("woff2"\)[^;}]*`)

// katexCSS lazily prepares KaTeX's stylesheet with all WOFF2 fonts embedded as
// base64 data URIs. This ensures math renders in standalone HTML files and PDFs
// without CORS or file:// font loading restrictions.
//
// Go idiom (`sync.OnceValue`):
//   `sync.OnceValue` (added in Go 1.21 using generics: `func OnceValue[T any](f func() T) func() T`)
//   wraps an initialization closure and returns a thread-safe getter function
//   `func() template.CSS`. Unlike a package-level IIFE (`func() ... {}()`),
//   which runs eagerly at program startup even if the markdown file has no
//   math, `sync.OnceValue` defers base64-encoding the 20 font files until the
//   first page that actually uses math, then caches the result forever
//   (like Guava's `Suppliers.memoize` or Java's lazy holder idiom).
var katexCSS = sync.OnceValue(func() template.CSS {
	css := katexFont.ReplaceAllStringFunc(asset("katex/katex.min.css"), func(m string) string {
		name := katexFont.FindStringSubmatch(m)[1]
		font := base64.StdEncoding.EncodeToString([]byte(asset("katex/fonts/" + name + ".woff2")))
		return `url(data:font/woff2;base64,` + font + `) format("woff2")`
	})
	return template.CSS(css)
})

// katexJS and mermaidJS lazily read and cache the embedded JS bundles as safe template.JS values
var (
	katexJS   = sync.OnceValue(func() template.JS { return template.JS(asset("katex/katex.min.js")) })
	mermaidJS = sync.OnceValue(func() template.JS { return template.JS(asset("mermaid/mermaid.min.js")) })
)
