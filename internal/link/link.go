// Package link works out what explains each finding, and writes down why.
//
// Build is O(R·n²) with R=4 rules. n counts distinct failure modes, not
// records: 20,000 records produce 3 to 10 findings, so the quadratic term is
// over something that doesn't grow with input size. Measured at n=10, 30µs;
// at a synthetic n=1,000, 32ms.
//
// Interval trees and sweep lines were considered and rejected. Every causal edge
// here runs from an instant to an effect that begins after it, so nothing
// overlaps and an overlap test finds none of them. Meanwhile 02 has a readiness
// blip sitting inside a memory leak's interval, on the same node, unrelated.
// See D-38.
package link

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/troglodytto/daiquiri/internal/classify"
	"github.com/troglodytto/daiquiri/internal/event"
	"github.com/troglodytto/daiquiri/internal/group"
)

// window bounds how long after a cause an effect can still be attributed to it.
//
// Every true edge in the corpus lands between +4.4s and +97.9s. The nearest
// false candidate is 600s out. Anything in (98s, 600s) behaves identically;
// 300s sits mid-gap. One constant, not one per rule, which would overfit. See
// D-33.
const window = 300 * time.Second

// Window reports the causal window in force, so output that asserts an edge can
// also state the bound that permitted it.
func Window() time.Duration { return window }

// noParent marks a finding nothing explains.
const noParent = -1

// Kind is how much weight an edge carries.
type Kind uint8

// Edge kinds, ordered weakest last.
const (
	// KindCaused is an edge the data proves: either the child's record text
	// names the parent's cause, or both describe the same object.
	KindCaused Kind = iota
	// KindMayRelate is an edge with a real shared dimension inside the window
	// but nothing in the record text connecting the two. Rendered distinctly so
	// it cannot be mistaken for an established link.
	KindMayRelate
)

// String returns the label used in rendered output.
func (k Kind) String() string {
	switch k {
	case KindCaused:
		return "caused"

	case KindMayRelate:
		return "may relate"

	default:
		return "unknown"
	}
}

// Edge describes what explains one finding.
type Edge struct {
	// Parent indexes into Forest.Findings, or is noParent for a root.
	// Invariant: Parent < the edge's own index.
	Parent int

	// Kind distinguishes a proven edge from a speculative one.
	Kind Kind

	// Rule names the causal rule that fired, so callers asking whether two
	// edges share an explanation compare a decision instead of parsing prose.
	Rule string

	// Evidence is printed verbatim beneath the edge. It names the shared
	// dimension and the elapsed time, and quotes the record text where the
	// record states its own cause, so a reader can check the claim against the
	// capture rather than trust it.
	Evidence string
}

// Forest is the causal structure: findings in time order, one edge each.
type Forest struct {
	// Findings ascend by FirstSeen. Never mutated.
	Findings []group.Finding

	// Edges is parallel to Findings: Edges[i] describes Findings[i].
	Edges []Edge
}

// Build derives the forest for findings, which must ascend by FirstSeen.
func Build(findings []group.Finding) Forest {
	edges := make([]Edge, len(findings))

	for i := range findings {
		edges[i] = Edge{Parent: noParent}

		// Rule priority outer, time proximity inner. Transposed, "first match"
		// would silently become "nearest candidate matching any rule" instead of
		// "strongest rule matching any candidate", and a weak workload-level link
		// could beat the node condition that actually explains the finding.
	rules:
		for _, r := range causalRules {
			for j := i - 1; j >= 0; j-- {
				if findings[i].FirstSeen.Sub(findings[j].FirstSeen) > window {
					continue
				}

				if ev, ok := r.links(findings[j], findings[i]); ok {
					edges[i] = Edge{Parent: j, Kind: r.kind, Rule: r.name, Evidence: ev}
					break rules
				}
			}
		}
	}

	return Forest{Findings: findings, Edges: edges}
}

// Roots returns the indices of findings nothing explains.
func (f Forest) Roots() []int {
	var out []int

	for i := range f.Edges {
		if f.IsRoot(i) {
			out = append(out, i)
		}
	}

	return out
}

// IsRoot reports whether nothing in the capture explains finding i.
func (f Forest) IsRoot(i int) bool {
	return f.Edges[i].Parent == noParent
}

// Children returns the indices of findings directly explained by i.
func (f Forest) Children(i int) []int {
	var out []int

	for j := i + 1; j < len(f.Edges); j++ {
		if f.Edges[j].Parent == i {
			out = append(out, j)
		}
	}

	return out
}

// RootOf walks up from i to the finding nothing explains.
func (f Forest) RootOf(i int) int {
	// Bounded by the Parent < index invariant instead of trusting it. Build
	// can't violate it, but Forest is exported and a hand-built one can, where
	// an unguarded walk spins forever. A malformed edge reads as a root.
	for f.Edges[i].Parent != noParent && f.Edges[i].Parent < i {
		i = f.Edges[i].Parent
	}

	return i
}

// rule is one row of the causal table. Adding a relationship is adding a row.
type rule struct {
	name  string
	kind  Kind
	links func(parent, child group.Finding) (string, bool)
}

