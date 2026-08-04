package event

import "time"

// Severity ranks how much an event matters to an on-call engineer.
//
// The ordering is meaningful and load-bearing: report sorts findings by
// severity, and an ordered integer makes that a comparison rather than a lookup
// table. SeverityInfo is deliberately the zero value, so that a zero Event is
// the least urgent thing rather than accidentally the most urgent.
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

// Object identifies the Kubernetes resource an event is about.
//
// The three fields travel together because group keys on the object as a unit;
// passing them loose invites transposing two strings at a call site.
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
	// In a watch-based pipeline -- which is what all six provided fixtures are --
	// this is always 1 and one record means one occurrence. In a pipeline that
	// re-emits a log each time the API server increments its count, one logical
	// event appears repeatedly with a rising count. The brief requires grouping
	// to be correct in either case, so group sums max(Count) per distinct
	// EventUID rather than summing Count or counting records.
	Count int

	// EventUID identifies the underlying API-server Event object. It exists so
	// that repeated records describing the same logical event can be collapsed
	// rather than double-counted. See Count.
	EventUID string
}
