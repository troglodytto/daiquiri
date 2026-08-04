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
const schemaVersion = 1

// Document is the whole run, in a form you can argue with. Every verdict sits
// next to the inputs that produced it, so each finding carries:
//
//   - the identity it was coalesced on
//   - every member record, verbatim, so counts are recomputable
//   - the causal edge, with the rule that fired and the evidence for it
//   - the diagnosis, with each suppression clause published separately
//
// Lifecycle noise is counted but not reproduced: ~19,800 of 20,000 records,
// already in the input file byte for byte. Everything the tool concluded is
// here. Everything it read is in the file it read. See D-55.
type Document struct {
	Schema  int     `json:"schema"`
	Tool    toolDoc `json:"tool"`
	Capture capture `json:"capture"`

	// Incidents are causal trees, ranked as the report ranks them. They index
	// into Findings, so no finding is duplicated and a
	// consumer can walk either structure.
	Incidents []incident `json:"incidents"`

	// Findings is every reportable group, suppressed ones included, in the same
	// order and at the same indices the incidents refer to.
	Findings []finding `json:"findings"`
}

// toolDoc records what produced the document and under which numbers.
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

	// Cadence is null where too few intervals existed to measure a rhythm.
	Cadence *cadence `json:"cadence"`

	// Edge is null for a root.
	Edge *edge `json:"edge"`

	// Records is every member, verbatim, so Shape is checkable.
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
	// unresolved here; the resolved form is on the incident.
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
	// Pattern is empty where the tool declined to categorise; a deploy marker
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
type because struct {
	// Diagnosable: is this a recognised failure at all? False for a rollout, and
	// for any reason the taxonomy could not read; and we never dismiss what we
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

	// Specific marks a signature naming something concrete; an image, an
	// amount, a status code; as opposed to one restating the reason. Ranking
	// puts specific first, then frequency.
	Specific bool `json:"specific"`

	// Means is what this body proves, where the finding's own meaning is too
	// coarse to say. Omitted when there is nothing to add.
	Means string `json:"means,omitempty"`
}

// cadence is the finding's rhythm.
//
// Every measured value is published, not just the classification: trend is a
// threshold applied to early and late, and a reader who disagrees with the
// threshold can apply their own. 02-memory-leak reads "steady" at a ratio of
// 0.79, and both numbers are here so that call can be checked.
type cadence struct {
	Samples int `json:"intervals_measured"`
	Objects int `json:"objects"`

	Median   string `json:"median"`
	Shortest string `json:"shortest"`
	Longest  string `json:"longest"`

	Early string `json:"early_mean"`
	Late  string `json:"late_mean"`

	Trend string `json:"trend"`
}

type edge struct {
	Parent int    `json:"parent"`
	Kind   string `json:"kind"`
	Rule   string `json:"rule"`

	// Evidence is written to be checked against the capture.
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
		out.Signatures = append(out.Signatures, signature{
			Text: s.Text, Count: s.Count, Specific: s.Specific, Means: s.Means,
		})
	}

	if cd := d.Cadence; cd.Known() {
		out.Cadence = &cadence{
			Samples:  cd.Samples,
			Objects:  cd.Objects,
			Median:   cd.Median.Round(time.Millisecond).String(),
			Shortest: cd.Shortest.Round(time.Millisecond).String(),
			Longest:  cd.Longest.Round(time.Millisecond).String(),
			Early:    cd.Early.Round(time.Millisecond).String(),
			Late:     cd.Late.Round(time.Millisecond).String(),
			Trend:    cd.Trend.String(),
		}
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
func stamp(t time.Time) string {
	if t.IsZero() {
		return ""
	}

	return t.UTC().Format("2006-01-02T15:04:05.000Z")
}

// nonNil returns an empty slice, so absence encodes as [] and
// not as null. A consumer iterating the field should not have to special-case
// "this finding is not about pods".
func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}

	return s
}
