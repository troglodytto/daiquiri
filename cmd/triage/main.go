package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/troglodytto/daiquiri/internal/classify"
	"github.com/troglodytto/daiquiri/internal/report"
	"github.com/troglodytto/daiquiri/internal/triage"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stderr))
}

// Exit codes; see the package comment for the contract these belong to.
const (
	exitOK    = 0
	exitFail  = 1
	exitUsage = 2
)

// version is overridden at build time via -ldflags "-X main.version=...".
var version = "dev"

func run(args []string, stderr io.Writer) int {
	flagSet := flag.NewFlagSet("triage", flag.ContinueOnError)
	flagSet.SetOutput(stderr)

	// Both views by default. The flags narrow, they do not enable: a reader who
	// wants only one is expressing a preference, and a reader who passes neither
	// should not have to know the tool had a second thing to show them.
	tableOnly := flagSet.Bool("table", false,
		"render only the prioritised summary table")
	treeOnly := flagSet.Bool("tree", false,
		"render only the causal incident view: root cause, impact, how we got there, "+
			"and the recommended next move")

	flagSet.Usage = func() {
		// Nothing useful can be done if stderr itself is broken.
		_, _ = fmt.Fprintf(stderr, "usage: triage [flags] <filename.jsonl>\n\nversion: %s\nflags:\n", version)
		flagSet.PrintDefaults()
	}

	if err := flagSet.Parse(args); err != nil {
		return exitUsage
	}

	if flagSet.NArg() != 1 {
		_, _ = fmt.Fprintf(stderr, "filename is required\n")
		flagSet.Usage()
		return exitUsage
	}

	path := flagSet.Arg(0)
	res, err := analyse(path)

	if err != nil {
		_, _ = fmt.Fprintf(stderr, "triage: %v\n", err)
		return exitFail
	}

	if *tableOnly && *treeOnly {
		_, _ = fmt.Fprintf(stderr, "--table and --tree are mutually exclusive; pass neither for both\n")
		return exitUsage
	}

	renderer := report.New(os.Stdout)

	switch {
	case *tableOnly:
		renderer = renderer.Only(report.ViewTable)
	case *treeOnly:
		renderer = renderer.Only(report.ViewTree)
	}

	if err := renderer.Render(res); err != nil {
		_, _ = fmt.Fprintf(stderr, "triage: writing report: %v\n", err)
		return exitFail
	}

	return exitOK
}

// readBufferBytes sizes the buffered reader wrapping the input file.
//
// Ingest is dominated by JSON parsing, not I/O -- reading all 16MB of a capture
// without parsing it measures 2.2ms against 115ms for the full decode -- so
// this is comfortably large rather than tuned. Raising it further has nothing
// left to win.
const readBufferBytes = 256 * 1024

func analyse(path string) (triage.Result, error) {
	// fmt.Printf("Analysing %s\n", path)
	file, err := os.Open(path)
	if err != nil {
		return triage.Result{}, err
	}

	// Close errors on a read-only handle carry no information a caller could
	// act on; the read result already told us whether we got the data.
	defer func() { _ = file.Close() }()

	res, err := triage.New(classify.New()).Run(bufio.NewReaderSize(file, readBufferBytes))
	if err != nil {
		return triage.Result{}, fmt.Errorf("reading %s: %w", path, err)
	}

	return res, nil
}
