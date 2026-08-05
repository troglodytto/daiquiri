// Package event is the domain model: one Kubernetes event, normalised.
//
// It is the leaf of the dependency graph and imports nothing else in this
// repository. Everything downstream speaks in these types.
//
// Prohibitions:
//
//   - No knowledge of the OTel wire format. Field names, nesting and JSON tags
//     belong to otel.
//   - No judgement. Severity is a rank carried on the event, never a decision
//     made here about whether the event matters.
//   - No I/O.
//
// The one piece of Kubernetes knowledge that does live here is Object.Workload,
// which strips ReplicaSet and pod suffixes. Owner is a property of identity, and
// putting the parse in the grouper would give a package whose job is coalescing
// a working knowledge of Kubernetes naming (D-07).
package event
