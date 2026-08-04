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
	"github.com/troglodytto/daiquiri/internal/link"
)

var start = time.Date(2024, 6, 1, 10, 0, 0, 0, time.UTC)

// issue builds a Pod-kind issue finding with the given shape.
func issue(workload, rule string, count, pods int, span time.Duration) group.Finding {
	f := group.Finding{
		Kind:       event.KindPod,
		Workload:   workload,
		Namespace:  "production",
		Reason:     "Evicted",
		Rule:       rule,
		Category:   classify.CategoryIssue,
		Severity:   event.SeverityWarning,
		Recognised: true,
		Count:      count,
		FirstSeen:  start,
		LastSeen:   start.Add(span),
	}

	for i := 0; i < pods; i++ {
		f.Pods = append(f.Pods, workload+"-abcdef123-0000"+string(rune('a'+i)))
	}

	return f
}

// forest wires findings into a forest with the given parents, so a diagnosis
// can be tested without depending on link's causal rules.
func forest(findings []group.Finding, parents ...int) link.Forest {
	if len(parents) != len(findings) {
		panic("forest: one parent per finding")
	}

	edges := make([]link.Edge, len(findings))
	for i, p := range parents {
		edges[i] = link.Edge{Parent: p, Kind: link.KindCaused}
	}

	return link.Forest{Findings: findings, Edges: edges}
}

const root = -1

// TestTransientNeedsAllThreeBounds covers D-43. Volume, blast radius and
// duration are independent axes, and a finding that is small on one of them is
// not a blip. 05-test-b's disk-pressure eviction of data-pipeline is three
// occurrences; small; across three pods over four and a half minutes, which
// is not transient by any reading.
func TestTransientNeedsAllThreeBounds(t *testing.T) {
	tests := []struct {
		name string
		f    group.Finding
		want bool
	}{
		{"small on every axis", issue("api-gateway", "evicted/memory-pressure", 3, 1, 12*time.Second), true},
		{"too many occurrences", issue("api-gateway", "evicted/memory-pressure", 18, 1, 12*time.Second), false},
		{"too many pods", issue("api-gateway", "evicted/memory-pressure", 3, 3, 12*time.Second), false},
		{"too long a span", issue("api-gateway", "evicted/memory-pressure", 3, 1, 5*time.Minute), false},
		{"no pods at all", issue("node-4", "node/disk-pressure", 1, 0, 0), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := diagnose.Build(forest([]group.Finding{tt.f}, root), start.Add(time.Hour))
			assert.Equal(t, tt.want, c.Diagnoses[0].Pattern == diagnose.PatternTransient)
		})
	}
}

// TestSuppressionNeedsAllFourClauses is the core of D-44.
func TestSuppressionNeedsAllFourClauses(t *testing.T) {
	// 01-healthy's decoy: nothing explains it, it explains nothing.
	decoy := issue("data-pipeline", "evicted/memory-pressure", 1, 1, 0)

	// 05-test-b's node condition: shape-identical to the decoy, and the most
	// important line in that capture. Kept only because six findings hang off it.
	condition := group.Finding{
		Kind: event.KindNode, Workload: "node-4", Namespace: "default",
		Reason: "NodeHasDiskPressure", Rule: "node/disk-pressure",
		Category: classify.CategoryIssue, Severity: event.SeverityCritical, Recognised: true,
		Count: 1, FirstSeen: start, LastSeen: start, Nodes: []string{"node-4"},
	}

	// 05-test-b's eviction: shape-identical to the decoy, and a pod killed by a
	// dying node. Kept only because the condition explains it.
	evicted := issue("auth-service", "evicted/disk-pressure", 1, 1, 0)

	t.Run("unexplained and unexplanatory is suppressed", func(t *testing.T) {
		c := diagnose.Build(forest([]group.Finding{decoy}, root), start.Add(time.Hour))
		assert.True(t, c.Diagnoses[0].Suppressed())
	})

	t.Run("explanatory survives", func(t *testing.T) {
		c := diagnose.Build(forest([]group.Finding{condition, evicted}, root, 0), start.Add(time.Hour))
		assert.False(t, c.Diagnoses[0].Suppressed(), "it explains six evictions")
	})

	t.Run("explained survives", func(t *testing.T) {
		c := diagnose.Build(forest([]group.Finding{condition, evicted}, root, 0), start.Add(time.Hour))
		assert.False(t, c.Diagnoses[1].Suppressed(), "the node condition explains it")
	})

	t.Run("large and unexplained survives", func(t *testing.T) {
		big := issue("checkout-service", "unhealthy/readiness", 222, 5, 8*time.Minute)
		c := diagnose.Build(forest([]group.Finding{big}, root), start.Add(time.Hour))
		assert.False(t, c.Diagnoses[0].Suppressed(), "shape alone keeps it")
	})
}

