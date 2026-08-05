// Package otel decodes a JSONL capture of OTel log records into event.Event.
//
// May import: event.
//
// Prohibitions:
//
//   - No classification, no grouping, no interpretation. A record's meaning is
//     decided downstream.
//   - No fatal errors on a single bad line. Decoding is per-record resilient: a
//     line that will not parse is counted and skipped, and the count is
//     published so a truncated capture cannot pass as a clean one (D-45).
//   - No buffering the whole file. Records stream.
//
// One normalisation happens at decode rather than later: a Node-kind record
// carries its name in k8s.object.name and leaves k8s.node.name empty, so the one
// record that names a node would fail every same-node rule downstream (D-08).
package otel
