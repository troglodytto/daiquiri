package report

import (
	"encoding/json"
	"io"
	"time"

	"github.com/troglodytto/daiquiri/internal/diagnose"
	"github.com/troglodytto/daiquiri/internal/event"
	"github.com/troglodytto/daiquiri/internal/link"
	"github.com/troglodytto/daiquiri/internal/triage"
)

// schemaVersion identifies the document shape.
//
// Present from the first release rather than added when it first breaks: a
// consumer that cannot tell which shape it is reading has to guess, and the
// point of this output is to be machine-read.
const schemaVersion = 1

// Document is the whole run, in a form you can argue with.
//
// The organising principle is that **every verdict sits next to the inputs that
// produced it**. A conclusion on its own is something you have to trust; a
// conclusion beside its evidence and the thresholds that were applied is
// something you can check, and disagree with, using nothing but this file.
//
// So each finding carries:
//
//   - the identity it was coalesced on, so you can see why these records are one
//     fact rather than several
//   - every member record, verbatim, so every count and time range is
//     recomputable rather than merely asserted
//   - the causal edge with the rule that fired and the evidence for it
//   - the diagnosis, and for suppression the value of each clause separately, so
//     "suppressed: true" can be traced to which clause decided it
//
// Suppressed findings are included, flagged. Excluding them would make the
// document agree with the tool by construction, which is the opposite of the
// point.
//
// Lifecycle noise is counted but not reproduced. It is roughly 19,800 of 20,000
// records, it is already in the input file byte for byte, and duplicating it
// would make this document larger than its own source while adding nothing that
// the source does not already hold. Everything the tool *concluded* is here;
// everything it *read* is in the file it read.
type Document struct {
	Schema  int     `json:"schema"`
	Tool    toolDoc `json:"tool"`
	Capture capture `json:"capture"`

	// Incidents are causal trees, ranked as the report ranks them. They index
	// into Findings rather than nesting it, so no finding is duplicated and a
	// consumer can walk either structure.
	Incidents []incident `json:"incidents"`

	// Findings is every reportable group, suppressed ones included, in the same
	// order and at the same indices the incidents refer to.
	Findings []finding `json:"findings"`
}

// toolDoc records what produced the document and under which numbers.
//
// The thresholds travel with the verdicts they decided. A threshold nobody can
// see is a threshold nobody can challenge.
type toolDoc struct {
	Name         string     `json:"name"`
	Version      string     `json:"version"`
	CausalWindow string     `json:"causal_window"`
	Thresholds   thresholds `json:"thresholds"`
}

// thresholds is diagnose.Config on the wire.
//
// Durations are strings, not nanosecond integers. This document is read by a
// person writing an analysis report at least as often as by a program, and
// "1m0s" is a number they can act on where 60000000000 is one they have to
// decode.
type thresholds struct {
	TransientCount     int    `json:"transient_max_count"`
	TransientPods      int    `json:"transient_max_pods"`
	TransientSpan      string `json:"transient_max_span"`
	StillFailingWithin string `json:"still_failing_within"`
}

type capture struct {
	Cluster string `json:"cluster"`

	// Counters. Skipped and Unrecognised are present even at zero, because
	// their absence and their being zero are different claims.
	Ingested     int `json:"records_ingested"`
	Skipped      int `json:"records_skipped"`
	Noise        int `json:"records_filtered_as_noise"`
	Unrecognised int `json:"records_unrecognised"`

	// End is the last record's timestamp, of any kind. It is what "still
	// failing" is measured against.
	End string `json:"observation_end"`

	ElapsedMS float64 `json:"elapsed_ms"`
}

type incident struct {
	Severity   string `json:"severity"`
	Confidence string `json:"confidence"`

	// Root, Mechanism and Paged index into Findings. They answer three different
	// questions and are three different findings; Paged is null when several
	// symptoms are equally bad and choosing one would be a fabrication.
	Root      int  `json:"root"`
	Mechanism int  `json:"mechanism"`
	Paged     *int `json:"paged"`

	Members []int `json:"members"`

	Events     int `json:"events"`
	Pods       int `json:"pods"`
	Workloads  int `json:"workloads"`
	Namespaces int `json:"namespaces"`

	First        string `json:"first_seen"`
	Last         string `json:"last_seen"`
	Span         string `json:"span"`
	StillFailing bool   `json:"still_failing"`

	// Remediation has its placeholders already resolved against the mechanism.
	Remediation string `json:"remediation,omitempty"`
}

type finding struct {
	Index int `json:"index"`

	Identity  identity  `json:"identity"`
	Verdict   verdict   `json:"verdict"`
	Shape     shape     `json:"shape"`
	Diagnosis diagnosis `json:"diagnosis"`

	Signatures []signature `json:"signatures"`

	// Edge is null for a root.
	Edge *edge `json:"edge"`

	// Records is every member, verbatim. This is what makes Shape checkable
	// rather than merely stated.
	Records []record `json:"records"`
}

