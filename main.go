// mdp renders Markdown files as GitHub-styled HTML pages or PDFs and opens
// them in a browser.
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/alecthomas/chroma/v2/styles"
	flag "github.com/spf13/pflag"
)

// config is what the command line asks for
type config struct {
	filename    string
	skipPreview bool
	watch       bool
	output      string // where to write the page or PDF; "" for the default
	pdf         bool
	opts        options
}

// main simply parses flags and calls run, or serve to watch
func main() {
	cfg, err := parseArgs(os.Args[1:], os.Stderr)
	if errors.Is(err, flag.ErrHelp) {
		return
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	if cfg.watch {
		err = serve(cfg.filename, cfg.opts, os.Stdout, cfg.skipPreview)
	} else {
		err = run(cfg, os.Stdout)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// parseArgs reads the flags and the one file name from args, printing usage
// to stderr when they don't make sense
func parseArgs(args []string, stderr io.Writer) (config, error) {
	var cfg config
	var forceDark, forceLight bool
	var style, darkStyle string

	fs := flag.NewFlagSet("mdp", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.BoolVarP(&cfg.skipPreview, "skip", "s", false, "skip auto-preview; just print the file path or URL")
	fs.BoolVarP(&cfg.opts.toc, "toc", "t", false, "open the table of contents")
	fs.BoolVarP(&cfg.watch, "watch", "w", false, "watch the file and reload the preview when it changes")
	fs.StringVarP(&cfg.output, "output", "o", "", "write to `file` instead of a temp file (a .pdf name implies --pdf)")
	fs.BoolVarP(&cfg.pdf, "pdf", "p", false, "make a PDF, using Chrome; it goes beside FILE unless -o says otherwise")
	fs.BoolVarP(&forceDark, "dark", "d", false, "use dark colors instead of following the OS")
	fs.BoolVarP(&forceLight, "light", "l", false, "use light colors instead of following the OS")
	fs.StringVar(&style, "style", defaultStyle, "code highlighting `style`; for dark mode, its dark version if it has one")
	fs.StringVar(&darkStyle, "dark-style", "", "code highlighting `style` for dark mode only")
	fs.StringVar(&cfg.opts.template, "template", "", "alternate template `file`")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "Usage: mdp [flags] FILE")
		fs.PrintDefaults()
		fmt.Fprintln(stderr, "\nStyles:", strings.Join(styles.Names(), ", "))
	}

	if err := fs.Parse(args); err != nil {
		return cfg, err
	}
	if strings.EqualFold(filepath.Ext(cfg.output), ".pdf") {
		cfg.pdf = true
	}

	switch {
	case fs.NArg() != 1:
		fs.Usage()
		return cfg, errors.New("expected exactly one Markdown file")
	case cfg.watch && (cfg.pdf || cfg.output != ""):
		return cfg, errors.New("--watch can't be used with --output or --pdf")
	case forceDark && forceLight:
		return cfg, errors.New("--dark and --light can't be used together")
	case !knownStyle(style):
		return cfg, fmt.Errorf("unknown style %q; --help lists them", style)
	case darkStyle != "" && !knownStyle(darkStyle):
		return cfg, fmt.Errorf("unknown style %q; --help lists them", darkStyle)
	case forceDark:
		cfg.opts.theme = dark
	case forceLight:
		cfg.opts.theme = light
	default:
		cfg.opts.theme = auto
	}
	cfg.filename = fs.Arg(0)
	cfg.opts.lightStyle, cfg.opts.darkStyle = stylesFor(style)
	if darkStyle != "" {
		cfg.opts.darkStyle = darkStyle
	}

	return cfg, nil
}

// run executes the preview or PDF generation pipeline for the given config.
// out receives the resulting file path (normally os.Stdout, or bytes.Buffer in tests).
func run(cfg config, out io.Writer) error {
	input, err := os.ReadFile(cfg.filename)
	if err != nil {
		return err
	}

	opts := cfg.opts
	if cfg.pdf {
		opts.theme = pdfTheme(opts.theme)
	}
	htmlData, err := parseContent(input, cfg.filename, opts)
	if err != nil {
		return err
	}

	if cfg.pdf {
		return runPDF(cfg, htmlData, out)
	}

	outName := cfg.output
	if outName == "" {
		temp, err := os.CreateTemp("", "mdp*.html")
		if err != nil {
			return err
		}
		if err := temp.Close(); err != nil {
			return err
		}
		outName = temp.Name()
	}

	fmt.Fprintln(out, outName)

	if err := os.WriteFile(outName, htmlData, 0644); err != nil {
		return err
	}

	if cfg.skipPreview {
		return nil
	}

	if cfg.output == "" {
		defer os.Remove(outName)
	}

	return preview(outName)
}

// preview opens fname, a file path or URL, with the OS default program
func preview(fname string) error {
	var cName string
	var cParams []string

	switch runtime.GOOS {
	case "linux":
		cName = "xdg-open"
	case "windows":
		cName = "cmd.exe"
		cParams = []string{"/C", "start"}
	case "darwin":
		cName = "open"
	default:
		return fmt.Errorf("unsupported OS: %s", runtime.GOOS)
	}

	cParams = append(cParams, fname)
	err := exec.Command(cName, cParams...).Run()

	// Give the browser time to open a temp file before run's defer deletes it
	time.Sleep(2 * time.Second)
	return err
}
