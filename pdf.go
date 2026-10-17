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
	defer os.RemoveAll(dir)

	htmlName := filepath.Join(dir, "page.html")
	if err := os.WriteFile(htmlName, page, 0644); err != nil {
		return err
	}
	pageURL := url.URL{Scheme: "file", Path: filepath.ToSlash(htmlName)}
	tempPDF := filepath.Join(dir, "page.pdf")

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
// told; a variable so tests can stand in for the OS
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
