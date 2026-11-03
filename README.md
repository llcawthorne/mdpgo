# mdp

A Markdown preview tool: renders a Markdown file as a GitHub-styled HTML page
and opens it in your browser.

## Features

* GitHub Flavored Markdown: tables, task lists, strikethrough, autolinks
* GitHub's additions: footnotes, `> [!NOTE]` alerts, `:emoji:` shortcodes,
  `$math$` and `$$math$$` (with KaTeX), Mermaid diagrams, and front matter
  shown as a table
* Raw HTML like `<kbd>`, `<details>`, `<picture>`, and `align="center"`, with
  anything unsafe removed, and disallowed tags like `<script>` shown as text
* Images ending in `#gh-dark-mode-only` or `#gh-light-mode-only` show in only
  that mode
* Heading ids made the way GitHub makes them, so `#links` carry over
* Syntax highlighting for fenced code blocks, in any Chroma style
* GitHub-like styling, light or dark to match your OS setting, or forced
* A top bar with the title, a table of contents toggle, search, a code style
  menu, a light/dark switch, and a print button
* Table of contents in a sidebar that slides in and out
* PDF output, using Chrome
* Local images embedded in the page, so relative paths work
* Watch mode: the preview reloads whenever you save the file

## How to install

```sh
go install github.com/llcawthorne/mdp@latest
```

## Usage

```sh
mdp [flags] FILE
```

| Flag                | Meaning                                       |
|---------------------|-----------------------------------------------|
| `-s`, `--skip`      | Don't open a preview; just print its path     |
| `-t`, `--toc`       | Open the table of contents                    |
| `-w`, `--watch`     | Serve a preview that reloads on file changes  |
| `-o`, `--output F`  | Write to `F` instead of a temp file           |
| `-p`, `--pdf`       | Make a PDF instead of a web page              |
| `-d`, `--dark`      | Start dark instead of following the OS        |
| `-l`, `--light`     | Start light instead of following the OS       |
| `--style NAME`      | Code colors (default `github`)                |
| `--dark-style NAME` | Code colors for dark mode only                |
| `--template FILE`   | Use an alternate template                     |

Flags can go before or after the file name, and short ones can be combined,
as in `mdp -wd notes.md`.

Without `-w`, mdp writes the page to a temp file, opens it, and deletes it.
With `-o`, the page goes where you say and is kept. With `-w`, it serves the
page on a local URL until you press Ctrl-C, along with the rest of its folder:
links to other Markdown files open them rendered, like browsing a repository
on GitHub, and each page reloads when its own file changes. Without `-w`,
links to local files point at the files themselves.

Math and Mermaid diagrams are drawn by KaTeX and Mermaid, which are built
into mdp (see `assets/`) and only added to pages that use them, so pages and
PDFs work offline. Diagrams take the page's light or dark colors as it
loads.

### PDFs

`-p` prints the page to a PDF with headless Chrome (or Chromium, Edge, or
Brave; set `MDP_CHROME` to the browser's path if mdp can't find it). The PDF
goes beside the Markdown file, `notes.md` becoming `notes.pdf`, unless `-o`
names another file; an `-o` name ending in `.pdf` implies `-p`. PDFs follow
the OS's light or dark setting unless you pass `-l` or `-d`. Instead of a
contents page, PDFs have an outline of their headings, which PDF viewers show
in their sidebar.

The page title is the file's first `#` heading, or the file name if it has
none.

`mdp --help` lists the code styles. Styles that come in light and dark
versions, like `solarized-light` and `solarized-dark`, switch with the page;
name either one. Other styles are used in both modes. `--style friendly`
matches pandoc's default colors.

The top bar's menu switches the code style between GitHub, Darcula,
Solarized, and Xcode, plus your `--style` if it's something else, and its
sun/moon button switches between light and dark. The search button, or `/` or
`s`, searches the page: matches are listed under their headings and
highlighted in the page; arrow keys and Enter pick one, and Esc closes it.
These settings, and the table of contents button's, last while the tab is
open, so watch mode's reloads keep them.

### Math

