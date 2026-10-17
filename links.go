package main

import (
	"bytes"
	"encoding/base64"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/net/html"
)

// fixLinks rewrites the relative paths in sanitized page HTML, which are
// relative to dir, the Markdown file's folder:
//
//   - local images, from Markdown or raw HTML, are embedded as data URIs,
//     since the page itself is written to a temp folder or served
//   - images ending in #gh-dark-mode-only or #gh-light-mode-only get a class
//     so they show only in that mode, as on GitHub
//   - other relative links become absolute file:// URLs, unless the page is
//     served, where they're left for the server to answer
func fixLinks(page []byte, dir string, served bool) []byte {
	var out bytes.Buffer
	z := html.NewTokenizer(bytes.NewReader(page))
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			return out.Bytes()
		}
		if tt != html.StartTagToken && tt != html.SelfClosingTagToken {
			out.Write(z.Raw())
			continue
		}

		tok := z.Token()
		changed := false
		for i, attr := range tok.Attr {
			var value string
			switch {
			case tok.Data == "img" && attr.Key == "src":
				if mode := modeOnly(attr.Val); mode != "" {
					tok.Attr = append(tok.Attr, html.Attribute{Key: "class", Val: mode})
					changed = true
				}
				value = embedImage(attr.Val, dir)
			case tok.Data == "source" && attr.Key == "srcset":
				value = embedSrcset(attr.Val, dir)
			case tok.Data == "a" && attr.Key == "href" && !served:
				value = absoluteLink(attr.Val, dir)
			default:
				continue
			}
			if value != attr.Val {
				tok.Attr[i].Val = value
				changed = true
			}
		}
		if changed {
			out.WriteString(tok.String())
		} else {
			out.Write(z.Raw())
		}
	}
}

// modeOnly returns the class for an image GitHub shows in only one mode
func modeOnly(src string) string {
	for _, mode := range []string{"gh-dark-mode-only", "gh-light-mode-only"} {
		if strings.HasSuffix(src, "#"+mode) {
			return mode
		}
	}
	return ""
}

// localPath returns the file a relative or absolute path URL points to, and
// whether it is one, rather than a URL with a scheme or host, or a #fragment
func localPath(ref, dir string) (path string, u *url.URL, ok bool) {
	u, err := url.Parse(ref)
	if err != nil || u.Scheme != "" || u.Host != "" || u.Path == "" {
		return "", nil, false
	}
	path = filepath.FromSlash(u.Path)
	if !filepath.IsAbs(path) {
		path = filepath.Join(dir, path)
	}
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	return path, u, true
}

// embedImage returns a local image as a data URI, or src unchanged if it's
// remote or can't be read
func embedImage(src, dir string) string {
	path, _, ok := localPath(src, dir)
	if !ok {
		return src
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return src
	}

	mimeType := mime.TypeByExtension(filepath.Ext(path))
	if mimeType == "" {
		mimeType = http.DetectContentType(data)
	}
	mimeType, _, _ = strings.Cut(mimeType, ";")

	return "data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString(data)
}

// embedSrcset embeds each local image in a srcset list ("a.png 1x, b.png
// 2x"); data URIs have no spaces, so the list stays parseable
func embedSrcset(srcset, dir string) string {
	if strings.Contains(srcset, "data:") {
		return srcset // already embedded, and its commas aren't separators
	}
	candidates := strings.Split(srcset, ",")
	for i, c := range candidates {
		fields := strings.Fields(c)
		if len(fields) == 0 {
			continue
		}
		fields[0] = embedImage(fields[0], dir)
		candidates[i] = strings.Join(fields, " ")
	}
	return strings.Join(candidates, ", ")
}

// absoluteLink turns a link to a local file into a file:// URL, keeping
// any #fragment
func absoluteLink(href, dir string) string {
	path, u, ok := localPath(href, dir)
	if !ok {
		return href
	}
	slashed := filepath.ToSlash(path)
	if strings.HasSuffix(u.Path, "/") {
		slashed += "/" // a folder, which Join's cleaning drops the slash of
	}
	abs := url.URL{Scheme: "file", Path: slashed, RawQuery: u.RawQuery, Fragment: u.Fragment}
	return abs.String()
}
