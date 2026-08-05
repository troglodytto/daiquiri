package group_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/troglodytto/daiquiri/internal/classify"
	"github.com/troglodytto/daiquiri/internal/event"
	"github.com/troglodytto/daiquiri/internal/group"
)

// at builds a timestamp inside the fixtures' capture window.
func at(mmss string) time.Time {
	ts, err := time.Parse(time.RFC3339Nano, "2024-06-01T10:"+mmss+"Z")
	if err != nil {
		panic(err)
	}
	return ts
}

// pod builds a classified Pod event with the given rule verdict.
func pod(name, ns, reason, node, rule string, ts time.Time) group.Classified {
	return group.Classified{
		Event: event.Event{
			Timestamp: ts,
			Reason:    reason,
			Namespace: ns,
			Node:      node,
			Object:    event.Object{Kind: event.KindPod, Name: name},
			Count:     1,
			EventUID:  name + reason + ts.String(),
		},
		Class: classify.Classification{
			Category: classify.CategoryIssue,
			Severity: event.SeverityWarning,
			Rule:     rule,
			Cause:    "cause for " + rule,
		},
	}
}

// TestCoalesceRollsUpPodInstances is the core of the brief's requirement that
// the tool coalesce independent records itself. Five pod instances of one
// service are one fact about that service.
func TestCoalesceRollsUpPodInstances(t *testing.T) {
	in := []group.Classified{
		pod("checkout-service-7d4f8b9c5-005e2", "production", "Unhealthy", "node-2", "unhealthy/readiness", at("22:07.319")),
		pod("checkout-service-7d4f8b9c5-c257b", "production", "Unhealthy", "node-2", "unhealthy/readiness", at("22:14.454")),
		pod("checkout-service-7d4f8b9c5-7f871", "production", "Unhealthy", "node-3", "unhealthy/readiness", at("22:15.743")),
		pod("checkout-service-7d4f8b9c5-cc6bb", "production", "Unhealthy", "node-5", "unhealthy/readiness", at("29:55.734")),
	}

	got := group.Coalesce(in)

	require.Len(t, got, 1, "one workload, one reason, one rule is one finding")
	f := got[0]
	assert.Equal(t, "checkout-service", f.Workload)
	assert.Equal(t, 4, f.Count)
	assert.Equal(t, at("22:07.319"), f.FirstSeen)
	assert.Equal(t, at("29:55.734"), f.LastSeen)
	assert.Len(t, f.Pods, 4, "blast radius is distinct instances")
	assert.Equal(t, []string{"node-2", "node-3", "node-5"}, f.Nodes, "nodes are observed and sorted, not keyed on")
	assert.Len(t, f.Events, 4, "every occurrence is retained so shape stays derivable")
}

// TestCoalesceSeparatesDistinctFailureModes covers 05-test-b, where one
// workload is evicted twice for genuinely different reasons: once for node
// memory pressure as background noise, and later as part of a node-wide disk
// pressure incident. Merging them puts FirstSeen thirteen minutes before the
// cause of the incident.
func TestCoalesceSeparatesDistinctFailureModes(t *testing.T) {
	in := []group.Classified{
		pod("data-pipeline-7a960e693-836d4", "data", "Evicted", "node-2", "evicted/memory-pressure", at("02:32.345")),
		pod("data-pipeline-26f6fad4e-0e53b", "data", "Evicted", "node-4", "evicted/disk-pressure", at("15:25.779")),
		pod("data-pipeline-de2124cc2-7a2a0", "data", "Evicted", "node-4", "evicted/disk-pressure", at("15:43.272")),
		pod("data-pipeline-de2124cc2-9e145", "data", "Evicted", "node-4", "evicted/disk-pressure", at("19:56.562")),
	}

	got := group.Coalesce(in)

	require.Len(t, got, 2, "same workload and reason, two failure modes, two findings")
	assert.Equal(t, "evicted/memory-pressure", got[0].Rule)
	assert.Equal(t, 1, got[0].Count)
	assert.Equal(t, "evicted/disk-pressure", got[1].Rule)
	assert.Equal(t, 3, got[1].Count)
	assert.Equal(t, at("15:25.779"), got[1].FirstSeen,
		"the incident finding must not inherit the background event's start time")
}

func TestCoalesceSeparatesOnEachKeyField(t *testing.T) {
	base := pod("api-gateway-027774d6c-aaaaa", "production", "Unhealthy", "node-1", "unhealthy/readiness", at("10:00.000"))

	tests := []struct {
		name  string
		other group.Classified
	}{
		{"different workload", pod("auth-service-027774d6c-aaaaa", "production", "Unhealthy", "node-1", "unhealthy/readiness", at("11:00.000"))},
		{"different namespace", pod("api-gateway-027774d6c-aaaaa", "staging", "Unhealthy", "node-1", "unhealthy/readiness", at("11:00.000"))},
		{"different reason", pod("api-gateway-027774d6c-aaaaa", "production", "FailedMount", "node-1", "failedmount", at("11:00.000"))},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Len(t, group.Coalesce([]group.Classified{base, tt.other}), 2)
		})
	}
}

