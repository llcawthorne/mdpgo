package main

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// newTestWatcher copies the test input to a temp file it can change
func newTestWatcher(t *testing.T) *watcher {
	t.Helper()
	input, err := os.ReadFile(inputFile)
	if err != nil {
		t.Fatal(err)
	}
	filename := filepath.Join(t.TempDir(), "test1.md")
	if err := os.WriteFile(filename, input, 0644); err != nil {
		t.Fatal(err)
	}
	w := newWatcher(filename, options{})
	w.interval = 10 * time.Millisecond
	return w
}

func TestWatchServesPageWithReloadScript(t *testing.T) {
	srv := httptest.NewServer(newTestWatcher(t).handler())
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	for _, want := range []string{"Test Markdown File", reloadScript + "\n</body>"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("page missing %q:\n%s", want, body)
		}
	}
}

func TestWatchShowsErrorPageThatStillReloads(t *testing.T) {
	w := newTestWatcher(t)
	os.Remove(w.filename)
	srv := httptest.NewServer(w.handler())
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", resp.StatusCode)
	}
	if !strings.Contains(string(body), reloadScript) {
		t.Errorf("error page missing reload script:\n%s", body)
	}
}

// eventLines streams the lines of the server's events for the file at
// urlPath until the test ends
func eventLines(t *testing.T, srvURL, urlPath string) <-chan string {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	req, err := http.NewRequestWithContext(ctx, "GET", srvURL+"/events?path="+url.QueryEscape(urlPath), nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}

	lines := make(chan string)
	go func() {
		defer resp.Body.Close()
		r := bufio.NewReader(resp.Body)
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				close(lines)
				return
			}
			lines <- strings.TrimSuffix(line, "\n")
		}
	}()
	return lines
}

// expectReload checks that changing file sends a reload event to lines, and
// that none comes before
func expectReload(t *testing.T, lines <-chan string, file string) {
	t.Helper()
	if line := <-lines; line != ": connected" {
		t.Fatalf("first line = %q, want the connected comment", line)
	}

	select {
	case line := <-lines:
		if line == "data: reload" {
			t.Fatal("reload sent before the file changed")
		}
	case <-time.After(60 * time.Millisecond):
	}

	if err := os.WriteFile(file, []byte("# Changed\n"), 0644); err != nil {
		t.Fatal(err)
	}
	timeout := time.After(5 * time.Second)
	for {
		select {
		case line, ok := <-lines:
			if !ok {
				t.Fatal("events ended without a reload")
			}
			if line == "data: reload" {
				return
			}
		case <-timeout:
			t.Fatal("no reload event")
		}
	}
}

func TestWatchSendsReloadWhenFileChanges(t *testing.T) {
	w := newTestWatcher(t)
	srv := httptest.NewServer(w.handler())
	t.Cleanup(srv.Close)

	expectReload(t, eventLines(t, srv.URL, "/"), w.filename)
}

func TestWatchServesLinkedFiles(t *testing.T) {
	w := newTestWatcher(t)
	other := filepath.Join(w.root, "other.md")
	if err := os.WriteFile(other, []byte("# Other Page\n\n[back](test1.md)\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(w.root, "notes.txt"), []byte("plain text"), 0644); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(w.handler())
	t.Cleanup(srv.Close)

	get := func(urlPath string) (int, string) {
		t.Helper()
		resp, err := http.Get(srv.URL + urlPath)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(body)
	}

	// Markdown is rendered, with links left relative for the server
	status, body := get("/other.md")
	for _, want := range []string{"<h1 id=\"other-page\">Other Page</h1>", `href="test1.md"`, reloadScript} {
		if status != http.StatusOK || !strings.Contains(body, want) {
			t.Errorf("/other.md: status %d, missing %q", status, want)
		}
	}

	// other files are served as they are
	if status, body := get("/notes.txt"); status != http.StatusOK || body != "plain text" {
		t.Errorf("/notes.txt: status %d, body %q", status, body)
	}

	// nothing outside the folder
	if status, _ := get("/../../etc/passwd"); status != http.StatusNotFound {
		t.Errorf("/../../etc/passwd: status %d, want 404", status)
	}

	// each page reloads for its own file
	expectReload(t, eventLines(t, srv.URL, "/other.md"), other)
}
