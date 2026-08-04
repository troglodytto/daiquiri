package diagnose

import (
	"sort"
	"strings"
	"time"

	"github.com/troglodytto/daiquiri/internal/classify"
	"github.com/troglodytto/daiquiri/internal/event"
)

// stillFailingWithin is how close to the end of the capture a finding's last
// occurrence must be for the incident to count as ongoing.
//
// Derived. Across the six captures every real problem's last event lands 0-100s
// before the capture ends, and every background finding's lands 600-1730s
// before. Any value in that gap behaves identically; two minutes sits inside it
// and reads as a round number rather than a tuned one.
//
// The distinction it draws -- "this is happening now" against "this happened" --
// is the first thing an on-call reader needs and the last thing a flat list of
// counts tells them.
const stillFailingWithin = 2 * time.Minute

// noNode marks an incident with no single symptom to point at.
const noNode = -1

// Incident is one causal tree, summarised for the reader.
//
// Built here rather than in report because every field is a derived fact, and
// report is forbidden to derive. It is also the unit of prioritisation: ranking
// incidents rather than findings is what keeps a cause and its effects from
// being split apart by a sort.
type Incident struct {
	// Root is the finding nothing explains -- what set this off.
	Root int

	// Mechanism is what actually broke: the shallowest failure of maximum
	// severity, skipping deploy markers.
	//
	// Shallowest, not deepest. In 03 the deepest is BackOff, whose cause reads
	// "repeated image-pull retry" -- a consequence. The shallowest failure is
	// Failed, which names the image tag that does not exist. Depth is the right
	// axis for "what paged you" and the wrong one for "what went wrong".
	Mechanism int

	// Paged is the symptom that would have raised the alert: the deepest leaf of
	// maximum severity.
	//
	// noNode when several leaves tie. 05-test-b has six equally-bad evictions
	// and no way to know which one paged; naming the first would be a
	// fabrication, so the incident is carried by its root instead.
	Paged int

	// Severity is the maximum over the whole tree, never the root's. 03's root
	// is a rollout at INFO and the outage beneath it is CRITICAL.
	Severity event.Severity

	// Confidence is the root's, which every member inherits.
	Confidence Confidence

	// Members are the root and its descendants, ascending by FirstSeen.
	Members []int

	// Blast radius. Pods, Workloads and Namespaces count Pod-kind findings only:
	// a node condition is not a workload, and counting it made 05 read "7
	// workloads across 4 namespaces" when six workloads in three namespaces were
	// evicted and nothing at all happened in the fourth.
	Events     int
	Pods       int
	Workloads  int
	Namespaces int

	// First and Last bound the whole incident.
	First time.Time
	Last  time.Time

	// StillFailing reports whether the incident was ongoing when the capture
	// ended.
	StillFailing bool
}

// Span is how long the incident lasted.
func (in Incident) Span() time.Duration { return in.Last.Sub(in.First) }

// Incidents groups the reported findings into causal trees, most urgent first.
//
// Order is severity, then blast radius, then time -- the brief's "prioritised
// summary", applied to incidents rather than to findings.
//
// A tree is included when any of its members survived suppression. A tree whose
// members were all held back as background never becomes an incident, which is
// how 01-healthy produces none.
func (c Chart) Incidents() []Incident {
	seen := make(map[int]bool, len(c.Findings))

	var roots []int
	for _, i := range c.Reported() {
		if r := c.RootOf(i); !seen[r] {
			seen[r] = true
			roots = append(roots, r)
		}
	}

	out := make([]Incident, 0, len(roots))
	for _, r := range roots {
		out = append(out, c.incidentAt(r))
	}

	sort.Slice(out, func(i, j int) bool {
		switch {
		case out[i].Severity != out[j].Severity:
			return out[i].Severity > out[j].Severity
		case out[i].Pods != out[j].Pods:
			return out[i].Pods > out[j].Pods
		case out[i].Events != out[j].Events:
			return out[i].Events > out[j].Events
		default:
			return out[i].First.Before(out[j].First)
		}
	})

	return out
}

// incidentAt summarises the tree rooted at r.
func (c Chart) incidentAt(r int) Incident {
	in := Incident{
		Root:       r,
		Mechanism:  r,
		Paged:      noNode,
		Confidence: c.Diagnoses[r].Confidence,
		First:      c.Findings[r].FirstSeen,
		Last:       c.Findings[r].LastSeen,
	}

	pods := map[string]bool{}
	workloads := map[string]bool{}
	namespaces := map[string]bool{}

	// mechanism tracking: highest severity, then shallowest.
	mechSeverity, mechDepth := event.Severity(-1), 1<<30

	// paged tracking: highest severity leaf, then deepest, and how many tie.
	pagedScore, pagedTies := -1, 0

	var walk func(i, depth int)
	walk = func(i, depth int) {
		f := c.Findings[i]
		in.Members = append(in.Members, i)

		if f.Severity > in.Severity {
			in.Severity = f.Severity
		}
		if f.FirstSeen.Before(in.First) {
			in.First = f.FirstSeen
		}
		if f.LastSeen.After(in.Last) {
			in.Last = f.LastSeen
		}

		// A rollout is an anchor, not damage: its single record must not be
		// counted as an event the incident caused.
		if f.Category != classify.CategoryDeployMarker {
			in.Events += f.Count

			if f.Severity > mechSeverity || (f.Severity == mechSeverity && depth < mechDepth) {
				in.Mechanism, mechSeverity, mechDepth = i, f.Severity, depth
			}
		}

		if f.Kind == event.KindPod {
			for _, p := range f.Pods {
				pods[p] = true
			}
			workloads[f.Workload] = true
			namespaces[f.Namespace] = true
		}

		kids := c.Children(i)
		if len(kids) == 0 {
			switch score := int(f.Severity)*1000 + depth; {
			case score > pagedScore:
				pagedScore, in.Paged, pagedTies = score, i, 1
			case score == pagedScore:
				pagedTies++
			}
		}

		for _, k := range kids {
			walk(k, depth+1)
		}
	}
	walk(r, 0)

	if pagedTies > 1 {
		in.Paged = noNode
	}

	in.Pods = len(pods)
	in.Workloads = len(workloads)
	in.Namespaces = len(namespaces)
	in.StillFailing = !c.CaptureEnd.IsZero() && c.CaptureEnd.Sub(in.Last) <= stillFailingWithin

	sort.Slice(in.Members, func(i, j int) bool {
		return c.Findings[in.Members[i]].FirstSeen.Before(c.Findings[in.Members[j]].FirstSeen)
	})

	return in
}

// Remediation returns the incident's suggested next move, with the taxonomy's
// placeholders resolved against the finding that earned it.
//
// Taken from the mechanism rather than the root: the fix for a rollout is
// nothing, and the fix for the image it could not pull is the whole point.
// Empty when the taxonomy has nothing useful to say, which renders nothing --
// silence beats a vague gesture at "investigate further".
func (c Chart) Remediation(in Incident) string {
	f := c.Findings[in.Mechanism]
	if f.Fix == "" {
		return ""
	}

	pod, node := "<pod>", "<node>"
	if len(f.Pods) > 0 {
		pod = f.Pods[0]
	}
	if len(f.Nodes) > 0 {
		node = f.Nodes[0]
	}

	return strings.NewReplacer(
		"{workload}", f.Workload,
		"{namespace}", f.Namespace,
		"{node}", node,
		"{pod}", pod,
	).Replace(f.Fix)
}
