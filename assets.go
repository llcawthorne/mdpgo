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
//go:embed assets/katex/katex.min.js assets/katex/katex.min.css assets/katex/fonts/*.woff2
//go:embed assets/mermaid/mermaid.min.js
var assets embed.FS

// asset returns the string content of an embedded file under assets/.
// It panics if the asset cannot be read, which can only occur if an embed pattern is misconfigured.
func asset(name string) string {
	data, err := assets.ReadFile("assets/" + name)
	if err != nil {
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
