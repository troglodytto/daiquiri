// Package diagnose reads a causal forest and judges each finding: what kind of
// failure it is, how well the capture accounts for it, and whether it belongs in
// the report at all.
//
//	01  data-pipeline/Evicted[memory-pressure]  n=1  pods=1  span=0  root, no children
//	05  auth-service/Evicted[disk-pressure]     n=1  pods=1  span=0  child of node-4
//	05  node-4/NodeHasDiskPressure              n=1  pods=0  span=0  root, six children
//
// Only the edges tell them apart, so this stage runs after link. See D-44.
package diagnose

import (
	"time"

	"github.com/troglodytto/daiquiri/internal/classify"
	"github.com/troglodytto/daiquiri/internal/event"
	"github.com/troglodytto/daiquiri/internal/group"
	"github.com/troglodytto/daiquiri/internal/link"
)

// What counts as transient. All three must hold; see D-43 for the derivation.
//
// Measured over the six captures: background findings run 1-3 occurrences on 1
// pod over 0-16s. Real problems run 18-222 occurrences across 3-6 pods over
// 7m49s or more.
const (
	// Mid-gap between the background ceiling of 3 and the smallest real
	// problem at 18.
	transientCount = 10

	// One instance misbehaving, against the workload misbehaving. The
	// comparison is <= because Node and Deployment findings carry no pods.
	transientPods = 1

	// Nothing in this corpus is decided by the span bound. It stops the tool
	// calling a pod that has failed once a minute for twenty minutes a blip.
	transientSpan = 60 * time.Second
)

// insufficientMarker appears when the cluster is full: "0/6 nodes are
// available: 6 Insufficient cpu". Without it, a FailedScheduling is a taint or
// affinity problem, which is a different fix.
const insufficientMarker = "Insufficient"

// Pattern is the failure's category, in the brief's own vocabulary.
type Pattern uint8

// Patterns, in the order the table tries them.
const (
	// PatternNone is an abstention. A deploy marker is an anchor, and an
	// unrecognised reason is one we have no basis to label.
	PatternNone Pattern = iota
	// PatternCapacity is a pod the scheduler cannot place because the cluster
	// has no room.
	PatternCapacity
	// PatternCrashLoop is a container failing and restarting, repeatedly, over
	// a sustained period.
	PatternCrashLoop
	// PatternNodeIssue is a failure whose incident begins at a node condition.
	PatternNodeIssue
	// PatternDeployCorrelated is a failure whose incident begins at a rollout.
	PatternDeployCorrelated
	// PatternTransient is small on every axis and connected to nothing.
	PatternTransient
)

// String returns the label used in rendered output.
func (p Pattern) String() string {
	switch p {
	case PatternCapacity:
		return "capacity issue"

	case PatternCrashLoop:
		return "sustained crash-loop"

	case PatternNodeIssue:
		return "node issue"

	case PatternDeployCorrelated:
		return "deploy-correlated failure"

	case PatternTransient:
		return "transient blip"

	default:
		return ""
	}
}

// Confidence is how completely the capture accounts for a finding. Decided by
// what the incident's root is, so it needs no threshold. See D-34.
type Confidence uint8

// Confidence levels, weakest first.
const (
	// ConfidenceUnexplained is a failure with nothing before it and nothing
	// after it. A longer capture would raise it.
	ConfidenceUnexplained Confidence = iota
	// ConfidencePartial is a failure that explains others but is itself
	// unexplained. 02's OOM kills explain the crash-loop; what drove the memory
	// growth is off the front of the capture.
	ConfidencePartial
	// ConfidenceExplained is an incident rooted in a rollout or node condition,
	// which the capture names outright.
	ConfidenceExplained
)

// String returns the label used in rendered output.
func (c Confidence) String() string {
	switch c {
	case ConfidenceExplained:
		return "explained"

	case ConfidencePartial:
		return "partially explained"

	default:
		return "unexplained"
	}
}

// Suppression is how each clause of the predicate evaluated. Published so a
// reader can see which clause decided a verdict and argue with that one.
type Suppression struct {
	// Diagnosable is false for a deploy marker and for any reason the taxonomy
	// could not read.
	Diagnosable bool
	// Transient is small on count, pods and span, all three.
	Transient bool
	// Root means nothing in the capture explains it.
	Root bool
	// Childless means it explains nothing.
	Childless bool
}