// TestSuppressionOnlyAppliesToIssues covers the guard in D-44.
func TestSuppressionOnlyAppliesToIssues(t *testing.T) {
	unclassified := group.Finding{
		Kind: event.KindPod, Workload: "data-pipeline", Namespace: "data",
		Reason: "LALALALA", Rule: "unclassified",
		Category: classify.CategoryUnclassified, Severity: event.SeverityInfo,
		Count: 1, FirstSeen: start, LastSeen: start,
		Pods: []string{"data-pipeline-abcdef123-0000a"},
	}
	marker := group.Finding{
		Kind: "Deployment", Workload: "payment-service", Namespace: "production",
		Reason: "ScalingReplicaSet", Rule: "deploy/scaled",
		Category: classify.CategoryDeployMarker, Severity: event.SeverityInfo,
		Count: 1, FirstSeen: start, LastSeen: start,
	}

	c := diagnose.Build(forest([]group.Finding{unclassified, marker}, root, root), start.Add(time.Hour))

	assert.False(t, c.Diagnoses[0].Suppressed(), "an uninterpretable reason must stay visible")
	assert.False(t, c.Diagnoses[1].Suppressed(), "a rollout is an anchor, not background")
}

// TestAnUnrecognisedReasonIsNeitherLabelledNorBuried covers D-46.
//
// 06-test-c plants a Warning with a reason no taxonomy row covers, whose body
// makes the argument itself: "In case there are some new events that we haven't
// really recognized and handled, we'd much rather surface it, instead of burying
// it." It is a CategoryIssue, and it is transient, unexplained and childless on
// every measure; so shape alone would suppress it.
func TestAnUnrecognisedReasonIsNeitherLabelledNorBuried(t *testing.T) {
	probe := issue("data-pipeline", "unrecognised-warning", 1, 1, 0)
	probe.Reason = "LALALALA"
	probe.Recognised = false

	c := diagnose.Build(forest([]group.Finding{probe}, root), start.Add(time.Hour))

	assert.False(t, c.Diagnoses[0].Suppressed(), "we cannot dismiss what we could not read")
	assert.Equal(t, diagnose.PatternNone, c.Diagnoses[0].Pattern, "and we cannot categorise it either")
}

// TestNonIssuesGetNoPattern covers D-42's two abstentions. Both findings are
// transient by every shape measure, and neither may be labelled a transient
// blip: one is a deployment that succeeded, the other is a reason we could not
// read.
func TestNonIssuesGetNoPattern(t *testing.T) {
	unclassified := group.Finding{
		Reason: "LALALALA", Rule: "unclassified", Category: classify.CategoryUnclassified,
		Count: 1, FirstSeen: start, LastSeen: start,
	}
	marker := group.Finding{
		Reason: "ScalingReplicaSet", Rule: "deploy/scaled", Category: classify.CategoryDeployMarker,
		Count: 1, FirstSeen: start, LastSeen: start,
	}

	c := diagnose.Build(forest([]group.Finding{unclassified, marker}, root, root), start.Add(time.Hour))

	assert.Equal(t, diagnose.PatternNone, c.Diagnoses[0].Pattern)
	assert.Equal(t, diagnose.PatternNone, c.Diagnoses[1].Pattern)
}

