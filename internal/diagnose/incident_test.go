package diagnose_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/troglodytto/daiquiri/internal/classify"
	"github.com/troglodytto/daiquiri/internal/diagnose"
	"github.com/troglodytto/daiquiri/internal/event"
	"github.com/troglodytto/daiquiri/internal/group"
)

// at offsets a finding from the incident's start, so a tree's shape in time is
// readable at the test site.
func at(f group.Finding, offset time.Duration) group.Finding {
	span := f.LastSeen.Sub(f.FirstSeen)
	f.FirstSeen = start.Add(offset)
	f.LastSeen = f.FirstSeen.Add(span)

	return f
}

func marker(workload string, offset time.Duration) group.Finding {
	return group.Finding{
		Kind: "Deployment", Workload: workload, Namespace: "production",
		Reason: "ScalingReplicaSet", Rule: "deploy/scaled",
		Category: classify.CategoryDeployMarker, Severity: event.SeverityInfo,
		Recognised: true, Count: 1,
		FirstSeen: start.Add(offset), LastSeen: start.Add(offset),
	}
}

func critical(f group.Finding) group.Finding {
	f.Severity = event.SeverityCritical

	return f
}

// TestMechanismIsShallowestNotDeepest is the bug this type exists to prevent.
func TestMechanismIsShallowestNotDeepest(t *testing.T) {
	findings := []group.Finding{
		marker("payment-service", 0),
		critical(at(issue("payment-service", "failed/image-pull", 24, 3, 10*time.Minute), 4*time.Second)),
		critical(at(issue("payment-service", "backoff/image-pull-retry", 18, 3, 10*time.Minute), 15*time.Second)),
	}

	c := diagnose.Build(forest(findings, root, 0, 1), start.Add(time.Hour))
	in := c.Incidents()

	require.Len(t, in, 1)
	assert.Equal(t, 0, in[0].Root, "the rollout set it off")
	assert.Equal(t, 1, in[0].Mechanism, "Failed is what actually broke")
	assert.Equal(t, 2, in[0].Paged, "BackOff is the symptom that would page")
}

// TestSeverityIsTheMaximumOverTheTree covers the other half of the same bug:
// 03's root is a rollout at INFO and the outage beneath it is CRITICAL, so
// taking the root's severity labelled the whole incident INFO and sorted it
// below a readiness blip.
func TestSeverityIsTheMaximumOverTheTree(t *testing.T) {
	findings := []group.Finding{
		marker("payment-service", 0),
		critical(at(issue("payment-service", "failed/image-pull", 24, 3, 10*time.Minute), 4*time.Second)),
	}

	c := diagnose.Build(forest(findings, root, 0), start.Add(time.Hour))
	in := c.Incidents()

	require.Len(t, in, 1)
	assert.Equal(t, event.SeverityCritical, in[0].Severity)
	assert.Equal(t, event.SeverityInfo, c.Findings[in[0].Root].Severity, "the root really is INFO")
}

// TestPagedIsUnsetWhenSymptomsTie covers 05-test-b: six equally-bad evictions
// and no way to know which one raised the page. Naming the first would be a
// fabrication.
func TestPagedIsUnsetWhenSymptomsTie(t *testing.T) {
	condition := group.Finding{
		Kind: event.KindNode, Workload: "node-4", Namespace: "default",
		Reason: "NodeHasDiskPressure", Rule: "node/disk-pressure",
		Category: classify.CategoryIssue, Severity: event.SeverityCritical, Recognised: true,
		Count: 1, FirstSeen: start, LastSeen: start, Nodes: []string{"node-4"},
	}
	findings := []group.Finding{
		condition,
		at(issue("auth-service", "evicted/disk-pressure", 1, 1, 0), 15*time.Second),
		at(issue("batch-reporter", "evicted/disk-pressure", 1, 1, 0), 25*time.Second),
	}

	c := diagnose.Build(forest(findings, root, 0, 0), start.Add(time.Hour))
	in := c.Incidents()

	require.Len(t, in, 1)
	assert.Equal(t, -1, in[0].Paged, "two equal symptoms: the root carries the incident")
}