// Config reports the thresholds in force, so output can publish the numbers
// behind its verdicts. See D-43 and D-54 for how each was derived.
type Config struct {
	TransientCount     int
	TransientPods      int
	TransientSpan      time.Duration
	StillFailingWithin time.Duration
}

// Settings returns the thresholds in force.
func Settings() Config {
	return Config{
		TransientCount:     transientCount,
		TransientPods:      transientPods,
		TransientSpan:      transientSpan,
		StillFailingWithin: stillFailingWithin,
	}
}

// Diagnosis is the verdict on one finding.
type Diagnosis struct {
	// Pattern is the failure's category, or PatternNone where naming one would
	// be a guess.
	Pattern Pattern

	// Confidence is inherited from the incident's root: a finding is only as
	// explained as the thing that explains it.
	Confidence Confidence

	// Because records how each clause of the suppression predicate evaluated.
	// Suppressed is derived from it, so a verdict cannot contradict its own
	// reasons. See D-56.
	Because Suppression

	// Cadence is how often the finding recurs per object, and which way that
	// interval is moving. Zero below the sample minimum.
	Cadence Cadence

	// Signatures are the distinct things the records say, most informative
	// first. 04's 222 readiness failures reduce to three, led by "HTTP probe
	// failed with statuscode: 404".
	Signatures []Signature
}

// Suppressed reports whether this finding is background. It stays in the chart
// and stays counted.
func (d Diagnosis) Suppressed() bool { return d.Because.holds() }

// Leading returns the most informative signature, if there is one.
func (d Diagnosis) Leading() (Signature, bool) {
	if len(d.Signatures) == 0 {
		return Signature{}, false
	}

	return d.Signatures[0], true
}

// Chart is a forest with a verdict on every finding.
type Chart struct {
	link.Forest

	// Diagnoses is parallel to Forest.Findings: Diagnoses[i] judges Findings[i].
	Diagnoses []Diagnosis

	// CaptureEnd is the last record's timestamp, lifecycle noise included.
	// Without it there is no way to tell "still failing" from "stopped". Zero
	// disables the distinction.
	CaptureEnd time.Time
}

// Build judges every finding in f. captureEnd is the last record's timestamp;
// the zero value disables the still-failing distinction.
func Build(f link.Forest, captureEnd time.Time) Chart {
	c := Chart{Forest: f, Diagnoses: make([]Diagnosis, len(f.Findings)), CaptureEnd: captureEnd}

	// The verdict functions read only the embedded forest, so filling the slice
	// as we go cannot make an earlier verdict influence a later one.
	for i := range f.Findings {
		c.Diagnoses[i] = Diagnosis{
			Pattern:    c.patternOf(i),
			Confidence: c.confidenceOf(i),
			Because:    c.suppressionOf(i),
			Cadence:    cadenceOf(f.Findings[i]),
			Signatures: signaturesOf(f.Findings[i]),
		}
	}

	return c
}

// Reported returns the indices the report should render, in order.
func (c Chart) Reported() []int {
	out := make([]int, 0, len(c.Diagnoses))

	for i := range c.Diagnoses {
		if !c.Diagnoses[i].Suppressed() {
			out = append(out, i)
		}
	}

	return out
}

// SuppressedCount returns how many findings were held back as background.
func (c Chart) SuppressedCount() int {
	return len(c.Diagnoses) - len(c.Reported())
}

// ShareExplanation reports whether every finding in kids has the same causal
// rule, reason and leading signature.
func (c Chart) ShareExplanation(kids []int) bool {
	if len(kids) < 2 {
		return false
	}

	rule := c.Edges[kids[0]].Rule
	reason := c.Findings[kids[0]].Reason

	lead, ok := c.Diagnoses[kids[0]].Leading()
	if !ok {
		return false
	}

	for _, k := range kids[1:] {
		other, ok := c.Diagnoses[k].Leading()
		if !ok || c.Edges[k].Rule != rule || c.Findings[k].Reason != reason || other.Text != lead.Text {
			return false
		}
	}

	return true
}

// patternRule is one row of the pattern table. Adding a pattern is adding a
// row.
//
// Order is priority, mechanism first: the trigger is already spelled out by the
// causal edge, and the mechanism appears nowhere else. See D-42.
type patternRule struct {
	name    string
	pattern Pattern
	matches func(c Chart, i int) bool
}

