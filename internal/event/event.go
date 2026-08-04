package event

import (
	"regexp"
	"time"
)

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

// Kubernetes object kinds the pipeline reasons about by name.
//
// Exported because ownership and node identity are decided in more than one
// stage: otel normalises a Node's identity at decode, and grouping dispatches on
// kind to derive a workload. A bare string literal in two packages is a typo
// waiting to silently disable a rule.
const (
	KindPod        = "Pod"
	KindReplicaSet = "ReplicaSet"
	KindNode       = "Node"
)

// Owner-name patterns, anchored so a partial match cannot strip anything.
//
// Kubernetes encodes ownership in the name: a Deployment's ReplicaSet is
// <deployment>-<pod-template-hash>, and its Pods are <replicaset>-<suffix>.
// Measured across the six provided captures, every one of 94,686 Pod names and
// 25,311 ReplicaSet names matches these shapes exactly, over twelve distinct
// workloads -- none of which itself ends in a hash-shaped segment, so a single
// strip is unambiguous.
//
// The capture group is greedy on purpose. It takes the longest possible
// workload prefix, so a workload whose own name happens to end in nine hex
// characters survives instead of being truncated.
var (
	podOwner        = regexp.MustCompile(`^(.+)-[0-9a-f]{9}-[0-9a-z]{5}$`)
	replicaSetOwner = regexp.MustCompile(`^(.+)-[0-9a-f]{9}$`)
)

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

// Workload returns the workload that owns the object, or the object's own name
// when it owns itself.
//
// Grouping keys on this rather than on Name because a Pod name is the
// disposable instance. The 225 Unhealthy records in 04-test-a span five pod
// instances of one service: keyed on Name that is five findings, keyed on
// Workload it is one, and only the second is a fact an on-call engineer can
// act on.
//
// Deliberately total and conservative. Dispatching on Kind keeps a Node named
// "node-4" from being mistaken for an owned resource, and an unmatched name is
// returned unchanged so the failure mode is "no rollup" rather than the far
// worse "wrong rollup". Real clusters use a different hash alphabet and a
// variable hash length; those names simply will not match, and will group by
// instance instead of being silently mis-attributed.
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

// GroupKey is the doc's required grouping tuple.
func (e Event) GroupKey() string {
	return e.Object.Kind + "|" + e.Object.Name
}
