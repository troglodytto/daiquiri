// Package link builds the Forest Data Structure: one parent index per finding.
//
// May import: event, classify, group.
//
// Findings are sorted ascending by first occurrence and Build only ever scans
// backwards, so Edges[i].Parent < i holds by construction. Cycles are therefore
// structurally impossible, the slice is already topologically ordered, and
// RootOf is an integer loop (D-31).
//
// Prohibitions:
//
//   - Time is a veto, never a proposal. No rule may propose an edge from
//     temporal proximity. Every rule requires a shared pod, node or ReplicaSet
//     first, and time may then only reject (D-21).
//   - An edge must be provable from record text or object identity. A link
//     nobody can check against the capture is not evidence (D-35).
//   - At most one parent. Joint causation is a stated limitation, not an
//     oversight (D-19).
//   - No second structure. Findings and Edges are index-coupled and only Build
//     constructs the pair.
package link
