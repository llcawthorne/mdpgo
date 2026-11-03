// File responsibilities (pdf.go):
//   - Drives headless Chrome (or Chromium/Edge/Brave) to print rendered HTML
//     pages to PDF with document outlines, and queries OS-specific settings to
//     detect dark mode when `--theme=auto`.
package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// runPDF prints the rendered page to a PDF, then opens it
func runPDF(cfg config, page []byte, out io.Writer) error {
	pdfName := cmp.Or(cfg.output, pdfPath(cfg.filename))
	if err := printPDF(page, pdfName); err != nil {
		return err
	}

	fmt.Fprintln(out, pdfName)
	if cfg.skipPreview {
		return nil
	}
	return preview(pdfName)
}

// pdfPath is where a Markdown file's PDF goes by default: beside it, with a
// .pdf extension instead of its own
func pdfPath(filename string) string {
	return strings.TrimSuffix(filename, filepath.Ext(filename)) + ".pdf"
}

// printPDF has headless Chrome load the page and print it to pdfName
func printPDF(page []byte, pdfName string) error {
	chrome, err := findChrome()
	if err != nil {
		return err
	}

	dir, err := os.MkdirTemp("", "mdp")
	if err != nil {
		return err
	}
	// Idiomatic Go resource cleanup: register `defer os.RemoveAll(dir)` right
	// after creating the temp directory so every early return path below
	// automatically cleans it up.
	defer os.RemoveAll(dir)

	htmlName := filepath.Join(dir, "page.html")
	if err := os.WriteFile(htmlName, page, 0644); err != nil {
		return err
	}
	pageURL := url.URL{Scheme: "file", Path: filepath.ToSlash(htmlName)}
	tempPDF := filepath.Join(dir, "page.pdf")

	// Go idiom (`context.WithTimeout` + `defer cancel()`):
	//   `context.Context` is Go's standard mechanism for propagating timeouts
	//   and cancellation across goroutines, network calls, and child processes.
	//   `WithTimeout` returns a derived Context that automatically cancels
	//   after 1 minute, plus a `cancel` function. Always `defer cancel()` so
	//   the internal timer is released immediately when `printPDF` finishes in
	//   <1s rather than leaking until the 1-minute deadline expires.
	//   Passing `ctx` to `exec.CommandContext` kills the Chrome process if the
	//   timeout is reached.
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, chrome,
		"--headless",
		"--disable-gpu",
		"--no-first-run",
		"--no-default-browser-check",
		// a profile of its own, so it doesn't clash with a running Chrome
		"--user-data-dir="+filepath.Join(dir, "profile"),
		"--no-pdf-header-footer",
		// the headings, as the outline PDF viewers show in their sidebar
		"--generate-pdf-document-outline",
		// time for scripts to finish, like Mermaid's drawing, before printing
		"--virtual-time-budget=10000",
		"--print-to-pdf="+tempPDF,
		pageURL.String(),
	)
	if output, err := cmd.CombinedOutput(); err != nil {
		// Error wrapping (`%w`): unlike `%v` or `%s`, the `%w` verb in
		// `fmt.Errorf` wraps the original `err` inside the new error (like
		// Java's `new Exception(message, cause)`), preserving it for callers
		// that inspect errors with `errors.Is` or `errors.As`.
		return fmt.Errorf("printing PDF with %s: %w\n%s", chrome, err, output)
	}

	// Chrome can exit cleanly without printing, so check it did; and only
	// replace any existing PDF once there's a new one
	pdf, err := os.ReadFile(tempPDF)
	if err != nil {
		return fmt.Errorf("printing PDF with %s: no PDF written", chrome)
	}
	return os.WriteFile(pdfName, pdf, 0644)
}

// pdfTheme picks a PDF's colors: those asked for, or else the OS's, since
// headless Chrome can't follow the OS itself
func pdfTheme(th theme) theme {
	switch {
	case th != auto:
		return th
	case osPrefersDark():
		return dark
	default:
		return light
	}
}

// osPrefersDark reports whether the OS is set to dark mode, as far as can be
// told; a variable so tests can stand in for the OS.
//
// Go testing idiom (function variable as a seam):
//   In Java, making `pdfTheme` testable without shelling out to the OS would
//   typically require defining an `OSThemeDetector` interface, a production
//   implementation class, and injecting a mock into `pdfTheme`. In Go, when
//   there is only a single function to stub in tests, it's common to declare
//   it as a package-level function variable (`var osPrefersDark = func() bool`)
//   that tests can temporarily reassign (see `TestPDFTheme` in `main_test.go`).
var osPrefersDark = func() bool {
	var out []byte
	var err error
	switch runtime.GOOS {
	case "darwin":
		out, err = exec.Command("defaults", "read", "-g", "AppleInterfaceStyle").Output()
		return err == nil && strings.TrimSpace(string(out)) == "Dark"
	case "linux":
		out, err = exec.Command("gsettings", "get", "org.gnome.desktop.interface", "color-scheme").Output()
		return err == nil && strings.Contains(string(out), "dark")
	case "windows":
		out, err = exec.Command("reg", "query", `HKCU\Software\Microsoft\Windows\CurrentVersion\Themes\Personalize`,
			"/v", "AppsUseLightTheme").Output()
		return err == nil && strings.Contains(string(out), "0x0")
	}
	return false
}

// findChrome returns the path of a Chromium-based browser to print with:
// $MDP_CHROME if set, otherwise the first one found in the usual places
func findChrome() (string, error) {
	if path := os.Getenv("MDP_CHROME"); path != "" {
		return path, nil
	}

	if runtime.GOOS == "darwin" {
		home, _ := os.UserHomeDir()
		for _, app := range []string{"Google Chrome", "Chromium", "Microsoft Edge", "Brave Browser"} {
			bin := filepath.Join("Applications", app+".app", "Contents", "MacOS", app)
			for _, path := range []string{"/" + bin, filepath.Join(home, bin)} {
				if _, err := os.Stat(path); err == nil {
					return path, nil
				}
			}
		}
	}

	for _, name := range []string{"google-chrome", "google-chrome-stable", "chromium", "chromium-browser", "microsoft-edge", "chrome"} {
		if path, err := exec.LookPath(name); err == nil {
			return path, nil
		}
	}

	return "", errors.New("making a PDF needs Chrome, Chromium, Edge or Brave; " +
		"if one is installed somewhere unusual, set MDP_CHROME to its path")
}
