// Package diagnose interprets a causal forest: what kind of failure each
// finding is, how completely it is accounted for, and whether it is worth the
// reader's attention at all.
//
// # The problem it exists to solve
//
// Every capture in the corpus carries three findings that are real, tiny and
// meaningless -- a single eviction, a single failed mount, a three-event
// readiness blip. They must not be reported, or 01-healthy.jsonl announces
// three problems on a healthy cluster. But 05-test-b contains evictions with
// byte-identical shape that are the whole incident, and a node condition, also
// byte-identical, that is the most important line in the file.
//
//	01  data-pipeline/Evicted[memory-pressure]  n=1  pods=1  span=0  root, no children
//	05  auth-service/Evicted[disk-pressure]     n=1  pods=1  span=0  child of node-4
//	05  node-4/NodeHasDiskPressure              n=1  pods=0  span=0  root, six children
//
// No threshold separates those three, because on shape there is nothing to
// separate. The forest is what separates them, which is why this stage runs
// after link rather than instead of it.
//
// # Prohibitions
//
// diagnose does not delete. Suppression is a flag on the verdict, the count is
// disclosed in the header, and every finding stays in the chart -- the same rule
// the pipeline already follows for noise records and skipped lines. It does not
// re-read the record stream, mutate a finding, or re-derive causality; it reads
// the forest it is handed.
//
// # Complexity
//
// Build is O(n²) with n findings: each verdict calls Children, which scans.
// Captures produce 3 to 10 findings, so this is tens of comparisons. Space is
// O(n) verdicts at 3 bytes each.
package diagnose

import (
	"time"

	"github.com/troglodytto/daiquiri/internal/classify"
	"github.com/troglodytto/daiquiri/internal/event"
	"github.com/troglodytto/daiquiri/internal/group"
	"github.com/troglodytto/daiquiri/internal/link"
)

// The bounds of "transient", each derived from the corpus rather than chosen.
//
// Measured across all six captures: the three background findings in every file
// run 1-3 occurrences on 1 pod over 0-16s, and every real problem runs 18-222
// occurrences across 3-6 pods over 7m49s or more.
//
// They are a conjunction, not alternatives. Volume, blast radius and duration
// are independent axes: 05-test-b's disk-pressure eviction of data-pipeline is
// three occurrences -- small -- across three pods over four and a half minutes,
// and calling that a blip because the count is low would be wrong.
const (
	// transientCount sits mid-gap between the background ceiling of 3 and the
	// smallest real problem at 18.
	transientCount = 10

	// transientPods is not a tuned threshold. It is the boundary between one
	// instance misbehaving and the workload misbehaving. The comparison is <=
	// rather than == because a Node or Deployment finding carries no pods at
	// all, and a node condition must be able to be small -- what keeps it is
	// that it explains things, not a pretence that it is large.
	transientPods = 1

	// transientSpan decides nothing on this corpus: no finding under the other
	// two bounds has a span over 16s. It is here because a definition of
	// "transient" that ignores duration would print "transient blip" beside a
	// pod that has failed once a minute for twenty minutes. Its bounds are still
	// observed -- 3.75x above the background ceiling, 4.5x below the shortest
	// real problem.
	transientSpan = 60 * time.Second
)

// insufficientMarker is what the scheduler writes when the cluster is full:
// "0/6 nodes are available: 6 Insufficient cpu". Its absence is what separates a
// capacity failure from a taint or affinity one, which sends the reader to a
// different dashboard entirely.
const insufficientMarker = "Insufficient"

// Pattern is the failure's category, in the brief's own vocabulary.
type Pattern uint8

// Patterns, in the order the table tries them.
const (
	// PatternNone is a deliberate abstention, not an unset value. A deploy
	// marker is an anchor rather than a failure, and an unclassified reason is
	// one the taxonomy could not read -- handing either a diagnosis would be
	// confident and wrong.
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

// Confidence is how completely the capture accounts for a finding.
//
// It is decided by what the incident's root *is*, never by when it happened, so
// it needs no threshold. It maps directly onto the confidence line the analysis
// report has to state, and onto what would raise it.
type Confidence uint8

// Confidence levels, weakest first.
const (
	// ConfidenceUnexplained is a failure with nothing before it and nothing
	// after it. Raised by a longer capture, or by logs the tool cannot see.
	ConfidenceUnexplained Confidence = iota
	// ConfidencePartial is a failure that explains other findings but is itself
	// unexplained: the proximate cause is known and its origin is not. 02's
	// OOM kills explain the crash-loop; what drove the memory growth is not in
	// the capture.
	ConfidencePartial
	// ConfidenceExplained is an incident whose root is a rollout or a node
	// condition -- a trigger the capture names outright.
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

// Diagnosis is the verdict on one finding.
type Diagnosis struct {
	// Pattern is the failure's category, or PatternNone where naming one would
	// be a guess.
	Pattern Pattern

	// Confidence is inherited from the incident's root: a finding is only as
	// explained as the thing that explains it.
	Confidence Confidence

	// Suppressed marks background noise the report should not lead with. The
	// finding is still present and still counted -- see the package prohibition.
	Suppressed bool

	// Signatures are the distinct things the finding's records say, ranked most
	// informative first. This is the "why" the pattern alone cannot give: 222
	// readiness failures reduce to three signatures, and the first of them is
	// "HTTP probe failed with statuscode: 404".
	Signatures []Signature
}

// Leading returns the most informative signature, if there is one.
func (d Diagnosis) Leading() (Signature, bool) {
	if len(d.Signatures) == 0 {
		return Signature{}, false
	}

	return d.Signatures[0], true
}

// Chart is a forest with a verdict on every finding.
//
// Forest is embedded rather than held in a field, so a Chart is usable wherever
// a Forest was and the three index-coupled slices travel as one value. Slices
// that can be separated eventually get sorted apart, and the failure mode there
// is a wrong diagnosis rather than a crash.
type Chart struct {
	link.Forest

	// Diagnoses is parallel to Forest.Findings: Diagnoses[i] judges Findings[i].
	Diagnoses []Diagnosis

	// CaptureEnd is the timestamp of the last record in the capture, including
	// the lifecycle noise no finding was built from.
	//
	// Needed to say whether an incident is ongoing, which no amount of staring
	// at the findings can answer: a stage cannot tell "still failing" from
	// "stopped failing" without knowing when the observation stopped. Zero
	// disables the distinction rather than guessing.
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
			Suppressed: c.suppressed(i),
			Signatures: signaturesOf(f.Findings[i]),
		}
	}

	return c
}

