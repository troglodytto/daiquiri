package event

import (
	"regexp"
	"time"
)

// Severity ranks how much an event matters to an on-call engineer.
type Severity int

// Severity levels, ordered least to most urgent. Do not reorder: the zero value
// must remain SeverityInfo.
const (
	SeverityInfo Severity = iota
	SeverityWarning
	SeverityCritical
)

// String returns the uppercase label used in rendered output.
func (s Severity) String() string {
	switch s {
	case SeverityInfo:
		return "INFO"

	case SeverityWarning:
		return "WARNING"

	case SeverityCritical:
		return "CRITICAL"

	default:
		return "UNKNOWN"
	}
}

// Kubernetes object kinds the pipeline reasons about by name.
const (
	KindPod        = "Pod"
	KindReplicaSet = "ReplicaSet"
	KindNode       = "Node"
)

// Owner-name patterns, anchored so a partial match cannot strip anything.
var (
	podOwner        = regexp.MustCompile(`^(.+)-[0-9a-f]{9}-[0-9a-z]{5}$`)
	replicaSetOwner = regexp.MustCompile(`^(.+)-[0-9a-f]{9}$`)
)

// Object identifies the Kubernetes resource an event is about.
type Object struct {
	// Kind is the resource kind: Pod, ReplicaSet, Deployment, Node.
	Kind string

	// Name is the resource name, and is what grouping keys on.
	Name string

	// UID is carried for evidence but is not part of the grouping key: a pod
	// deleted and recreated under the same name is the same logical service for
	// triage purposes, even though its UID changed.
	UID string
}

// Workload returns the workload that owns the object, or the object's own name
// when it owns itself.
//
// Total and conservative. Dispatching on Kind keeps a Node called "node-4" from
// looking like an owned resource, and an unmatched name comes back unchanged, so
// the failure mode is no rollup instead of a wrong one. Clusters using a
// different hash alphabet just group by instance. See D-07.
func (o Object) Workload() string {
	switch o.Kind {
	case KindPod:
		if m := podOwner.FindStringSubmatch(o.Name); m != nil {
			return m[1]
		}

	case KindReplicaSet:
		if m := replicaSetOwner.FindStringSubmatch(o.Name); m != nil {
			return m[1]
		}
	}

	return o.Name
}

// Event is one normalized Kubernetes event occurrence.
//
// Only the fields the pipeline actually reads are carried. Decoding the full
// wire record measured 16% slower with no consumer for the extra fields; any
// field added back must name the stage that reads it.
type Event struct {
	// Timestamp is when the event happened in the cluster.
	Timestamp time.Time

	// Reason is the Kubernetes event reason and part of the grouping key.
	Reason string

	// Body is the human-readable message. Classification is body-sensitive:
	// Kubernetes does not give image-pull failures their own reason, so the
	// body is what distinguishes them from other Failed events.
	Body string

	// Namespace is part of the grouping key.
	Namespace string

	// Cluster names the source cluster, for the report header.
	Cluster string

	// Node is the node whose kubelet emitted the event, and a correlation
	// dimension. It is empty for scheduler and controller events, which are not
	// emitted by a kubelet. Absence is normal and must not be treated as an
	// error.
	Node string

	// Object is the resource the event is about.
	Object Object

	// Severity is the event's own severity_text, mapped. It is the raw signal;
	// classify decides the finding-level severity, which may legitimately
	// differ. A BackOff at Normal severity is an image-pull retry, which is not
	// routine even though the record calls it Normal.
	Severity Severity

	// Count is how many occurrences this single record represents.
	//
	// In a watch-based pipeline; which is what all six provided fixtures are,
	// this is always 1 and one record means one occurrence. In a pipeline that
	// re-emits a log each time the API server increments its count, one logical
	// event appears repeatedly with a rising count. The brief requires grouping
	// to be correct in either case, so group sums max(Count) per distinct
	// EventUID. Summing Count double-counts; counting records under-counts.
	Count int

	// EventUID identifies the underlying API-server Event object. It exists so
	// that repeated records describing the same logical event can be collapsed
	//. See Count.
	EventUID string
}

// GroupKey is the doc's required grouping tuple.
func (e Event) GroupKey() string {
	return e.Object.Kind + "|" + e.Object.Name
}