// identity is the coalescing key: why these records are one finding.
type identity struct {
	Kind      string `json:"kind"`
	Workload  string `json:"workload"`
	Namespace string `json:"namespace"`
	Reason    string `json:"reason"`
	Rule      string `json:"rule"`
}

type verdict struct {
	Category   string `json:"category"`
	Severity   string `json:"severity"`
	Recognised bool   `json:"recognised"`

	// Cause is what the cluster did; Meaning is what that says about the
	// service, without Kubernetes vocabulary. Fix keeps its {placeholders}
	// unresolved here -- the resolved form is on the incident.
	Cause   string `json:"cause,omitempty"`
	Meaning string `json:"meaning,omitempty"`
	Fix     string `json:"fix,omitempty"`
}

type shape struct {
	// Count is occurrences, not records: the highest k8s.event.count seen per
	// distinct event UID, summed. It equals len(records) only for a watch-based
	// pipeline, which all six provided captures are.
	Count int `json:"occurrences"`

	Pods  []string `json:"pods"`
	Nodes []string `json:"nodes"`

	First string `json:"first_seen"`
	Last  string `json:"last_seen"`
	Span  string `json:"span"`
}

type diagnosis struct {
	// Pattern is empty where the tool declined to categorise -- a deploy marker
	// or a reason not in the taxonomy. Empty is an abstention, not an unset
	// value.
	Pattern    string `json:"pattern"`
	Confidence string `json:"confidence"`

	Suppressed bool `json:"suppressed"`

	// Because is why. Suppression holds only when all four are true, so a reader
	// who disagrees with the outcome can see which clause to argue with.
	Because because `json:"because"`
}

// because is diagnose.Suppression on the wire.
//
// The names are spelled as the questions they answer, because this object is
// the whole reason the document is auditable and it should read as an argument
// rather than as four flags.
type because struct {
	// Diagnosable: is this a recognised failure at all? False for a rollout, and
	// for any reason the taxonomy could not read -- and we never dismiss what we
	// could not read.
	Diagnosable bool `json:"is_a_recognised_failure"`

	// Transient: small on occurrences, pods and span, all three.
	Transient bool `json:"is_small_on_every_axis"`

	// Root: nothing in the capture explains it.
	Root bool `json:"nothing_explains_it"`

	// Childless: it explains nothing. Without this clause 05-test-b's node
	// condition is suppressed and takes its six evictions with it.
	Childless bool `json:"it_explains_nothing"`
}

type signature struct {
	// Text is a record body with its volatile tokens replaced: pod names,
	// addresses and object UIDs. Numbers with units are never normalised.
	Text  string `json:"text"`
	Count int    `json:"count"`

	// Specific marks a signature naming something concrete -- an image, an
	// amount, a status code -- as opposed to one restating the reason. Ranking
	// puts specific first, then frequency.
	Specific bool `json:"specific"`
}

type edge struct {
	Parent int    `json:"parent"`
	Kind   string `json:"kind"`
	Rule   string `json:"rule"`

	// Evidence is written to be checked against the capture rather than trusted.
	Evidence string `json:"evidence"`

	// Delay is how long after the parent this finding began.
	Delay string `json:"delay"`
}

type record struct {
	Timestamp string `json:"timestamp"`
	Severity  string `json:"severity"`
	Reason    string `json:"reason"`
	Body      string `json:"body"`
	Object    string `json:"object"`
	Node      string `json:"node,omitempty"`
	UID       string `json:"event_uid,omitempty"`
	Count     int    `json:"count"`
}

// WriteJSON encodes the whole run to w.
//
// Indented, because a human reads this too -- to write the analysis report, and
// to check the tool. jq works either way; a person diffing two runs does not.
func WriteJSON(w io.Writer, res triage.Result, version string) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")

	// Escaping is disabled because record bodies contain URLs, and a probe
	// address rendered as "http://10.244.11.243:8080<" is unreadable in the
	// one output whose purpose is to be read closely.
	enc.SetEscapeHTML(false)

	return enc.Encode(documentOf(res, version))
}