// TestCapacityOutranksDeployCorrelated is D-42's ordering principle under test.
//
// 06-test-c's FailedScheduling is truthfully both: its body names insufficient
// CPU, and its root is a rollout 4.7s earlier. The pattern must name the
// mechanism, because the trigger is already stated by the causal edge with far
// more detail than a one-word label could carry.
func TestCapacityOutranksDeployCorrelated(t *testing.T) {
	marker := group.Finding{
		Kind: "Deployment", Workload: "data-pipeline", Namespace: "data",
		Reason: "ScalingReplicaSet", Rule: "deploy/scaled",
		Category: classify.CategoryDeployMarker, Count: 1, FirstSeen: start, LastSeen: start,
	}
	scheduling := issue("data-pipeline", classify.RuleFailedScheduling, 177, 6, 9*time.Minute)
	scheduling.Reason = "FailedScheduling"
	scheduling.Events = []event.Event{{
		Body: "0/6 nodes are available: 6 Insufficient cpu. preemption: 0/6 nodes are available.",
	}}

	c := diagnose.Build(forest([]group.Finding{marker, scheduling}, root, 0), start.Add(time.Hour))

	assert.Equal(t, diagnose.PatternCapacity, c.Diagnoses[1].Pattern)
}

// TestSchedulingWithoutInsufficientResourcesIsNotCapacity guards the body test.
// A pod that cannot be placed because of a taint or an affinity rule is a
// scheduling failure, not a capacity one, and claiming the cluster is full when
// it is not sends the reader to the wrong dashboard.
func TestSchedulingWithoutInsufficientResourcesIsNotCapacity(t *testing.T) {
	marker := group.Finding{
		Kind: "Deployment", Workload: "data-pipeline", Reason: "ScalingReplicaSet",
		Rule: "deploy/scaled", Category: classify.CategoryDeployMarker,
		Count: 1, FirstSeen: start, LastSeen: start,
	}
	scheduling := issue("data-pipeline", classify.RuleFailedScheduling, 40, 4, 9*time.Minute)
	scheduling.Events = []event.Event{{
		Body: "0/6 nodes are available: 6 node(s) had untolerated taint {node-role.kubernetes.io/control-plane: }.",
	}}

	c := diagnose.Build(forest([]group.Finding{marker, scheduling}, root, 0), start.Add(time.Hour))

	assert.Equal(t, diagnose.PatternDeployCorrelated, c.Diagnoses[1].Pattern)
}

// TestCrashLoopKeysOnRuleNotReason is why classify carries a rule ID at all.
func TestCrashLoopKeysOnRuleNotReason(t *testing.T) {
	marker := group.Finding{
		Kind: "Deployment", Workload: "payment-service", Reason: "ScalingReplicaSet",
		Rule: "deploy/scaled", Category: classify.CategoryDeployMarker,
		Count: 1, FirstSeen: start, LastSeen: start,
	}
	crashLoop := issue("recommendation-service", classify.RuleCrashLoop, 91, 4, 26*time.Minute)
	pullRetry := issue("payment-service", "backoff/image-pull-retry", 18, 3, 10*time.Minute)

	c := diagnose.Build(forest([]group.Finding{marker, crashLoop, pullRetry}, root, root, 0), start.Add(time.Hour))

	assert.Equal(t, diagnose.PatternCrashLoop, c.Diagnoses[1].Pattern)
	assert.Equal(t, diagnose.PatternDeployCorrelated, c.Diagnoses[2].Pattern)
}

// TestASingleOOMKillIsNotACrashLoop keeps the word "sustained" honest. One kill
// is an incident; a loop is the same kill happening over and over.
func TestASingleOOMKillIsNotACrashLoop(t *testing.T) {
	once := issue("recommendation-service", classify.RuleOOMKilled, 1, 1, 0)

	c := diagnose.Build(forest([]group.Finding{once}, root), start.Add(time.Hour))

	assert.Equal(t, diagnose.PatternTransient, c.Diagnoses[0].Pattern)
}

// TestNodeIssueIsInheritedFromTheRoot covers the whole 05-test-b incident: the
// condition and every pod it evicted carry one label, so the reader sees one
// node failure rather than seven unrelated warnings.
func TestNodeIssueIsInheritedFromTheRoot(t *testing.T) {
	condition := group.Finding{
		Kind: event.KindNode, Workload: "node-4", Reason: "NodeHasDiskPressure",
		Rule: "node/disk-pressure", Category: classify.CategoryIssue, Recognised: true,
		Severity: event.SeverityCritical, Count: 1, FirstSeen: start, LastSeen: start,
		Nodes: []string{"node-4"},
	}
	evicted := issue("auth-service", "evicted/disk-pressure", 1, 1, 0)

	c := diagnose.Build(forest([]group.Finding{condition, evicted}, root, 0), start.Add(time.Hour))

	assert.Equal(t, diagnose.PatternNodeIssue, c.Diagnoses[0].Pattern)
	assert.Equal(t, diagnose.PatternNodeIssue, c.Diagnoses[1].Pattern)
}

