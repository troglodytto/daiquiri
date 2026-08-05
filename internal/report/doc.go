// Package report renders a triage result for a human reading it under time
// pressure.
//
// May import: event, classify, group, link, diagnose, triage.
//
// Three views over the same result: a prioritised table, the causal incident
// tree, and a JSON audit document. The wire format lives here, because how a
// verdict is spelled on a wire is a rendering concern (D-55).
//
// Prohibitions:
//
//   - No deriving. This package renders what it is handed. Blast radius,
//     ordering, severity and the paged node are computed in diagnose precisely
//     so that this rule can hold (D-54).
//   - Colour is emphasis only. Strip the escape sequences and the styled output
//     is byte-identical to the plain one, which is what makes a redirected file
//     readable. TestStylingIsEmphasisOnly enforces it (D-30).
//   - No fact carried by colour, position or absence alone.
//   - No silent filtering. --trace counts the incidents it left out, and the
//     header states every number that could hide something (D-59).
package report
