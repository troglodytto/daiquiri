package triage_test

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/troglodytto/daiquiri/internal/classify"
	"github.com/troglodytto/daiquiri/internal/event"
	"github.com/troglodytto/daiquiri/internal/group"
	"github.com/troglodytto/daiquiri/internal/triage"
)

// budget is the brief's acceptance criterion for a 20,000-record capture.
const budget = 5 * time.Second

// run drives the whole pipeline over a fixture.
func run(t *testing.T, fixture string) triage.Result {
	t.Helper()

	f, err := os.Open("../../testdata/" + fixture)
	require.NoError(t, err)
	t.Cleanup(func() { _ = f.Close() })

	res, err := triage.New(classify.New()).Run(f)
	require.NoError(t, err)

	return res
}

// find returns the single finding matching workload and reason.
func find(t *testing.T, res triage.Result, workload, reason string) group.Finding {
	t.Helper()

	var hits []group.Finding
	for _, f := range res.Forest.Findings {
		if f.Workload == workload && f.Reason == reason {
			hits = append(hits, f)
		}
	}

	require.Len(t, hits, 1, "expected exactly one %s/%s finding", workload, reason)

	return hits[0]
}

// TestPipelineCoalescesEveryFixture pins the coalescing outcome for all six
// provided captures. These counts are the acceptance data for Step 2: they were
// derived independently from the raw JSONL before the grouper existed, so a
// change here is a change in behaviour, not a test that follows the code.
func TestPipelineCoalescesEveryFixture(t *testing.T) {
	tests := []struct {
		fixture      string
		wantRecords  int
		wantFindings int
	}{
		{"01-healthy.jsonl", 20000, 3},
		{"02-memory-leak.jsonl", 20000, 5},
		{"03-image-pull-failure.jsonl", 20000, 6},
		{"04-test-a.jsonl", 20000, 5},
		{"05-test-b.jsonl", 20000, 10},
		{"06-test-c.jsonl", 20001, 6},
	}
	for _, tt := range tests {
		t.Run(tt.fixture, func(t *testing.T) {
			res := run(t, tt.fixture)

			assert.Equal(t, tt.wantRecords, res.Ingested)
			assert.Zero(t, res.Skipped, "no provided capture contains a malformed record")
			assert.Len(t, res.Forest.Findings, tt.wantFindings)

			assert.Len(t, res.Records, tt.wantRecords,
				"every record is retained, including the ~19,800 filtered as noise")
			assert.Less(t, res.Elapsed, budget)
		})
	}
}

// TestHealthyCaptureSurfacesNothingSustained is the brief's "no false
// positives on 01-healthy.jsonl" criterion, expressed at the grouping stage.
//
// The capture is not free of warnings -- every one of the six fixtures carries
// the same background floor of 1 Evicted, 1 FailedMount and 3 Unhealthy on
// randomly chosen workloads. Those are genuine Warning records that the
// taxonomy correctly calls issues, so no reason-based filter can remove them.
//
// What must hold is that none of them looks like a real problem: each is a
// handful of events on a single pod inside a few seconds. Suppressing them is
// the diagnose stage's job; grouping's job is to preserve the shape that makes
// the decision possible.
func TestHealthyCaptureSurfacesNothingSustained(t *testing.T) {
	res := run(t, "01-healthy.jsonl")

	for _, f := range res.Forest.Findings {
		t.Run(f.Workload+"/"+f.Reason, func(t *testing.T) {
			assert.Less(t, f.Severity, event.SeverityCritical, "nothing critical in a healthy cluster")
			assert.LessOrEqual(t, f.Count, 3, "background warnings come in ones and threes")
			assert.Len(t, f.Pods, 1, "background warnings never spread across instances")
			assert.Less(t, f.LastSeen.Sub(f.FirstSeen), 30*time.Second, "and never last")
		})
	}
}