// TestBlastRadiusCountsWorkloadsNotObjects covers the miscount that made 05
// read "7 workloads across 4 namespaces": node-4 is not a workload, and nothing
// at all happened in the default namespace.
func TestBlastRadiusCountsWorkloadsNotObjects(t *testing.T) {
	condition := group.Finding{
		Kind: event.KindNode, Workload: "node-4", Namespace: "default",
		Reason: "NodeHasDiskPressure", Rule: "node/disk-pressure",
		Category: classify.CategoryIssue, Severity: event.SeverityCritical, Recognised: true,
		Count: 1, FirstSeen: start, LastSeen: start, Nodes: []string{"node-4"},
	}
	findings := []group.Finding{
		condition,
		at(issue("auth-service", "evicted/disk-pressure", 1, 1, 0), 15*time.Second),
		at(issue("batch-reporter", "evicted/disk-pressure", 2, 2, 0), 25*time.Second),
	}
	findings[2].Namespace = "data"

	c := diagnose.Build(forest(findings, root, 0, 0), start.Add(time.Hour))
	in := c.Incidents()[0]

	assert.Equal(t, 2, in.Workloads, "the node is not a workload")
	assert.Equal(t, 2, in.Namespaces, "default is not an affected namespace")
	assert.Equal(t, 3, in.Pods)
	assert.Equal(t, 4, in.Events, "the condition plus three evictions")
}

// TestARolloutIsNotCountedAsDamage keeps the impact line honest: a deploy marker
// is an anchor, and its single record is not an event the incident caused.
func TestARolloutIsNotCountedAsDamage(t *testing.T) {
	findings := []group.Finding{
		marker("checkout-service", 0),
		at(issue("checkout-service", "unhealthy/readiness", 222, 5, 8*time.Minute), 7*time.Second),
	}

	in := diagnose.Build(forest(findings, root, 0), start.Add(time.Hour)).Incidents()[0]

	assert.Equal(t, 222, in.Events)
}

// TestStillFailingComparesAgainstTheCaptureEnd is the distinction a flat list of
// counts never draws: whether this is happening now or already over.
func TestStillFailingComparesAgainstTheCaptureEnd(t *testing.T) {
	f := issue("checkout-service", "unhealthy/readiness", 222, 5, 8*time.Minute)

	t.Run("ends with the capture", func(t *testing.T) {
		in := diagnose.Build(forest([]group.Finding{f}, root), f.LastSeen.Add(3*time.Second)).Incidents()[0]
		assert.True(t, in.StillFailing)
	})

	t.Run("stopped well before it", func(t *testing.T) {
		in := diagnose.Build(forest([]group.Finding{f}, root), f.LastSeen.Add(10*time.Minute)).Incidents()[0]
		assert.False(t, in.StillFailing)
	})

	t.Run("unknown capture end makes no claim", func(t *testing.T) {
		in := diagnose.Build(forest([]group.Finding{f}, root), time.Time{}).Incidents()[0]
		assert.False(t, in.StillFailing)
	})
}

// TestIncidentsRankBySeverityThenBlastRadius is the brief's prioritised
// summary, applied to incidents so a cause is never sorted away from its
// effects.
func TestIncidentsRankBySeverityThenBlastRadius(t *testing.T) {
	small := critical(at(issue("a-service", "oom-killed", 30, 1, 5*time.Minute), 0))
	wide := critical(at(issue("b-service", "oom-killed", 30, 6, 5*time.Minute), time.Minute))
	mild := at(issue("c-service", "unhealthy/readiness", 40, 9, 5*time.Minute), 2*time.Minute)

	c := diagnose.Build(forest([]group.Finding{small, wide, mild}, root, root, root), start.Add(time.Hour))

	var order []string
	for _, in := range c.Incidents() {
		order = append(order, c.Findings[in.Root].Workload)
	}

	assert.Equal(t, []string{"b-service", "a-service", "c-service"}, order,
		"critical before warning; within critical, the wider blast radius first")
}

// TestSuppressedOnlyTreesAreNotIncidents is why 01-healthy reports nothing.
func TestSuppressedOnlyTreesAreNotIncidents(t *testing.T) {
	decoy := issue("data-pipeline", "evicted/memory-pressure", 1, 1, 0)

	c := diagnose.Build(forest([]group.Finding{decoy}, root), start.Add(time.Hour))

	assert.Empty(t, c.Incidents())
}