// TestCoalesceMergesAcrossNodes is the regression for a grouping key that
// included the node. A memory leak is a property of the workload; the nodes its
// pods happen to land on are incidental, and keying on them fragments one
// crash-loop into one finding per node.
func TestCoalesceMergesAcrossNodes(t *testing.T) {
	in := []group.Classified{
		pod("recommendation-service-5a8e2c1f4-ea1b0", "production", "BackOff", "node-3", "backoff/crash-loop", at("02:44.400")),
		pod("recommendation-service-5a8e2c1f4-e0556", "production", "BackOff", "node-4", "backoff/crash-loop", at("03:34.292")),
		pod("recommendation-service-5a8e2c1f4-bd016", "production", "BackOff", "node-1", "backoff/crash-loop", at("03:49.037")),
		pod("recommendation-service-5a8e2c1f4-b35ff", "production", "BackOff", "node-6", "backoff/crash-loop", at("03:52.752")),
	}

	got := group.Coalesce(in)

	require.Len(t, got, 1)
	assert.Equal(t, 4, got[0].Count)
	assert.Len(t, got[0].Nodes, 4)
}

// TestCoalesceCountsOccurrencesNotRecords covers the pipeline shape the brief
// requires but the fixtures cannot exercise: k8s.event.count is 1 for all
// 120,001 provided records, so this path is only reachable synthetically.
func TestCoalesceCountsOccurrencesNotRecords(t *testing.T) {
	mk := func(uid string, count int, ts time.Time) group.Classified {
		c := pod("checkout-service-7d4f8b9c5-005e2", "production", "BackOff", "node-2", "backoff/crash-loop", ts)
		c.Event.EventUID = uid
		c.Event.Count = count
		return c
	}

	got := group.Coalesce([]group.Classified{
		mk("uid-a", 1, at("22:00.000")),
		mk("uid-a", 2, at("22:10.000")),
		mk("uid-a", 5, at("22:20.000")), // same logical event, count climbing
		mk("uid-b", 3, at("22:30.000")), // a second logical event
	})

	require.Len(t, got, 1)
	assert.Equal(t, 8, got[0].Count, "max per uid (5) plus max per uid (3), not the record count and not the sum")
	assert.Len(t, got[0].Events, 4, "every record is still retained as evidence")
}

// TestCoalesceIsDeterministic guards the trap that Go randomises map iteration
// order. Sorting findings on FirstSeen alone leaves ties; 05-test-b has
// several evictions inside the same second; so the sort must run through the
// whole key. Without it, two runs on identical input differ and golden tests
// fail intermittently, which is the worst way to discover this.
func TestCoalesceIsDeterministic(t *testing.T) {
	sameInstant := at("15:00.000")
	in := []group.Classified{
		pod("metrics-collector-46dcac1b0-35a6d", "data", "Evicted", "node-4", "evicted/disk-pressure", sameInstant),
		pod("auth-service-cb5f88109-57949", "production", "Evicted", "node-4", "evicted/disk-pressure", sameInstant),
		pod("batch-reporter-820090759-b420b", "data", "Evicted", "node-4", "evicted/disk-pressure", sameInstant),
		pod("notification-service-1afa35f80-97fed", "staging", "Evicted", "node-4", "evicted/disk-pressure", sameInstant),
	}

	want := group.Coalesce(in)
	require.Len(t, want, 4, "four workloads tied on the same instant")

	const runs = 100
	for i := 0; i < runs; i++ {
		assert.Equal(t, want, group.Coalesce(in), "run %d diverged", i)
	}
}

func TestCoalesceEmptyInput(t *testing.T) {
	assert.Empty(t, group.Coalesce(nil))
}

// TestCoalesceOnlyCollectsPodsForPodFindings guards a field that used to lie.
//
// Pods was built from k8s.object.name without checking the kind, so a Node
// finding carried Pods=["node-4"] and a rollout carried Pods=["payment-service"]
// ; a field named Pods holding a node and a deployment. The report counted
// those as pods, and the same-pod causal rule could fire between two non-pods
// and print evidence reading "same pod node-4", which is text that ends up
// quoted in the analysis report.
func TestCoalesceOnlyCollectsPodsForPodFindings(t *testing.T) {
	nodeEvent := group.Classified{
		Event: event.Event{
			Timestamp: at("15:00.000"), Reason: "NodeHasDiskPressure", Namespace: "default",
			Node: "node-4", Count: 1, EventUID: "uid-node",
			Object: event.Object{Kind: event.KindNode, Name: "node-4"},
		},
		Class: classify.Classification{Category: classify.CategoryIssue, Rule: "node/disk-pressure"},
	}
	deployEvent := group.Classified{
		Event: event.Event{
			Timestamp: at("17:59.977"), Reason: "ScalingReplicaSet", Namespace: "production",
			Count: 1, EventUID: "uid-deploy",
			Object: event.Object{Kind: "Deployment", Name: "payment-service"},
		},
		Class: classify.Classification{Category: classify.CategoryDeployMarker, Rule: "deploy/scaled"},
	}
	podEvent := pod("checkout-service-7d4f8b9c5-005e2", "production", "Unhealthy", "node-2",
		"unhealthy/readiness", at("22:07.319"))

	got := group.Coalesce([]group.Classified{nodeEvent, deployEvent, podEvent})
	require.Len(t, got, 3)

	byKind := map[string]group.Finding{}
	for _, f := range got {
		byKind[f.Kind] = f
	}

	assert.Empty(t, byKind[event.KindNode].Pods, "a node condition is not about pods")
	assert.Equal(t, []string{"node-4"}, byKind[event.KindNode].Nodes,
		"but its node is still recorded, because the causal rules key on it")

	assert.Empty(t, byKind["Deployment"].Pods, "a rollout is not about pods")

	assert.Equal(t, []string{"checkout-service-7d4f8b9c5-005e2"}, byKind[event.KindPod].Pods,
		"a pod finding still carries its instances")
}
