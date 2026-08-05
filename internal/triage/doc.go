// Package triage wires the stages together and owns the Result.
//
// May import: otel, event, classify, group, link, diagnose.
//
// Prohibitions:
//
//   - No domain logic. Every judgement belongs to a stage. If a decision is
//     being made here, it is in the wrong package.
//   - No rendering. Result is data; report decides how it looks.
//   - No hidden drops. Records skipped by the decoder, filtered as noise, or
//     held back as background are all counted and carried on Result.
package triage