// TestRemediationComesFromTheMechanism covers D-53: the fix for a rollout is
// nothing, and the fix for the image it could not pull is the whole point.
func TestRemediationComesFromTheMechanism(t *testing.T) {
	broken := critical(at(issue("payment-service", "failed/image-pull", 24, 3, 10*time.Minute), 4*time.Second))
	broken.Fix = "confirm the tag exists, then roll back: kubectl rollout undo deploy/{workload} -n {namespace}"
	broken.Namespace = "production"

	c := diagnose.Build(forest([]group.Finding{marker("payment-service", 0), broken}, root, 0), start.Add(time.Hour))
	in := c.Incidents()[0]

	assert.Equal(t,
		"confirm the tag exists, then roll back: kubectl rollout undo deploy/payment-service -n production",
		c.Remediation(in),
		"placeholders resolve, so the command can be pasted rather than hand-edited")
}

// TestRemediationSubstitutesNodeAndPod covers the other two placeholders, and
// the fallback where a finding carries neither.
func TestRemediationSubstitutesNodeAndPod(t *testing.T) {
	// Deliberately large: a one-event, one-pod finding is transient background
	// and never becomes an incident, so it would have no remediation to render.
	f := issue("batch-reporter", "node/disk-pressure", 40, 3, 5*time.Minute)
	f.Nodes = []string{"node-4"}
	f.Fix = "kubectl describe node {node}; kubectl logs {pod} --previous"

	c := diagnose.Build(forest([]group.Finding{f}, root), start.Add(time.Hour))
	got := c.Remediation(c.Incidents()[0])

	assert.Contains(t, got, "node-4")
	assert.Contains(t, got, "batch-reporter-abcdef123-0000a")

	t.Run("no node or pod leaves a visible placeholder", func(t *testing.T) {
		bare := issue("svc", "node/disk-pressure", 40, 0, 5*time.Minute)
		bare.Fix = "kubectl cordon {node}"

		c := diagnose.Build(forest([]group.Finding{bare}, root), start.Add(time.Hour))
		assert.Equal(t, "kubectl cordon <node>", c.Remediation(c.Incidents()[0]))
	})
}

// TestNoRemediationRendersNothing; silence beats a vague gesture at
// "investigate further", and an unrecognised reason is exactly the case where we
// have nothing to say.
func TestNoRemediationRendersNothing(t *testing.T) {
	probe := issue("data-pipeline", "unrecognised-warning", 1, 1, 0)
	probe.Recognised = false // never suppressed, so it does reach the report
	probe.Fix = ""

	c := diagnose.Build(forest([]group.Finding{probe}, root), start.Add(time.Hour))

	assert.Empty(t, c.Remediation(c.Incidents()[0]))
}

// TestShareExplanationNeedsAllThreeToMatch covers D-51's collapse condition. It
// is asked of the causal rule rather than the evidence text, because evidence is
// prose: 05's six evictions all fired one rule and every string differs in the
// elapsed time it quotes.
func TestShareExplanationNeedsAllThreeToMatch(t *testing.T) {
	mk := func(workload string, offset time.Duration) group.Finding {
		return withBodies(at(issue(workload, "evicted/disk-pressure", 0, 1, 0), offset),
			"The node had condition: [DiskPressure].")
	}

	condition := group.Finding{
		Kind: event.KindNode, Workload: "node-4", Reason: "NodeHasDiskPressure",
		Rule: "node/disk-pressure", Category: classify.CategoryIssue,
		Severity: event.SeverityCritical, Recognised: true,
		Count: 1, FirstSeen: start, LastSeen: start,
	}

	base := []group.Finding{condition, mk("auth-service", 15*time.Second), mk("batch-reporter", 25*time.Second)}

	c := diagnose.Build(forest(base, root, 0, 0), start.Add(time.Hour))
	require.True(t, c.ShareExplanation(c.Children(0)))

	t.Run("a different reason breaks it", func(t *testing.T) {
		f := append([]group.Finding(nil), base...)
		f[2].Reason = "FailedMount"

		c := diagnose.Build(forest(f, root, 0, 0), start.Add(time.Hour))
		assert.False(t, c.ShareExplanation(c.Children(0)))
	})

	t.Run("a different leading signature breaks it", func(t *testing.T) {
		f := append([]group.Finding(nil), base...)
		f[2] = withBodies(f[2], "The node was low on resource: memory.")

		c := diagnose.Build(forest(f, root, 0, 0), start.Add(time.Hour))
		assert.False(t, c.ShareExplanation(c.Children(0)),
			"a child telling a different story must never be folded into its siblings")
	})

	t.Run("a lone child is never collapsed", func(t *testing.T) {
		c := diagnose.Build(forest(base[:2], root, 0), start.Add(time.Hour))
		assert.False(t, c.ShareExplanation(c.Children(0)))
	})
}
