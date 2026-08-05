package otel

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/troglodytto/daiquiri/internal/event"
)

// ErrMalformedRecord indicates a line that could not be interpreted as a
// Kubernetes event, either because it is not valid JSON or because a field the
// pipeline requires is unusable. Callers inspect it with errors.Is.
var ErrMalformedRecord = errors.New("malformed record")

// defaultCount is the occurrence count assumed when a record does not supply a
// usable one. A record always represents at least one occurrence: dropping it
// would undercount, and trusting a zero would erase it entirely.
const defaultCount = 1

// record mirrors the subset of the OTel log-record wire format that the
// pipeline reads.
//
// Fields the tool never consults; observed_timestamp, severity_number,
// k8s.event.action, k8s.event.start_time, k8s.event.name, k8s.object.fieldpath,
// k8s.object.api_version, k8s.object.resource_version, service.name; are
// deliberately absent. Decoding the full record measured 137ms against 115ms
// for this subset, a 16% cost for no consumer.
//
// Note where things live: object identity sits under resource, while event
// metadata and the namespace sit under attributes. That is what the real
// k8seventsreceiver emits, and it is easy to get backwards.
type record struct {
	Timestamp    string `json:"timestamp"`
	SeverityText string `json:"severity_text"`
	Body         string `json:"body"`
	Attributes   struct {
		Reason    string `json:"k8s.event.reason"`
		Namespace string `json:"k8s.namespace.name"`
		Count     int    `json:"k8s.event.count"`
		UID       string `json:"k8s.event.uid"`
	} `json:"attributes"`
	Resource struct {
		Cluster string `json:"k8s.cluster.name"`
		Node    string `json:"k8s.node.name"`
		Kind    string `json:"k8s.object.kind"`
		Name    string `json:"k8s.object.name"`
		UID     string `json:"k8s.object.uid"`
	} `json:"resource"`
}

// unmarshal decodes one JSONL line into r, clearing r first.
//
// The clear is load-bearing, not hygiene. encoding/json leaves fields absent
// from the input untouched, and this struct is reused across every line of the
// capture to keep allocations flat. Since k8s.node.name is absent on scheduler
// and controller events, skipping the clear would silently attribute those
// events to whichever node last emitted a kubelet event; manufacturing false
// node correlations rather than merely losing data.
func (r *record) unmarshal(line []byte) error {
	*r = record{}

	if err := json.Unmarshal(line, r); err != nil {
		return fmt.Errorf("%w: %w", ErrMalformedRecord, err)
	}

	return nil
}

// toEvent maps the wire record onto the domain model.
func (r *record) toEvent() (event.Event, error) {
	ts, err := time.Parse(time.RFC3339Nano, r.Timestamp)
	if err != nil {
		return event.Event{}, fmt.Errorf("%w: timestamp %q: %w", ErrMalformedRecord, r.Timestamp, err)
	}

	count := r.Attributes.Count
	if count < 1 {
		count = defaultCount
	}

	// A Node-kind event names its node in k8s.object.name and carries no
	// k8s.node.name: that field is populated by the kubelet that emitted the
	// event, and a node condition comes from the node controller instead. So the
	// one record shape that identifies a node arrives with an empty Node.
	//
	// Normalising here rather than at each use keeps every downstream "same
	// node" rule a plain string comparison. Without it those rules silently fail
	// to match the very event they exist to find; the anchor for the whole
	// node-pressure diagnosis in 05-test-b.
	node := r.Resource.Node
	if node == "" && r.Resource.Kind == event.KindNode {
		node = r.Resource.Name
	}

	return event.Event{
		Timestamp: ts,
		Reason:    r.Attributes.Reason,
		Body:      r.Body,
		Namespace: r.Attributes.Namespace,
		Cluster:   r.Resource.Cluster,
		Node:      node,
		Object: event.Object{
			Kind: r.Resource.Kind,
			Name: r.Resource.Name,
			UID:  r.Resource.UID,
		},
		Severity: severityOf(r.SeverityText),
		Count:    count,
		EventUID: r.Attributes.UID,
	}, nil
}

// severityOf maps a record's severity_text onto the domain severity.
func severityOf(text string) event.Severity {
	if text == "Warning" {
		return event.SeverityWarning
	}

	return event.SeverityInfo
}