// TestMemoryLeakCoalescesAcrossPodsAndNodes covers 02, where four pod instances
// of one service crash-loop on four different nodes. Both the pod instance and
// the node must be rolled up, or one memory leak reports as eight findings.
func TestMemoryLeakCoalescesAcrossPodsAndNodes(t *testing.T) {
	res := run(t, "02-memory-leak.jsonl")

	oom := find(t, res, "recommendation-service", "OOMKilling")
	assert.Equal(t, 26, oom.Count)
	assert.Len(t, oom.Pods, 4)
	assert.Len(t, oom.Nodes, 4, "one workload, four nodes, still one finding")
	assert.Equal(t, event.SeverityCritical, oom.Severity)

	backOff := find(t, res, "recommendation-service", "BackOff")
	assert.Equal(t, 91, backOff.Count)
	assert.Equal(t, "backoff/crash-loop", backOff.Rule,
		"a Warning BackOff is a crash-loop, not an image-pull retry")
}

// TestImagePullIsDistinguishedByBody covers 03. ImagePullBackOff and
// ErrImagePull are not Kubernetes event reasons -- they appear only inside the
// body of Failed events -- so only a body-sensitive rule tells an image-pull
// failure from a sandbox-creation failure.
func TestImagePullIsDistinguishedByBody(t *testing.T) {
	res := run(t, "03-image-pull-failure.jsonl")

	failed := find(t, res, "payment-service", "Failed")
	assert.Equal(t, "failed/image-pull", failed.Rule)
	assert.Equal(t, 24, failed.Count)

	assert.Equal(t, "backoff/image-pull-retry", find(t, res, "payment-service", "BackOff").Rule,
		"a Normal BackOff alongside an image-pull failure is the retry, not a crash-loop")
}

// TestEvictionsSplitByFailureMode is the regression for the grouping key.
//
// 05-test-b evicts data-pipeline twice for genuinely different reasons: once at
// 10:02:32 on node-2 for node memory pressure, unrelated background noise, and
// three times from 10:15:25 on node-4 as part of a node-wide disk pressure
// incident that began at 10:15:00.
//
// Keyed on reason alone these merge, and the merged finding starts at 10:02:32
// -- thirteen minutes before the node condition that explains three quarters of
// it. A cause cannot postdate its effect, so the causal link would then be
// correctly rejected and the whole diagnosis lost.
func TestEvictionsSplitByFailureMode(t *testing.T) {
	res := run(t, "05-test-b.jsonl")

	var evictions []group.Finding
	for _, f := range res.Forest.Findings {
		if f.Workload == "data-pipeline" && f.Reason == "Evicted" {
			evictions = append(evictions, f)
		}
	}
	require.Len(t, evictions, 2, "one workload, one reason, two failure modes")

	background, incident := evictions[0], evictions[1]

	assert.Equal(t, "evicted/memory-pressure", background.Rule)
	assert.Equal(t, 1, background.Count)
	assert.Equal(t, []string{"node-2"}, background.Nodes)

	assert.Equal(t, "evicted/disk-pressure", incident.Rule)
	assert.Equal(t, 3, incident.Count)
	assert.Equal(t, []string{"node-4"}, incident.Nodes)
	assert.True(t, incident.FirstSeen.After(background.LastSeen),
		"the incident finding must not inherit the background event's start time")
}

// TestNodeConditionCarriesItsNodeIdentity covers the record shape where the
// node a finding correlates on is not in the field that names it: a Node-kind
// event has no k8s.node.name, only k8s.object.name. This is the anchor for the
// whole node-pressure diagnosis, and an empty Node here silently disables every
// node-correlation rule downstream.
func TestNodeConditionCarriesItsNodeIdentity(t *testing.T) {
	f := find(t, run(t, "05-test-b.jsonl"), "node-4", "NodeHasDiskPressure")

	assert.Equal(t, event.KindNode, f.Kind)
	assert.Equal(t, []string{"node-4"}, f.Nodes)
	assert.Equal(t, event.SeverityCritical, f.Severity)
}

