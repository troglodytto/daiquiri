// Package group coalesces independent event records into findings.
//
// May import: event, classify.
//
// One finding per (Kind, Workload, Namespace, Reason, Rule). Keyed on the
// workload rather than the pod instance, so 04-test-a's 225 Unhealthy records
// across five replicas are one problem and not five (D-11). Keyed on the fired
// taxonomy rule as well, so one reason carrying two failure modes stays two
// findings (D-28).
//
// Prohibitions:
//
//   - This package does not interpret shape over time, rank findings, or decide
//     what caused what. Those are diagnose and link.
//   - No windowing. One finding spans the whole capture and every occurrence
//     timestamp is retained, because count plus first plus last cannot tell a
//     15-second burst from a 27-minute crash loop (D-16).
//   - No merging across a dimension that cannot be shown irrelevant. Merging
//     destroys information irreversibly; linking preserves it (D-15).
//   - Node is deliberately absent from the key. It fixes one capture and
//     fragments four (D-12).
//
// Output is sorted on a total order, because Go randomises map iteration and
// several evictions in 05-test-b share a second (D-13).
package group
