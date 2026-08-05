// Package classify decides what kind of thing an event is.
//
// May import: event.
//
// Every record lands in exactly one category: issue, deploy marker,
// unclassified, or noise. The taxonomy is a table of reasons to ordered rules,
// first match wins, so adding a Kubernetes reason means adding a row and never
// editing a switch.
//
// Prohibitions:
//
//   - Nothing is deleted. Noise is categorised, never dropped, because lifecycle
//     events are the evidence a trail is built from (D-05, D-06).
//   - No coalescing. One record in, one classification out.
//   - No shape. Whether three occurrences matter is a question about a group
//     over time, and this package sees one record at a time.
//   - No confident guessing. A reason outside the taxonomy is marked
//     Recognised: false and carries no rule, no meaning and no fix. Downstream
//     stages are then forbidden from labelling or suppressing it (D-46).
//
// Two traps the taxonomy exists to handle: ImagePullBackOff is not an event
// reason and appears only in the body of a Failed event, and BackOff means
// crash-loop at Warning severity and image-pull retry at Normal.
package classify