// Reported returns the indices the report should render, in order.
func (c Chart) Reported() []int {
	out := make([]int, 0, len(c.Diagnoses))

	for i := range c.Diagnoses {
		if !c.Diagnoses[i].Suppressed {
			out = append(out, i)
		}
	}

	return out
}

// SuppressedCount returns how many findings were held back as background.
//
// The header states it. A suppressed finding that is never counted is a fact
// that silently left the building, and that is what makes an aggressive
// threshold dangerous rather than merely wrong.
func (c Chart) SuppressedCount() int {
	return len(c.Diagnoses) - len(c.Reported())
}

// ShareExplanation reports whether every finding in kids is explained the same
// way -- same causal rule, same reason, same leading signature.
//
// The question is asked of the rule rather than of the evidence text, because
// evidence is display prose: 05-test-b's six evictions all fired
// node-condition-named and all differ in the elapsed time they quote.
//
// Where it holds, the renderer states the explanation once and gives each child
// a single line. Where any child differs on any of the three it does not hold,
// so a child telling a different story can never be folded into its siblings.
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

// patternRule is one row of the pattern table.
//
// Data, like the event taxonomy and the causal rules: adding a pattern is adding
// a row. Order is priority, and it runs mechanism-first -- a pattern that names
// *how* the failure works outranks one that names *what set it off*, because the
// trigger is already stated by the causal edge in far more detail than a
// one-word label could carry, and the mechanism is stated nowhere else.
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

	// A container restarting on a loop. Keyed on the rule rather than the
	// reason, which is the whole purpose of classify carrying a rule ID: BackOff
	// is both a crash-loop and an image-pull retry, and the brief names those as
	// two different patterns.
	//
	// The transient test keeps the word "sustained" honest. One OOM kill is an
	// incident; a loop is that kill happening over and over.
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
	// sees one node failure rather than seven unrelated warnings.
	{
		name:    "node-issue",
		pattern: PatternNodeIssue,
		matches: func(c Chart, i int) bool {
			return c.Findings[c.RootOf(i)].Kind == event.KindNode
		},
	},

	// The incident begins at a rollout. Last of the causal patterns because a
	// rollout is a trigger rather than a mechanism.
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
	// have no basis to categorise -- labelling either is the confident wrongness
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

// suppressed reports whether finding i is background the report should not lead
// with.
func (c Chart) suppressed(i int) bool {
	// Recognised failures only, and for two different reasons.
	//
	// A deploy marker with no children is a rollout that broke nothing, which is
	// worth a line rather than a hiding.
	//
	// An unrecognised reason is the stronger case. Suppressing a finding is a
	// claim to understand it well enough to know it does not matter, and the
	// whole point of the classification fallback is that we do not understand
	// this one. 06-test-c plants the argument in the record's own body: "In case
	// there are some new events that we haven't really recognized and handled,
	// we'd much rather surface it, instead of burying it." It is transient,
	// unexplained and childless on every measure, and burying it on shape would
	// answer that record by doing exactly what it asks us not to.
	if !diagnosable(c.Findings[i]) {
		return false
	}

	// Small, explained by nothing, and explaining nothing. The last clause is
	// load-bearing: without it 05-test-b's node condition -- one occurrence, no
	// pods, zero span -- is suppressed, and the six evictions hanging off it go
	// with it.
	return transient(c.Findings[i]) &&
		c.IsRoot(i) &&
		len(c.Children(i)) == 0
}

// diagnosable reports whether this stage may reach a verdict about a finding at
// all -- whether it is a failure, and one the taxonomy could actually read.
//
// Both callers ask the same question and must not drift apart: a finding we
// decline to categorise is exactly a finding we may not dismiss.
func diagnosable(f group.Finding) bool {
	return f.Category == classify.CategoryIssue && f.Recognised
}

// transient reports whether a finding is small on all three axes.
func transient(f group.Finding) bool {
	return f.Count <= transientCount &&
		len(f.Pods) <= transientPods &&
		f.LastSeen.Sub(f.FirstSeen) <= transientSpan
}
