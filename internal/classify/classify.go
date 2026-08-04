package classify

import "github.com/troglodytto/daiquiri/internal/event"

// Category is what the pipeline should do with an event.
type Category int

// Categories. The filter categorizes rather than deletes, so that a record can
// be excluded from the report while still being available to correlation.
const (
	// CategoryNoise is normal lifecycle traffic, discarded by the caller.
	CategoryNoise Category = iota
	// CategoryDeployMarker is not reportable on its own but is retained as a
	// correlation anchor: a rollout is the likeliest explanation for several
	// unrelated services failing at once.
	CategoryDeployMarker
	// CategoryIssue is reportable and reaches grouping.
	CategoryIssue
)

// String returns the uppercase label used in rendered output.
func (c Category) String() string {
	switch c {
	case CategoryNoise:
		return "NOISE"

	case CategoryDeployMarker:
		return "DEPLOY"

	case CategoryIssue:
		return "ISSUE"
	default:
		return "UNKNOWN"
	}
}

// Classification is the verdict on a single event.
type Classification struct {
	// Category decides whether the event is reported, retained, or dropped.
	Category Category

	// Severity is the finding-level severity, which may differ from the
	// event's own severity_text. A BackOff the API server calls Normal is a
	// repeated image-pull retry, which is critical.
	Severity event.Severity

	// Cause is a short interpretation for the report's likely-cause line,
	// written from the engineer's point of view rather than the data's. The
	// brief is explicit that counts without interpretation are half the work.
	Cause string

	// Recognised is false when no rule matched and the verdict came from the
	// fallback. It lets the report disclose unclassified reasons instead of
	// silently bucketing them, in the same spirit as the decoder's skip count.
	Recognised bool
}

// Classifier assigns a category and severity to each event, from a table.
//
// It holds no state. It is a struct rather than a bare function so that
// internal/triage can declare a narrow interface over it and substitute a fake,
// consistently with how every other stage is consumed.
type Classifier struct{}

// New returns a Classifier ready for use.
func New() *Classifier { return &Classifier{} }

// Classify returns the verdict for e.
//
// Classification is total: every event gets a Classification and there is no
// error path, because for no input is "cannot classify" more useful than the
// conservative fallback plus Recognised=false.
func (c *Classifier) Classify(e event.Event) Classification {
	rules, known := taxonomy[e.Reason]

	if !known {
		return fallback(e)
	}

	for _, r := range rules {
		if r.matches(e) {
			return Classification{
				Category:   r.category,
				Severity:   r.severity,
				Cause:      r.cause,
				Recognised: true,
			}
		}
	}

	// Unreachable while every reason's final rule is unconditional, which the
	// tests enforce. Falling back rather than panicking keeps a future
	// half-finished taxonomy row from taking the tool down mid-run.
	return fallback(e)
}

// fallback classifies an event whose reason is not in the taxonomy.
//
// A warning we do not recognise is surfaced rather than hidden: the cost of one
// spurious low-confidence finding is far lower than the cost of silently
// dropping a novel failure mode. An unrecognised Normal event is treated as
// lifecycle, because Kubernetes Normal traffic is overwhelmingly routine and
// admitting all of it would drown the report.
//
// Either way Recognised is false, so the report can say how much of the capture
// it could not interpret.
func fallback(e event.Event) Classification {
	if e.Severity >= event.SeverityWarning {
		return Classification{
			Category:   CategoryIssue,
			Severity:   event.SeverityWarning,
			Cause:      "unrecognised warning reason; classified conservatively",
			Recognised: false,
		}
	}
	return Classification{Category: CategoryNoise, Severity: event.SeverityInfo}
}
