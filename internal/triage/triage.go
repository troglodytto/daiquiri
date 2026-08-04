package triage

import (
	"fmt"
	"io"
	"time"

	"github.com/troglodytto/daiquiri/internal/classify"
	"github.com/troglodytto/daiquiri/internal/event"
	"github.com/troglodytto/daiquiri/internal/group"
	"github.com/troglodytto/daiquiri/internal/link"
	"github.com/troglodytto/daiquiri/internal/otel"
)

// recordsCapacityHint pre-sizes the retained record slice.
//
// Every provided capture is ~20,000 records, so one allocation covers a whole
// file. Purely an allocation hint -- correctness does not depend on it, and a
// larger capture simply grows the slice.
const recordsCapacityHint = 20 * 1024

// classifiedCapacityHint pre-sizes the slice of reportable records.
//
// The largest reportable count across the provided captures is 228 (04-test-a:
// 227 issues plus one deploy marker), so one allocation covers every one of
// them.
const classifiedCapacityHint = 256

// Classifier is what the pipeline needs from internal/classify.
//
// Declared here, at the consumer, rather than in the package that implements
// it. That is the Go convention and it earns its keep: the interface lists
// exactly what is called and nothing more, classify stays readable without a
// separate interface file, and a test substitutes a three-line fake defined at
// the test site without production code changing to accommodate it.
type Classifier interface {
	Classify(event.Event) classify.Classification
}

// Result reports what one run of the pipeline found.
type Result struct {
	// Ingested and Skipped come from the decoder. Skipped is disclosed rather
	// than swallowed so a truncated capture cannot look like a clean one.
	Ingested int
	Skipped  int

	// Cluster names the source cluster, for the report header. Taken from the
	// first record: a capture is one cluster's events, and nothing downstream
	// reasons about it, so disagreement between records is not worth detecting.
	Cluster string

	// Noise counts records classified as normal lifecycle traffic. They are
	// counted here for the header and retained in full in Records.
	Noise int

	// Unrecognised counts records whose reason is not in the taxonomy. They are
	// still categorised, conservatively, but the count is surfaced so the
	// report can say how much of the capture it could not interpret.
	Unrecognised int

	// Records is every decoded record, in the order the capture supplied them.
	//
	// Retained rather than filtered, because lifecycle events are evidence
	// rather than clutter. The interval between a pod's Started and its next
	// Killing is what gives a memory leak a fill rate -- 4m41s contracting to
	// 4m12s in 02-memory-leak -- and both endpoints are records the report
	// itself never prints. Roughly 1.6 MB for a 20,000-record capture, against
	// a 16 MB input file.
	Records []event.Event

	// Forest holds the coalesced reportable groups -- issues, deploy markers and
	// unclassified reasons -- together with the causal edge derived for each.
	// Lifecycle noise never becomes a finding.
	//
	// The findings and their edges are held as one value rather than two fields
	// because they are index-coupled: sorting one without the other would
	// produce a wrong diagnosis rather than a crash.
	Forest link.Forest

	// Elapsed is wall-clock time for the run.
	Elapsed time.Duration
}

// Summarise renders the one-line header.
func (res Result) Summarise() string {
	s := fmt.Sprintf(
		"Records ingested: %d | Filtered as noise: %d | Findings: %d",
		res.Ingested,
		res.Noise,
		len(res.Forest.Findings),
	)

	// Both numbers are disclosed rather than hidden: a truncated capture must
	// not be able to look clean, and neither must one the taxonomy could not
	// fully interpret.
	if res.Skipped > 0 {
		s += fmt.Sprintf(" | Skipped: %d", res.Skipped)
	}

	if res.Unrecognised > 0 {
		s += fmt.Sprintf(" | Unrecognised: %d", res.Unrecognised)
	}

	return s + fmt.Sprintf(" | %s", res.Elapsed.Round(time.Millisecond))
}

// Pipeline runs the ingest, classification and grouping stages in order.
//
// It holds the sequence and nothing else. If logic accumulates here, that logic
// belongs in a stage.
type Pipeline struct {
	classifier Classifier
}

// New returns a Pipeline that classifies with c.
func New(c Classifier) *Pipeline {
	return &Pipeline{classifier: c}
}

// Run streams the capture in r, classifies every record, coalesces the
// reportable ones, and reports the result.
//
// The decoder is constructed from r rather than injected: driving Run with a
// reader already exercises the real decoder, so a factory indirection would buy
// nothing. Injection is for what you cannot easily construct.
//
// A malformed record is counted and skipped. A stream failure returns a zero
// Result and an error, because a partial result presented as complete is worse
// than no result at all.
func (p *Pipeline) Run(r io.Reader) (Result, error) {
	start := time.Now()

	res := Result{Records: make([]event.Event, 0, recordsCapacityHint)}
	reportable := make([]group.Classified, 0, classifiedCapacityHint)

	d := otel.New(r)
	for {
		e, ok := d.Next()
		if !ok {
			break
		}

		if res.Cluster == "" {
			res.Cluster = e.Cluster
		}

		res.Records = append(res.Records, e)

		verdict := p.classifier.Classify(e)
		if !verdict.Recognised {
			res.Unrecognised++
		}

		// Noise is counted for the header but never coalesced. It stays in
		// Records, where a trail can reach it, and out of the findings, where
		// ~19,800 lifecycle records per capture would drown every real one.
		if verdict.Category == classify.CategoryNoise {
			res.Noise++
			continue
		}

		reportable = append(reportable, group.Classified{Event: e, Class: verdict})
	}

	if err := d.Err(); err != nil {
		return Result{}, fmt.Errorf("triage: decoding: %w", err)
	}

	res.Forest = link.Build(group.Coalesce(reportable))

	stats := d.Stats()
	res.Ingested = stats.Ingested
	res.Skipped = stats.Skipped
	res.Elapsed = time.Since(start)

	return res, nil
}
