// Package diagnose turns a Forest Data Structure into verdicts a reader can act
// on.
//
// May import: event, classify, group, link.
//
// Produces, per finding: a pattern from the brief's vocabulary, a confidence
// from what the root is, ranked signatures over the record bodies, a cadence,
// and whether it is background. Produces, per tree: an Incident carrying
// severity, blast radius and the three nodes a reader needs (root, mechanism,
// paged).
//
// Prohibitions:
//
//   - No labelling or suppressing an unrecognised reason. Both are claims to
//     understand it, and neither can be made about a reason outside the
//     taxonomy (D-46).
//   - No stored verdict beside its reasons. Suppressed is derived from the four
//     clauses, so a chart cannot publish an outcome its own reasons contradict
//     (D-56).
//   - Nothing is deleted. Suppressed findings stay in the chart and are counted
//     in the header (D-45).
//   - No wire-format knowledge. Config and Suppression carry no struct tags; how
//     a verdict is spelled is report's business (D-55).
//   - No confidence score. Two edge tiers, each defensible from the rule that
//     fired (D-37).
package diagnose