// TestUnclassifiedReasonIsSurfaced covers the probe record planted in 06, whose
// body asks not to be buried. It is Normal severity, so routing unrecognised
// Normal events to noise hides exactly the record that argues against hiding.
func TestUnclassifiedReasonIsSurfaced(t *testing.T) {
	res := run(t, "06-test-c.jsonl")

	assert.Equal(t, 1, res.Unrecognised, "the header discloses what could not be interpreted")

	f := find(t, res, "data-pipeline", "LALALALA")
	assert.Equal(t, classify.CategoryUnclassified, f.Category)
	assert.NotEmpty(t, f.Cause)
}

// TestFindingsAreOrderedAndDeterministic guards the trap that Go randomises map
// iteration. 05-test-b has evictions inside the same second, so a sort on
// FirstSeen alone leaves ties broken by map order -- and golden tests would
// then fail intermittently rather than reproducibly.
func TestFindingsAreOrderedAndDeterministic(t *testing.T) {
	first := run(t, "05-test-b.jsonl").Forest.Findings

	for i := 1; i < len(first); i++ {
		assert.False(t, first[i].FirstSeen.Before(first[i-1].FirstSeen),
			"findings are ordered by start time")
	}

	// Few repeats on purpose: each one re-decodes 20,000 records. The dense
	// determinism check lives in group's own tests, which hammer Coalesce a
	// hundred times over a handful of same-instant events. This one only
	// confirms the property survives the whole pipeline.
	const runs = 3
	for i := 0; i < runs; i++ {
		assert.Equal(t, first, run(t, "05-test-b.jsonl").Forest.Findings, "run %d diverged", i)
	}
}

