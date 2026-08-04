package classify

import "github.com/troglodytto/daiquiri/internal/event"

// Category is what the pipeline should do with an event.
type Category int

// Categories. The filter categorizes rather than deletes, so that a record can
// be excluded from the report while still being available to correlation.
const (
	// CategoryNoise is normal lifecycle traffic. Retained by the caller but
	// never narrated: it is the evidence a trail is built from, not a finding.
	CategoryNoise Category = iota
	// CategoryUnclassified is a named reason the taxonomy does not cover, at a
	// severity too low to call an issue. It is reported, demoted, rather than
	// buried -- a reason we cannot interpret is a gap in the taxonomy, and
	// silently bucketing it as lifecycle is how a novel failure mode goes
	// unnoticed. Grouping collapses many such records into one finding per
	// reason, so disclosing them costs one line, not one line per record.
	CategoryUnclassified
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

	case CategoryUnclassified:
		return "UNCLASSIFIED"

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

	// Meaning is what the failure amounts to for whoever owns the service,
	// stated without Kubernetes vocabulary. Empty for the fallback: a reason we
	// could not interpret is a reason we cannot explain.
	Meaning string

	// Fix is the next move for an engineer holding the pager, carried from the
	// taxonomy row. It may contain {workload}, {namespace}, {node} and {pod}
	// placeholders, substituted by whoever renders it.
	//
	// Empty where the taxonomy has nothing useful to say, which is always true
	// of the fallback: a reason we could not interpret is a reason we cannot
	// advise on.
	Fix string

	// Rule identifies the taxonomy row that produced this verdict.
	//
	// Grouping keys on it so that two events sharing a reason but matching
	// different rules stay separate findings. In 05-test-b one workload is
	// Evicted twice for genuinely different reasons -- once for node memory
	// pressure as background noise, later as part of a node-wide disk pressure
	// incident -- and merging them drags the incident's start time thirteen
	// minutes earlier than the cause that explains it.
	//
	// Deliberately not Cause: that is display text, and rewording a sentence
	// must never change how records coalesce.
	Rule string

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
				Meaning:    r.meaning,
				Fix:        r.fix,
				Rule:       r.id,
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
// A warning we do not recognise is surfaced as an issue: the cost of one
// spurious low-confidence finding is far lower than the cost of silently
// dropping a novel failure mode.
//
// A *named* reason at Normal severity is surfaced too, demoted, rather than
// swept in with lifecycle traffic. Kubernetes Normal traffic is overwhelmingly
// routine, but a reason absent from the taxonomy is a gap in our
// interpretation, not a fact about the cluster -- and grouping collapses every
// occurrence of it into a single finding, so admitting it costs one line.
//
// An *unnamed* reason is the one case that stays noise. There is no reason
// string to report, so a finding for it would say nothing a reader could act
// on.
//
// Recognised is false throughout, so the report can also state in aggregate how
// much of the capture it could not interpret.
func fallback(e event.Event) Classification {
	if e.Severity >= event.SeverityWarning {
		return Classification{
			Category:   CategoryIssue,
			Severity:   event.SeverityWarning,
			Cause:      "unrecognised warning reason; classified conservatively",
			Rule:       "unrecognised-warning",
			Recognised: false,
		}
	}

	if e.Reason == "" {
		return Classification{Category: CategoryNoise, Severity: event.SeverityInfo, Rule: "lifecycle"}
	}

	return Classification{
		Category:   CategoryUnclassified,
		Severity:   event.SeverityInfo,
		Cause:      "reason is not in the taxonomy; surfaced uninterpreted rather than dropped",
		Rule:       "unclassified",
		Recognised: false,
	}
}
