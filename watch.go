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

	// Launch preview in a separate goroutine so it opens while http.Serve blocks below
	if !skipPreview {
		go func() {
			if err := preview(url); err != nil {
				fmt.Fprintln(os.Stderr, err)
			}
		}()
	}

	return http.Serve(ln, newWatcher(filename, opts).handler())
}

// handler returns an http.Handler that routes /events for SSE and / for page serving
func (w *watcher) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /events", w.serveEvents)
	mux.HandleFunc("GET /", w.serveFile)
	return mux
}

// resolve maps a URL path to a file: / is the previewed file, and anything
// else is in its folder; cleaning the path keeps it there
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
