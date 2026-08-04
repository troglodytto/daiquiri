package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/troglodytto/daiquiri/internal/classify"
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

	// Provisional rendering until the report stage lands: the header plus one
	// line per finding, in timeline order. Enough to check grouping against the
	// captures by eye; not the prioritised output the brief asks for.
	fmt.Println(res.Summarise())

	for _, f := range res.Findings {
		nodes := "-"
		if len(f.Nodes) > 0 {
			nodes = strings.Join(f.Nodes, ",")
		}

		fmt.Printf(
			"  %s  %-8s %-20s %-11s %-19s n=%-4d pods=%-2d nodes=%s\n",
			f.FirstSeen.UTC().Format("15:04:05.000"),
			f.Severity,
			f.Workload,
			f.Namespace,
			f.Reason,
			f.Count,
			len(f.Pods),
			nodes,
		)
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