// TestConfidenceIsDecidedByWhatTheRootIs covers D-34, which needs no threshold.
func TestConfidenceIsDecidedByWhatTheRootIs(t *testing.T) {
	marker := group.Finding{
		Kind: "Deployment", Workload: "payment-service", Reason: "ScalingReplicaSet",
		Rule: "deploy/scaled", Category: classify.CategoryDeployMarker,
		Count: 1, FirstSeen: start, LastSeen: start,
	}
	condition := group.Finding{
		Kind: event.KindNode, Workload: "node-4", Reason: "NodeHasDiskPressure",
		Rule: "node/disk-pressure", Category: classify.CategoryIssue, Recognised: true,
		Count: 1, FirstSeen: start, LastSeen: start,
	}
	oom := issue("recommendation-service", classify.RuleOOMKilled, 26, 4, 25*time.Minute)
	backOff := issue("recommendation-service", classify.RuleCrashLoop, 91, 4, 26*time.Minute)
	alone := issue("inventory-service", "failed-mount", 1, 1, 0)

	t.Run("a rollout root explains its descendants", func(t *testing.T) {
		c := diagnose.Build(forest([]group.Finding{marker, backOff}, root, 0), start.Add(time.Hour))
		assert.Equal(t, diagnose.ConfidenceExplained, c.Diagnoses[1].Confidence)
	})

	t.Run("a node condition root explains its descendants", func(t *testing.T) {
		c := diagnose.Build(forest([]group.Finding{condition, alone}, root, 0), start.Add(time.Hour))
		assert.Equal(t, diagnose.ConfidenceExplained, c.Diagnoses[1].Confidence)
	})

	t.Run("a failure root with children is partially explained", func(t *testing.T) {
		c := diagnose.Build(forest([]group.Finding{oom, backOff}, root, 0), start.Add(time.Hour))
		assert.Equal(t, diagnose.ConfidencePartial, c.Diagnoses[0].Confidence,
			"the crash-loop is explained by the OOM kills; what drove the memory growth is not in this capture")
		assert.Equal(t, diagnose.ConfidencePartial, c.Diagnoses[1].Confidence,
			"a finding is only as explained as its root")
	})

	t.Run("a failure root with no children is unexplained", func(t *testing.T) {
		c := diagnose.Build(forest([]group.Finding{alone}, root), start.Add(time.Hour))
		assert.Equal(t, diagnose.ConfidenceUnexplained, c.Diagnoses[0].Confidence)
	})
}

// TestReportedSkipsSuppressed is what the renderer iterates.
func TestReportedSkipsSuppressed(t *testing.T) {
	decoy := issue("data-pipeline", "evicted/memory-pressure", 1, 1, 0)
	real := issue("checkout-service", "unhealthy/readiness", 222, 5, 8*time.Minute)

	c := diagnose.Build(forest([]group.Finding{decoy, real}, root, root), start.Add(time.Hour))

	assert.Equal(t, []int{1}, c.Reported())
	assert.Equal(t, 1, c.SuppressedCount(), "the count is disclosed, not discarded")
	assert.Len(t, c.Findings, 2, "and the finding itself is still there")
}

func TestBuildOnAnEmptyForest(t *testing.T) {
	c := diagnose.Build(link.Forest{}, time.Time{})

	assert.Empty(t, c.Diagnoses)
	assert.Empty(t, c.Reported())
	assert.Zero(t, c.SuppressedCount())
}

// TestDiagnosesAreParallelToFindings is the invariant D-40 exists to protect.
func TestDiagnosesAreParallelToFindings(t *testing.T) {
	findings := []group.Finding{
		issue("a", "failed-mount", 1, 1, 0),
		issue("b", "failed-mount", 1, 1, 0),
		issue("c", "failed-mount", 1, 1, 0),
	}

	c := diagnose.Build(forest(findings, root, root, 1), start.Add(time.Hour))

	require.Len(t, c.Diagnoses, len(c.Findings))
}