`$…$` is inline math and `$$…$$` (or a `math` code block) is display math,
in LaTeX, drawn by KaTeX: `$\textbf{test}$`, `$e^{i\pi} + 1 = 0$`. Inline
math stays on one line, with no space just inside its dollar signs, so prices
like $5 and $10 stay text; `` $`…`$ `` is inline math that can hold a `$`.

The table of contents links to every heading. If there's only one `#` heading,
it's treated as the page title and left out. On wide windows the page moves
over to make room for it; on narrow ones it covers the page.

### Templates

A custom template receives the following fields:
* `{{ .Title }}`: Page title (first `#` heading or file name)
* `{{ .CSS }}`: Combined GitHub styles and Chroma syntax highlighting rules
* `{{ .TOC }}`: Rendered HTML for the table of contents
* `{{ .TOCOpen }}`: Whether `--toc` asked to start with the table of contents open
* `{{ .Body }}`: The sanitized HTML article
* `{{ .KaTeXCSS }}`, `{{ .KaTeX }}`: Styles and script for math rendering (when math is present)
* `{{ .Mermaid }}`: Script for rendering Mermaid diagrams (when diagrams are present)

Wrap the body in `<article class="markdown-body">` for the styles to apply.
See `template-fmt.html.tmpl` for a minimal example.

> [!NOTE]
> Custom templates replace the default page entirely. Bare-bones templates that
> omit `{{ .KaTeX }}` or `{{ .Mermaid }}` will not render math formulas or diagrams,
> and won't include the top bar, search, or theme switcher unless implemented
> in the template. In watch mode (`-w`), the live-reload script is automatically
> appended before `</body>` (or at the end of the file if omitted).

## Development

```sh
go test ./...
```

The golden tests render each `testdata/golden/*.md` and compare the result
with the `.html` beside it. `TestRendersInChrome` loads some of them in
headless Chrome to check that the math and diagrams really render; it's
skipped with `-short` or without Chrome. After a deliberate change to the output,
regenerate them and read the diff:

```sh
go test -run TestGolden -update
```

### File Responsibilities

The codebase uses a flat `package main` structure, where each file covers a specific domain:

* **`main.go`**: Command-line flag parsing with `pflag`, validation, execution routing (`run` vs `serve`), and OS browser preview launcher.
* **`render.go`**: Core Markdown-to-HTML rendering pipeline via Goldmark, HTML sanitization via Bluemonday UGC policy, heading ID slug generation, and TOC tree building.
* **`github.go`**: Goldmark AST extensions for GitHub additions: math syntax (`$…$`, `$$…$$`), `math` and `mermaid` fenced block transformers, alert callouts (`> [!NOTE]`), tag filtering, and YAML front matter tables.
* **`links.go`**: Streaming HTML tokenizer that rewrites relative links and inlines local images as base64 data URIs so pages are portable and self-contained.
* **`style.go`**: Chroma syntax highlighting, light and dark theme pairings, and dynamic CSS selector scoping (`scopeCSS`) to prevent style collisions.
* **`watch.go`**: Live-reload server using Server-Sent Events (SSE) and file stat polling, serving linked files across the source directory.
* **`pdf.go`**: Headless Chrome driver for printing PDFs with heading outlines and platform dark mode detection.
* **`assets.go`**: Embedded vendor libraries (KaTeX and Mermaid) via `embed.FS`, with base64-inlined fonts for offline rendering.
* **`page.html.tmpl`**: Default HTML template containing the top bar, slide-out table of contents, client-side search, and theme switching scripts.
* **`github.css` / `github-dark.css`**: GitHub-like stylesheets embedded into the page for light and dark modes.
* **`golden_test.go`**: Golden test runner that extracts `<title>`, `<nav>`, and `<article>` to verify output against `testdata/golden/` without breaking on CSS changes.
* **`chrome_test.go`**: Integration tests verifying that KaTeX math and Mermaid diagrams render cleanly in headless Chrome.
* **`watch_test.go`**: Tests for HTTP live-reload event streaming and directory serving.
* **`main_test.go`**: CLI flag parsing tests, end-to-end execution tests, and legacy book regression fixtures.
