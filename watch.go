// File responsibilities (watch.go):
//   - Serves a live-reloading local HTTP preview of a Markdown file and its
//     directory, streaming Server-Sent Events (SSE) on `/events` whenever a
//     watched file's modification time or size changes.
package main

import (
	"bytes"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// pollInterval is how often watch mode checks a file for changes. Polling
// rather than filesystem events copes with editors like Neovim that save by
// writing a new file and renaming it over the old one.
//
// `time.Duration` is a defined type (`type Duration int64`) representing
// nanoseconds; multiplying an untyped integer constant like `250` by
// `time.Millisecond` produces a typed `time.Duration` at compile time.
const pollInterval = 250 * time.Millisecond

// reloadScript makes a page reload when the server reports its file changed.
// The browser's EventSource reconnects by itself if the connection drops.
const reloadScript = `<script>new EventSource("/events?path=" + ` +
	`encodeURIComponent(decodeURIComponent(location.pathname))).onmessage = () => location.reload();</script>`

// watcher serves a live preview of a Markdown file at /, and the rest of its
// folder beside it, like a repository on GitHub: other Markdown files
// rendered, so links between them work, and anything else as it is. Each
// page reloads when its own file changes.
type watcher struct {
	filename string
	root     string
	opts     options
	interval time.Duration
}

// newWatcher creates a watcher rooted in the directory of filename.
// opts.served is enabled so relative links between markdown files are left
// for the server to resolve rather than converted to absolute file:// URLs.
//
// Go constructor convention (`newThing`):
//
//	Go has no special constructor syntax; constructors are ordinary functions
//	named `newX` (or `NewX` if exported). Note how `opts` is passed by value,
//	so `opts.served = true` mutates the local copy without affecting the
//	caller's `options` struct.
func newWatcher(filename string, opts options) *watcher {
	opts.served = true
	return &watcher{
		filename: filename,
		root:     filepath.Dir(filename),
		opts:     opts,
		interval: pollInterval,
	}
}

// serve previews filename at a local URL, re-rendering it on every request,
// until the program is interrupted
func serve(filename string, opts options, out io.Writer, skipPreview bool) error {
	if _, err := os.Stat(filename); err != nil {
		return err
	}

	// Port :0 tells the OS kernel to allocate an available ephemeral port
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}

	url := "http://" + ln.Addr().String() + "/"
	fmt.Fprintln(out, url)

	// Go vs. Java note on Goroutines (`go func() { ... }()`):
	//   Prefixing a function call with `go` spawns a goroutine—a lightweight
	//   green thread managed by the Go runtime (starting with only a ~2KB
	//   growable stack, similar to Java 21 virtual threads). Because
	//   `http.Serve` below blocks forever serving HTTP requests, launching
	//   `preview(url)` in a goroutine lets the browser open concurrently
	//   while the server starts accepting connections.
	if !skipPreview {
		go func() {
			if err := preview(url); err != nil {
				fmt.Fprintln(os.Stderr, err)
			}
		}()
	}

	// Note that `net/http` automatically handles each incoming HTTP request
	// in its own goroutine, so a long-lived `/events` SSE request never blocks
	// `/` page or asset requests.
	return http.Serve(ln, newWatcher(filename, opts).handler())
}

// handler returns an http.Handler that routes /events for SSE and / for page serving.
//
// Go note on `http.Handler`, method values (`w.serveEvents`), and ServeMux:
//   - `http.Handler` is Go's core HTTP interface (`ServeHTTP(ResponseWriter, *Request)`),
//     analogous to a Java Servlet.
//   - `w.serveEvents` is a **method value**: binding method `serveEvents` to
//     receiver `w` yields a `func(http.ResponseWriter, *http.Request)` closure
//     (just like Java's `this::serveEvents` method reference).
//   - Go 1.22+ `http.ServeMux` supports method-prefixed patterns (`"GET /events"`).
//     `"GET /"` is a subtree pattern that matches `/` and any path not matched
//     by a more specific pattern.
func (w *watcher) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /events", w.serveEvents)
	mux.HandleFunc("GET /", w.serveFile)
	return mux
}