var causalRules = []rule{
	// Object identity. The weakest proof of the three despite ranking first:
	// a shared pod plus adjacency is an inference, where the two below are
	// stated outright in the record text.
	//
	// Scoped to pods by construction rather than by a kind check: group leaves
	// Pods empty for anything that is not a pod, and an empty set intersects
	// nothing. That is the correct scope for the claim this rule makes; one
	// running instance experienced both, so the earlier likely caused the later.
	// Two deliberate rollouts of a Deployment are not cause and effect, and two
	// conditions on one node want their own rule with their own wording.
	//
	// It ranks first anyway because it is what produces a three-hop chain. An
	// image-pull BackOff matches both this rule (against the Failed on the same
	// pods) and the rollout rule; letting the rollout win would flatten
	// "rollout caused the pull failure, which caused the retry" into two
	// unrelated-looking siblings.
	{
		name: "same-pod",
		kind: KindCaused,
		links: func(parent, child group.Finding) (string, bool) {
			pod, ok := firstShared(parent.Pods, child.Pods)
			if !ok {
				return "", false
			}

			return fmt.Sprintf("same pod %s, %s after %s",
				pod, since(parent, child), parent.Reason), true
		},
	},

	// A node condition whose name the child's own record repeats. The cluster
	// states the cause: an evicted pod's message reads "The node had condition:
	// [DiskPressure]" and the node reported NodeHasDiskPressure.
	{
		name: "node-condition-named",
		kind: KindCaused,
		links: func(parent, child group.Finding) (string, bool) {
			node, cond, ok := nodeConditionShared(parent, child)
			if !ok || !child.BodyContains("["+cond+"]") {
				return "", false
			}

			return fmt.Sprintf("%s reported %s %s earlier, and this record names it: %q",
				node, cond, since(parent, child), firstBody(child)), true
		},
	},

	// A rollout, matched on the replica set it names rather than on the
	// workload. "Same workload" would link a service that merely happened to be
	// deployed recently; belonging to the replica set the rollout created is the
	// actual causal claim.
	{
		name: "rollout-replicaset",
		kind: KindCaused,
		links: func(parent, child group.Finding) (string, bool) {
			rs, ok := replicaSetOf(parent)
			if !ok || len(child.Pods) == 0 {
				return "", false
			}

			for _, p := range child.Pods {
				if !strings.HasPrefix(p, rs+"-") {
					return "", false
				}
			}

			return fmt.Sprintf("rollout created replica set %s %s earlier; all %d affected pods belong to it",
				rs, since(parent, child), len(child.Pods)), true
		},
	},

	// Last, and the only speculative tier: the finding sits on a node that
	// reported a condition, but its record does not name that condition and it
	// shares no object with anything. Reported as "may relate" so it cannot be
	// read as established.
	{
		name: "node-condition-unnamed",
		kind: KindMayRelate,
		links: func(parent, child group.Finding) (string, bool) {
			node, cond, ok := nodeConditionShared(parent, child)
			if !ok {
				return "", false
			}

			return fmt.Sprintf("on %s, which reported %s %s earlier -- but this record does not name that condition",
				node, cond, since(parent, child)), true
		},
	},
}

// nodeConditionShared reports whether parent is a node condition on a node the
// child was seen on, returning that node and the condition's short name.
func nodeConditionShared(parent, child group.Finding) (node, cond string, ok bool) {
	if parent.Kind != event.KindNode {
		return "", "", false
	}

	node, ok = firstShared(parent.Nodes, child.Nodes)
	if !ok {
		return "", "", false
	}

	return node, conditionName(parent.Reason), true
}

// conditionName reduces a node-condition reason to the token an evicted pod's
// record uses for it: NodeHasDiskPressure is reported as [DiskPressure], and
// NodeNotReady as [NotReady].
func conditionName(reason string) string {
	for _, prefix := range []string{"NodeHas", "Node"} {
		if strings.HasPrefix(reason, prefix) {
			return strings.TrimPrefix(reason, prefix)
		}
	}

	return reason
}

// replicaSetNamePattern matches a <workload>-<pod-template-hash> token, which is
// how a ScalingReplicaSet body names the replica set it scaled.
var replicaSetNamePattern = regexp.MustCompile(`[a-z0-9]+(?:-[a-z0-9]+)*-[0-9a-f]{9}`)

// replicaSetOf extracts the replica set a deploy marker names in its body.
func replicaSetOf(f group.Finding) (string, bool) {
	if f.Category != classify.CategoryDeployMarker {
		return "", false
	}

	for _, e := range f.Events {
		for _, m := range replicaSetNamePattern.FindAllString(e.Body, -1) {
			if strings.HasPrefix(m, f.Workload+"-") {
				return m, true
			}
		}
	}

	return "", false
}

// firstBody returns the finding's earliest record body, trimmed for quoting.
func firstBody(f group.Finding) string {
	if len(f.Events) == 0 {
		return ""
	}

	return strings.TrimSpace(f.Events[0].Body)
}

// firstShared returns the first value present in both slices.
func firstShared(a, b []string) (string, bool) {
	for _, x := range a {
		for _, y := range b {
			if x == y {
				return x, true
			}
		}
	}

	return "", false
}

// since renders the gap between a parent and its child.
func since(parent, child group.Finding) string {
	return child.FirstSeen.Sub(parent.FirstSeen).Round(100 * time.Millisecond).String()
}