var patternRules = []patternRule{
	// The cluster is full. Both halves are required: FailedScheduling also
	// covers taints and affinity rules, and reporting "capacity issue" for those
	// sends the reader to go add nodes they do not need.
	{
		name:    "capacity",
		pattern: PatternCapacity,
		matches: func(c Chart, i int) bool {
			return c.Findings[i].Rule == classify.RuleFailedScheduling &&
				c.Findings[i].BodyContains(insufficientMarker)
		},
	},

	// A container restarting on a loop. Keyed on the rule, because BackOff is
	// both a crash-loop and an image-pull retry and the brief names those as
	// different patterns.
	//
	// The transient test keeps "sustained" honest: one OOM kill is an incident,
	// a loop is that kill over and over.
	{
		name:    "crash-loop",
		pattern: PatternCrashLoop,
		matches: func(c Chart, i int) bool {
			switch c.Findings[i].Rule {
			case classify.RuleCrashLoop, classify.RuleOOMKilled:
				return !transient(c.Findings[i])

			default:
				return false
			}
		},
	},

	// The incident begins at a node condition. Inherited from the root, so the
	// condition and all seven pods it evicted carry one label and the reader
	// sees one node failure, not seven warnings.
	{
		name:    "node-issue",
		pattern: PatternNodeIssue,
		matches: func(c Chart, i int) bool {
			return c.Findings[c.RootOf(i)].Kind == event.KindNode
		},
	},

	// The incident begins at a rollout. Last of the causal patterns because a
	// rollout is a trigger, not a mechanism.
	{
		name:    "deploy-correlated",
		pattern: PatternDeployCorrelated,
		matches: func(c Chart, i int) bool {
			return c.Findings[c.RootOf(i)].Category == classify.CategoryDeployMarker
		},
	},

	// Shape only, and therefore last: it is what remains once nothing else
	// explains the finding.
	{
		name:    "transient",
		pattern: PatternTransient,
		matches: func(c Chart, i int) bool {
			return transient(c.Findings[i])
		},
	},
}

// patternOf categorises finding i.
func (c Chart) patternOf(i int) Pattern {
	// Only a recognised failure gets a diagnosis. A deploy marker is an anchor
	// rather than a failure, and a reason the taxonomy could not read is one we
	// have no basis to categorise; labelling either is the confident wrongness
	// the classification fallback exists to avoid.
	if !diagnosable(c.Findings[i]) {
		return PatternNone
	}

	for _, r := range patternRules {
		if r.matches(c, i) {
			return r.pattern
		}
	}

	return PatternNone
}

// confidenceOf reports how completely the capture accounts for finding i.
func (c Chart) confidenceOf(i int) Confidence {
	root := c.RootOf(i)

	// A rollout or a node condition is a trigger the capture names outright.
	// Anything else at the top of an incident is itself a failure, and a failure
	// with nothing before it is a trail that runs off the front of the capture.
	if c.Findings[root].Category == classify.CategoryDeployMarker ||
		c.Findings[root].Kind == event.KindNode {
		return ConfidenceExplained
	}

	if len(c.Children(root)) > 0 {
		return ConfidencePartial
	}

	return ConfidenceUnexplained
}

// holds reports whether every clause of the predicate is satisfied.
func (s Suppression) holds() bool {
	return s.Diagnosable && s.Transient && s.Root && s.Childless
}

// suppressionOf evaluates each clause for finding i.
func (c Chart) suppressionOf(i int) Suppression {
	// Small, explained by nothing, explaining nothing. Recognised failures only.
	//
	// A deploy marker with no children is a rollout that broke nothing, worth a
	// line. And suppressing a finding claims we understand it well enough to
	// know it doesn't matter, which is the one thing the fallback tells us we
	// can't say. See D-46.
	//
	// The Childless clause carries 05: without it node-4's condition is
	// suppressed and the six evictions hanging off it go with it.
	return Suppression{
		Diagnosable: diagnosable(c.Findings[i]),
		Transient:   transient(c.Findings[i]),
		Root:        c.IsRoot(i),
		Childless:   len(c.Children(i)) == 0,
	}
}

// diagnosable reports whether this stage may reach a verdict about a finding at
// all; whether it is a failure, and one the taxonomy could actually read.
func diagnosable(f group.Finding) bool {
	return f.Category == classify.CategoryIssue && f.Recognised
}

// transient reports whether a finding is small on all three axes.
func transient(f group.Finding) bool {
	return f.Count <= transientCount &&
		len(f.Pods) <= transientPods &&
		f.LastSeen.Sub(f.FirstSeen) <= transientSpan
}