// resolve maps a URL path to a file: / is the previewed file, and anything
// else is in its folder; cleaning the path keeps it there.
//
// Security note: prepending `"/"` before `path.Clean("/"+urlPath)` ensures any
// `".."` segments cannot climb above `/` (e.g., `path.Clean("/../../etc/passwd")`
// collapses to `"/etc/passwd"`), so `filepath.Join(w.root, ...)` stays safely
// inside `w.root`.
func (w *watcher) resolve(urlPath string) string {
	if urlPath == "" || urlPath == "/" {
		return w.filename
	}
	return filepath.Join(w.root, filepath.FromSlash(path.Clean("/"+urlPath)))
}

// isMarkdown reports whether name has a recognized Markdown extension
func isMarkdown(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".md", ".markdown", ".mdown", ".mkd":
		return true
	}
	return false
}

// serveFile renders Markdown files with the reload script added, and serves
// other files as they are. Errors are shown as a page that still reloads,
// so fixing the problem refreshes it.
func (w *watcher) serveFile(rw http.ResponseWriter, r *http.Request) {
	name := w.resolve(r.URL.Path)
	if r.URL.Path != "/" && !isMarkdown(name) {
		http.ServeFile(rw, r, name)
		return
	}

	page, err := w.render(name)
	if err != nil {
		rw.WriteHeader(http.StatusInternalServerError)
		page = []byte("<pre>" + html.EscapeString(err.Error()) + "</pre>\n" + reloadScript)
	}
	rw.Header().Set("Content-Type", "text/html; charset=utf-8")
	rw.Write(page)
}

// render reads, parses, and injects the live-reload script into the Markdown file
func (w *watcher) render(name string) ([]byte, error) {
	input, err := os.ReadFile(name)
	if err != nil {
		return nil, err
	}
	page, err := parseContent(input, name, w.opts)
	if err != nil {
		return nil, err
	}
	return injectScript(page), nil
}

// injectScript adds reloadScript just before </body>, or at the end if a
// custom template has no </body>
func injectScript(page []byte) []byte {
	i := bytes.LastIndex(page, []byte("</body>"))
	if i < 0 {
		return append(page, reloadScript...)
	}
	return bytes.Join([][]byte{page[:i], []byte(reloadScript + "\n"), page[i:]}, nil)
}

// serveEvents streams a server-sent "reload" event each time the file at the
// ?path= URL path changes: its modification time or size.
// http.Flusher pushes chunks immediately down the open connection.
func (w *watcher) serveEvents(rw http.ResponseWriter, r *http.Request) {
	// Go idiom (optional capability interface via type assertion):
	//   `rw` has static interface type `http.ResponseWriter`, which only has
	//   `Header()`, `Write()`, and `WriteHeader()`. To keep `ResponseWriter`
	//   minimal, optional transport features like flushing buffered bytes for
	//   Server-Sent Events are defined on a separate 1-method interface:
	//       type Flusher interface { Flush() }
	//   `flusher, ok := rw.(http.Flusher)` checks at runtime whether the
	//   concrete `ResponseWriter` implementation also satisfies `http.Flusher`.
	flusher, ok := rw.(http.Flusher)
	if !ok {
		http.Error(rw, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	name := w.resolve(r.URL.Query().Get("path"))
	last, _ := os.Stat(name)

	rw.Header().Set("Content-Type", "text/event-stream")
	rw.Header().Set("Cache-Control", "no-cache")
	fmt.Fprint(rw, ": connected\n\n")
	flusher.Flush()

	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		// Go concurrency idiom (`select` over channels):
		//   `select` blocks until one of its channel cases is ready:
		//     - `<-r.Context().Done()`: the request context's `Done()` channel
		//       is closed by `net/http` as soon as the browser tab closes or
		//       reloads, cleanly exiting this infinite loop and running
		//       `defer ticker.Stop()`.
		//     - `<-ticker.C`: receives a timestamp every `w.interval` (250ms).
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
		}

		info, err := os.Stat(name)
		if err != nil {
			// may be mid-save; check again next tick
			continue
		}
		if last == nil || !info.ModTime().Equal(last.ModTime()) || info.Size() != last.Size() {
			last = info
			fmt.Fprint(rw, "data: reload\n\n")
			flusher.Flush()
		}
	}
}