// TestForestMatchesTheFixtures pins the causal edge derived for every finding in
// every provided capture.
//
// These assignments were produced by simulating the rules against the raw JSONL
// before internal/link existed, so they are acceptance data rather than a test
// written to agree with the code. An empty parent means the finding is a root:
// nothing in the capture explains it.
//
// Findings are labelled workload/reason[rule] because reason alone is ambiguous:
// 05-test-b evicts data-pipeline twice for different causes, and only one of
// them belongs to the incident.
func TestForestMatchesTheFixtures(t *testing.T) {
	tests := []struct {
		fixture string
		want    map[string]string // child -> parent, "" for a root
	}{
		{"01-healthy.jsonl", map[string]string{
			"data-pipeline/Evicted[evicted/memory-pressure]":      "",
			"recommendation-service/FailedMount[failed-mount]":    "",
			"notification-service/Unhealthy[unhealthy/readiness]": "",
		}},
		{"02-memory-leak.jsonl", map[string]string{
			"data-pipeline/Evicted[evicted/memory-pressure]":     "",
			"recommendation-service/OOMKilling[oom-killed]":      "",
			"recommendation-service/BackOff[backoff/crash-loop]": "recommendation-service/OOMKilling[oom-killed]",
			"order-service/Unhealthy[unhealthy/readiness]":       "",
			"inventory-service/FailedMount[failed-mount]":        "",
		}},
		{"03-image-pull-failure.jsonl", map[string]string{
			"data-pipeline/Evicted[evicted/memory-pressure]":    "",
			"auth-service/Unhealthy[unhealthy/readiness]":       "",
			"recommendation-service/FailedMount[failed-mount]":  "",
			"payment-service/ScalingReplicaSet[deploy/scaled]":  "",
			"payment-service/Failed[failed/image-pull]":         "payment-service/ScalingReplicaSet[deploy/scaled]",
			"payment-service/BackOff[backoff/image-pull-retry]": "payment-service/Failed[failed/image-pull]",
		}},
		{"04-test-a.jsonl", map[string]string{
			"data-pipeline/Evicted[evicted/memory-pressure]":    "",
			"auth-service/FailedMount[failed-mount]":            "",
			"data-pipeline/Unhealthy[unhealthy/readiness]":      "",
			"checkout-service/ScalingReplicaSet[deploy/scaled]": "",
			"checkout-service/Unhealthy[unhealthy/readiness]":   "checkout-service/ScalingReplicaSet[deploy/scaled]",
		}},
		{"05-test-b.jsonl", map[string]string{
			"batch-reporter/Unhealthy[unhealthy/readiness]":       "",
			"data-pipeline/Evicted[evicted/memory-pressure]":      "", // node-2, background
			"api-gateway/FailedMount[failed-mount]":               "",
			"node-4/NodeHasDiskPressure[node/disk-pressure]":      "",
			"batch-reporter/Evicted[evicted/disk-pressure]":       "node-4/NodeHasDiskPressure[node/disk-pressure]",
			"data-pipeline/Evicted[evicted/disk-pressure]":        "node-4/NodeHasDiskPressure[node/disk-pressure]",
			"metrics-collector/Evicted[evicted/disk-pressure]":    "node-4/NodeHasDiskPressure[node/disk-pressure]",
			"auth-service/Evicted[evicted/disk-pressure]":         "node-4/NodeHasDiskPressure[node/disk-pressure]",
			"inventory-service/Evicted[evicted/disk-pressure]":    "node-4/NodeHasDiskPressure[node/disk-pressure]",
			"notification-service/Evicted[evicted/disk-pressure]": "node-4/NodeHasDiskPressure[node/disk-pressure]",
		}},
		{"06-test-c.jsonl", map[string]string{
			"batch-reporter/Evicted[evicted/memory-pressure]":   "",
			"checkout-service/FailedMount[failed-mount]":        "",
			"search-service/Unhealthy[unhealthy/readiness]":     "",
			"data-pipeline/ScalingReplicaSet[deploy/scaled]":    "",
			"data-pipeline/FailedScheduling[failed-scheduling]": "data-pipeline/ScalingReplicaSet[deploy/scaled]",
			"data-pipeline/LALALALA[unclassified]":              "", // 600s after the rollout: vetoed by the window
		}},
	}

	label := func(f group.Finding) string { return f.Workload + "/" + f.Reason + "[" + f.Rule + "]" }

	for _, tt := range tests {
		t.Run(tt.fixture, func(t *testing.T) {
			forest := run(t, tt.fixture).Forest
			require.Len(t, forest.Findings, len(tt.want), "expectation must cover every finding")

			for i, f := range forest.Findings {
				want, known := tt.want[label(f)]
				require.True(t, known, "unexpected finding %s", label(f))

				got := ""
				if p := forest.Edges[i].Parent; p != -1 {
					got = label(forest.Findings[p])
				}

				assert.Equal(t, want, got, "%s: wrong parent", label(f))
			}

			// The invariant that makes cycles impossible.
			for i, e := range forest.Edges {
				assert.True(t, e.Parent == -1 || e.Parent < i,
					"Edges[%d].Parent = %d violates Parent < i", i, e.Parent)
			}
		})
	}
}

// TestNodePressureIncidentIsOneTree is the payoff for 05: six workloads across
// three namespaces, all evicted, all resolving to the same root. That is the
// brief's "seemingly unconnected findings are sometimes side effects of the same
// underlying issue", answered by the tool rather than by the reader.
func TestNodePressureIncidentIsOneTree(t *testing.T) {
	forest := run(t, "05-test-b.jsonl").Forest

	var node int = -1
	for i, f := range forest.Findings {
		if f.Reason == "NodeHasDiskPressure" {
			node = i
		}
	}
	require.NotEqual(t, -1, node, "the node condition must be a finding")

	children := forest.Children(node)
	assert.Len(t, children, 6, "six workloads evicted by one node condition")

	namespaces := map[string]bool{}
	for _, c := range children {
		assert.Equal(t, node, forest.RootOf(c), "every eviction traces to the node condition")
		assert.Equal(t, "caused", forest.Edges[c].Kind.String())
		assert.Contains(t, forest.Edges[c].Evidence, "DiskPressure",
			"the evidence must quote the condition the record itself names")
		namespaces[forest.Findings[c].Namespace] = true
	}
	assert.Len(t, namespaces, 3, "data, production and staging")
}
