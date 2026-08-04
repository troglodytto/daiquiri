package triage

import (
	"fmt"
	"io"
	"time"

	"github.com/troglodytto/daiquiri/internal/classify"
	"github.com/troglodytto/daiquiri/internal/event"
	"github.com/troglodytto/daiquiri/internal/otel"
)

// issuesCapacityHint pre-sizes the retained slices.
//
// The largest issue count across the provided captures is 227 (04-test-a), so
// one allocation covers every one of them. Purely an allocation hint --
// correctness does not depend on it.
const issuesCapacityHint = 256

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
//
// It is a value with no methods: everything on it is something cmd/triage or
// internal/report will print.
type Result struct {
	// Ingested and Skipped come from the decoder. Skipped is disclosed rather
	// than swallowed so a truncated capture cannot look like a clean one.
	Ingested int
	Skipped  int

	// Noise counts records dropped as normal lifecycle traffic.
	Noise int

	// Unrecognised counts records whose reason is not in the taxonomy. They are
	// still categorised, conservatively, but the count is surfaced so the
	// report can say how much of the capture it could not interpret.
	Unrecognised int

	// Issues are the reportable events. In Phase 3 this becomes a slice of
	// coalesced findings; carrying raw events until then keeps the phase
	// boundary honest rather than inventing a finding type before grouping
	// exists.
	Issues []event.Event

	// Markers are deploy markers -- not reportable on their own, retained
	// because a rollout is the likeliest explanation for several unrelated
	// services failing at once.
	Markers []event.Event

	// Elapsed is wall-clock time for the run.
	Elapsed time.Duration
}

func (res Result) Summarise() string {
	s := fmt.Sprintf(
		"Records ingested: %d | Filtered as noise: %d | Issues: %d",
		res.Ingested,
		res.Noise,
		len(res.Issues),
	)

	if len(res.Markers) > 0 {
		s += fmt.Sprintf(" | Deploys: %d", len(res.Markers))
	}

	// Both numbers are disclosed rather than hidden: a truncated capture must
	// not be able to look clean, and neither must one the taxonomy could not
	// fully interpret.
	if res.Skipped > 0 {
		s += fmt.Sprintf(" | Skipped: %d", res.Skipped)
	}

	if res.Unrecognised > 0 {
		s += fmt.Sprintf(" | Unrecognised: %d", res.Unrecognised)
	}

	return s + fmt.Sprintf(" | %s\n", res.Elapsed.Round(time.Millisecond))
}

// Pipeline runs the ingest and classification stages in order.
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

// Run streams the capture in r, classifies every record, and reports the result.
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

	res := Result{
		Issues:  make([]event.Event, 0, issuesCapacityHint), // we're pre allocating size = "issuesCapacityHint" as a preemptive measure
		Markers: make([]event.Event, 0),                     // Deployment markers, since there can be issues which originated after a certain deployment
	}

	d := otel.New(r)
	for {
		e, ok := d.Next()
		if !ok {
			break
		}

		verdict := p.classifier.Classify(e)
		if !verdict.Recognised {
			res.Unrecognised++
		}

		switch verdict.Category {
		case classify.CategoryIssue:
			res.Issues = append(res.Issues, e)

		case classify.CategoryDeployMarker:
			res.Markers = append(res.Markers, e)

		case classify.CategoryNoise:
			res.Noise++
		}
	}

	if err := d.Err(); err != nil {
		return Result{}, fmt.Errorf("triage: decoding: %w", err)
	}

	stats := d.Stats()
	res.Ingested = stats.Ingested
	res.Skipped = stats.Skipped
	res.Elapsed = time.Since(start)

	return res, nil
}
