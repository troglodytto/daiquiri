// Package group coalesces independent event records into findings.
package group

import (
	"sort"
	"strings"
	"time"

	"github.com/troglodytto/daiquiri/internal/classify"
	"github.com/troglodytto/daiquiri/internal/event"
)

// Classified pairs a decoded event with the verdict classify reached on it.
type Classified struct {
	Event event.Event
	Class classify.Classification
}

// key is what counts as one thing. A struct, so Go compares it field-wise and
// no separator can collide with a workload name.
//
// Node is absent. A memory leak belongs to the workload; which nodes its pods
// land on is incidental. Keying on node splits 02's crash-loop into four. See
// D-12.
type key struct {
	kind      string
	workload  string
	namespace string
	reason    string
	rule      string
}

// Finding is one coalesced group: every record sharing a key, folded into a
// single reportable fact with a count and a time range.
type Finding struct {
	// Identity; the coalescing key.
	Kind      string
	Workload  string
	Namespace string
	Reason    string
	Rule      string

	// Verdict, carried from the classification every member shares.
	Category classify.Category
	Severity event.Severity
	Cause    string

	// Meaning is the taxonomy's plain-language reading of the failure.
	Meaning string

	// Fix is the taxonomy's remediation, with its placeholders still unresolved.
	// Substitution happens at render, against this finding.
	Fix string

	// Recognised is false when the reason is not in the taxonomy and the verdict
	// came from the conservative fallback.
	//
	// Carried this far because it is the difference between a judgement and a
	// guess. The diagnose stage refuses to name a pattern for an unrecognised
	// reason, and refuses to suppress one: calling a finding background noise is
	// a claim to understand it well enough to know it does not matter, and that
	// claim cannot be made about a reason we have never seen.
	Recognised bool

	// Count is occurrences, not records. See occurrences.
	Count int

	// FirstSeen and LastSeen bound the finding. For a point event; a
	// deploy marker, a node condition; they are equal.
	FirstSeen time.Time
	LastSeen  time.Time

	// Pods is the blast radius: the distinct pod instances the finding spans,
	// sorted so output is stable.
	//
	// Empty unless the finding is about pods. A Node or Deployment finding
	// concerns exactly one object, already named by Workload, and collecting
	// that object's name here would put a node or a deployment in a field called
	// Pods; which the report would count as a pod, and which would let the
	// same-pod causal rule fire between two things that are not pods and print
	// evidence reading "same pod node-4".
	Pods []string

	// Nodes are the distinct nodes the finding was observed on, sorted.
	//
	// Empty for scheduler and controller events, which no kubelet emitted;
	// absence is normal and not an error.
	Nodes []string

	// Events are every member record, ascending by timestamp.
	//
	// Retained in full rather than reduced to count and time range, because a
	// 13-second burst of three events and a 27-minute crash-loop are otherwise
	// indistinguishable; and telling them apart is exactly what stops
	// 01-healthy.jsonl reporting false positives.
	Events []event.Event
}

// BodyContains reports whether any of the finding's records contain token.
func (f Finding) BodyContains(token string) bool {
	for _, e := range f.Events {
		if strings.Contains(e.Body, token) {
			return true
		}
	}

	return false
}

// Coalesce folds classified events into findings, ordered for display.
//
// The returned slice is sorted ascending by FirstSeen and then through the
// whole key. The tie-break is load-bearing, not tidiness: Go randomises map
// iteration order, and 05-test-b contains several evictions inside the same
// millisecond. Sorting on time alone would leave those ties resolved by map
// order, so two runs over identical input would differ and golden tests would
// fail intermittently.
func Coalesce(in []Classified) []Finding {
	byKey := make(map[key][]Classified)
	for _, c := range in {
		byKey[keyOf(c)] = append(byKey[keyOf(c)], c)
	}

	findings := make([]Finding, 0, len(byKey))
	for k, members := range byKey {
		findings = append(findings, findingOf(k, members))
	}

	sort.Slice(findings, func(i, j int) bool {
		return less(findings[i], findings[j])
	})

	return findings
}

// keyOf derives the coalescing key for one classified event.
func keyOf(c Classified) key {
	return key{
		kind:      c.Event.Object.Kind,
		workload:  c.Event.Object.Workload(),
		namespace: c.Event.Namespace,
		reason:    c.Event.Reason,
		rule:      c.Class.Rule,
	}
}

// findingOf folds one key's members into a finding.
func findingOf(k key, members []Classified) Finding {
	events := make([]event.Event, 0, len(members))
	for _, m := range members {
		events = append(events, m.Event)
	}

	sort.Slice(events, func(i, j int) bool {
		return events[i].Timestamp.Before(events[j].Timestamp)
	})

	return Finding{
		Kind:      k.kind,
		Workload:  k.workload,
		Namespace: k.namespace,
		Reason:    k.reason,
		Rule:      k.rule,

		// Every member matched the same rule, so they share a verdict.
		Category:   members[0].Class.Category,
		Severity:   members[0].Class.Severity,
		Cause:      members[0].Class.Cause,
		Meaning:    members[0].Class.Meaning,
		Fix:        members[0].Class.Fix,
		Recognised: members[0].Class.Recognised,

		Count:     occurrences(events),
		FirstSeen: events[0].Timestamp,
		LastSeen:  events[len(events)-1].Timestamp,
		Pods:      podsOf(k.kind, events),
		Nodes:     distinct(events, func(e event.Event) string { return e.Node }),
		Events:    events,
	}
}

// occurrences totals how many times the finding actually happened.
func occurrences(events []event.Event) int {
	total := 0
	highest := make(map[string]int, len(events))

	for _, e := range events {
		if e.EventUID == "" {
			total += e.Count
			continue
		}
		if e.Count > highest[e.EventUID] {
			highest[e.EventUID] = e.Count
		}
	}

	for _, c := range highest {
		total += c
	}

	return total
}

// podsOf returns the distinct pod instances among events, or nil when the
// finding is not about pods.
//
// Kind-guarded rather than collecting object names blindly: an event's
// k8s.object.name is a pod only when its kind says so, and a Node or Deployment
// finding names exactly one object which Workload already carries. Returning nil
// is what keeps the same-pod causal rule from firing between two non-pods, since
// an empty set intersects nothing.
func podsOf(kind string, events []event.Event) []string {
	if kind != event.KindPod {
		return nil
	}

	return distinct(events, func(e event.Event) string { return e.Object.Name })
}

// distinct returns the sorted unique non-empty values of f over events.
func distinct(events []event.Event, f func(event.Event) string) []string {
	seen := make(map[string]struct{}, len(events))
	out := make([]string, 0, len(events))

	for _, e := range events {
		v := f(e)
		if v == "" {
			continue
		}
		if _, dup := seen[v]; dup {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}

	sort.Strings(out)

	return out
}

// less orders two findings totally: by start time, then through the whole key.
func less(a, b Finding) bool {
	if !a.FirstSeen.Equal(b.FirstSeen) {
		return a.FirstSeen.Before(b.FirstSeen)
	}

	for _, pair := range [][2]string{
		{a.Kind, b.Kind},
		{a.Workload, b.Workload},
		{a.Namespace, b.Namespace},
		{a.Reason, b.Reason},
		{a.Rule, b.Rule},
	} {
		if pair[0] != pair[1] {
			return pair[0] < pair[1]
		}
	}

	return false
}