// documentOf builds the document. It derives nothing: every value is read from
// the result or is a formatting of one.
func documentOf(res triage.Result, version string) Document {
	c := res.Chart

	doc := Document{
		Schema: schemaVersion,
		Tool: toolDoc{
			Name:         "daiquiri",
			Version:      version,
			CausalWindow: link.Window().String(),
			Thresholds:   thresholdsOf(diagnose.Settings()),
		},
		Capture: capture{
			Cluster:      res.Cluster,
			Ingested:     res.Ingested,
			Skipped:      res.Skipped,
			Noise:        res.Noise,
			Unrecognised: res.Unrecognised,
			End:          stamp(c.CaptureEnd),
			ElapsedMS:    float64(res.Elapsed.Microseconds()) / 1000,
		},
		Incidents: make([]incident, 0, len(c.Findings)),
		Findings:  make([]finding, 0, len(c.Findings)),
	}

	for _, in := range c.Incidents() {
		doc.Incidents = append(doc.Incidents, incidentOf(c, in))
	}

	for i := range c.Findings {
		doc.Findings = append(doc.Findings, findingOf(c, i))
	}

	return doc
}

func thresholdsOf(c diagnose.Config) thresholds {
	return thresholds{
		TransientCount:     c.TransientCount,
		TransientPods:      c.TransientPods,
		TransientSpan:      c.TransientSpan.String(),
		StillFailingWithin: c.StillFailingWithin.String(),
	}
}

func incidentOf(c diagnose.Chart, in diagnose.Incident) incident {
	out := incident{
		// The incident's maximum, not the root's: 03's root is a rollout at INFO
		// and the outage beneath it is CRITICAL.
		Severity:     in.Severity.String(),
		Confidence:   in.Confidence.String(),
		Root:         in.Root,
		Mechanism:    in.Mechanism,
		Members:      in.Members,
		Events:       in.Events,
		Pods:         in.Pods,
		Workloads:    in.Workloads,
		Namespaces:   in.Namespaces,
		First:        stamp(in.First),
		Last:         stamp(in.Last),
		Span:         in.Span().Round(time.Millisecond).String(),
		StillFailing: in.StillFailing,
		Remediation:  c.Remediation(in),
	}

	if in.Paged >= 0 {
		paged := in.Paged
		out.Paged = &paged
	}

	return out
}

func findingOf(c diagnose.Chart, i int) finding {
	f, d := c.Findings[i], c.Diagnoses[i]

	out := finding{
		Index: i,
		Identity: identity{
			Kind:      f.Kind,
			Workload:  f.Workload,
			Namespace: f.Namespace,
			Reason:    f.Reason,
			Rule:      f.Rule,
		},
		Verdict: verdict{
			Category:   f.Category.String(),
			Severity:   f.Severity.String(),
			Recognised: f.Recognised,
			Cause:      f.Cause,
			Meaning:    f.Meaning,
			Fix:        f.Fix,
		},
		Shape: shape{
			Count: f.Count,
			Pods:  nonNil(f.Pods),
			Nodes: nonNil(f.Nodes),
			First: stamp(f.FirstSeen),
			Last:  stamp(f.LastSeen),
			Span:  f.LastSeen.Sub(f.FirstSeen).Round(time.Millisecond).String(),
		},
		Diagnosis: diagnosis{
			Pattern:    d.Pattern.String(),
			Confidence: d.Confidence.String(),
			Suppressed: d.Suppressed(),
			Because: because{
				Diagnosable: d.Because.Diagnosable,
				Transient:   d.Because.Transient,
				Root:        d.Because.Root,
				Childless:   d.Because.Childless,
			},
		},
		Signatures: make([]signature, 0, len(d.Signatures)),
		Records:    make([]record, 0, len(f.Events)),
	}

	for _, s := range d.Signatures {
		out.Signatures = append(out.Signatures, signature{Text: s.Text, Count: s.Count, Specific: s.Specific})
	}

	if !c.IsRoot(i) {
		e := c.Edges[i]
		out.Edge = &edge{
			Parent:   e.Parent,
			Kind:     e.Kind.String(),
			Rule:     e.Rule,
			Evidence: e.Evidence,
			Delay:    f.FirstSeen.Sub(c.Findings[e.Parent].FirstSeen).Round(time.Millisecond).String(),
		}
	}

	for _, ev := range f.Events {
		out.Records = append(out.Records, recordOf(ev))
	}

	return out
}

func recordOf(e event.Event) record {
	return record{
		Timestamp: stamp(e.Timestamp),
		Severity:  e.Severity.String(),
		Reason:    e.Reason,
		Body:      e.Body,
		Object:    e.Object.Name,
		Node:      e.Node,
		UID:       e.EventUID,
		Count:     e.Count,
	}
}

// stamp formats a timestamp, or empty for the zero value.
//
// RFC3339 with milliseconds: the captures distinguish evictions that are
// fractions of a second apart, and a format that rounded them would make two
// findings look simultaneous.
func stamp(t time.Time) string {
	if t.IsZero() {
		return ""
	}

	return t.UTC().Format("2006-01-02T15:04:05.000Z")
}

// nonNil returns an empty slice rather than nil, so absence encodes as [] and
// not as null. A consumer iterating the field should not have to special-case
// "this finding is not about pods".
func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}

	return s
}
