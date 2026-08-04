package link_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/troglodytto/daiquiri/internal/classify"
	"github.com/troglodytto/daiquiri/internal/event"
	"github.com/troglodytto/daiquiri/internal/group"
	"github.com/troglodytto/daiquiri/internal/link"
)

func at(hhmmss string) time.Time {
	ts, err := time.Parse(time.RFC3339Nano, "2024-06-01T"+hhmmss+"Z")
	if err != nil {
		panic(err)
	}
	return ts
}

// fnd builds a Pod finding. Bodies matter: two of the three rules prove
// themselves from record text, so a finding without bodies cannot be linked.
func fnd(workload, ns, reason string, first time.Time, pods, nodes []string, body string) group.Finding {
	events := make([]event.Event, 0, len(pods))
	for _, p := range pods {
		events = append(events, event.Event{
			Timestamp: first, Reason: reason, Body: body,
			Object: event.Object{Kind: event.KindPod, Name: p},
		})
	}

	return group.Finding{
		Kind: event.KindPod, Workload: workload, Namespace: ns, Reason: reason,
		Category: classify.CategoryIssue, Severity: event.SeverityWarning,
		Count: len(pods), FirstSeen: first, LastSeen: first,
		Pods: pods, Nodes: nodes, Events: events,
	}
}

// nodeCond builds a Node-kind finding: a node condition.
func nodeCond(node, reason string, first time.Time) group.Finding {
	return group.Finding{
		Kind: event.KindNode, Workload: node, Namespace: "default", Reason: reason,
		Category: classify.CategoryIssue, Severity: event.SeverityCritical,
		Count: 1, FirstSeen: first, LastSeen: first,
		Nodes: []string{node}, // no Pods: a node condition is not about pods
		Events: []event.Event{{
			Timestamp: first, Reason: reason,
			Body:   "Node " + node + " status is now: " + reason,
			Object: event.Object{Kind: event.KindNode, Name: node},
		}},
	}
}

// deploy builds a ScalingReplicaSet marker whose body names the replica set.
func deploy(workload, ns, replicaSet string, first time.Time, replicas int) group.Finding {
	return group.Finding{
		Kind: "Deployment", Workload: workload, Namespace: ns, Reason: "ScalingReplicaSet",
		Category: classify.CategoryDeployMarker, Severity: event.SeverityInfo,
		Count: 1, FirstSeen: first, LastSeen: first,
		// no Pods: a rollout is not about pods
		Events: []event.Event{{
			Timestamp: first, Reason: "ScalingReplicaSet",
			Body:   "Scaled up replica set " + replicaSet + " to " + itoa(replicas),
			Object: event.Object{Kind: "Deployment", Name: workload},
		}},
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// TestSamePodLinks covers 02: four pods of one service OOM-kill and then
// crash-loop. Identity of the object is the shared dimension.
func TestSamePodLinks(t *testing.T) {
	pods := []string{"rec-5a8e2c1f4-ea1b0", "rec-5a8e2c1f4-e0556"}
	f := link.Build([]group.Finding{
		fnd("rec", "production", "OOMKilling", at("10:02:37.049"), pods, []string{"node-3"}, "exceeded memory limit"),
		fnd("rec", "production", "BackOff", at("10:02:44.400"), pods, []string{"node-3"}, "Back-off restarting failed container"),
	})

	require.Len(t, f.Edges, 2)
	assert.Equal(t, -1, f.Edges[0].Parent)
	assert.Equal(t, 0, f.Edges[1].Parent)
	assert.Equal(t, link.KindCaused, f.Edges[1].Kind)
	assert.Contains(t, f.Edges[1].Evidence, "rec-5a8e2c1f4-ea1b0")
}

// TestNodeConditionLinksWhenTheBodyNamesIt covers 05. The evicted pod's own
// record names the condition that evicted it; the cluster states the cause,
// so the edge is proven rather than inferred.
func TestNodeConditionLinksWhenTheBodyNamesIt(t *testing.T) {
	f := link.Build([]group.Finding{
		nodeCond("node-4", "NodeHasDiskPressure", at("10:15:00.000")),
		fnd("auth-service", "production", "Evicted", at("10:16:25.429"),
			[]string{"auth-service-cb5f88109-57949"}, []string{"node-4"},
			"The node had condition: [DiskPressure]. "),
	})

	require.Len(t, f.Edges, 2)
	assert.Equal(t, 0, f.Edges[1].Parent)
	assert.Equal(t, link.KindCaused, f.Edges[1].Kind)
	assert.Contains(t, f.Edges[1].Evidence, "DiskPressure")
	assert.Contains(t, f.Edges[1].Evidence, "node-4")
}

// TestNodeConditionRejectsADifferentNamedCause is the control from 05: the
// background eviction at 10:02:32 has the same Evicted reason but its body says
// the node was low on memory, not that it had the disk-pressure condition.
// It must not link even when placed on the same node inside the window.
func TestNodeConditionRejectsADifferentNamedCause(t *testing.T) {
	f := link.Build([]group.Finding{
		nodeCond("node-4", "NodeHasDiskPressure", at("10:15:00.000")),
		fnd("data-pipeline", "data", "Evicted", at("10:15:30.000"),
			[]string{"data-pipeline-7a960e693-836d4"}, []string{"node-4"},
			"The node was low on resource: memory. Container pipeline was using 612Mi."),
	})

	assert.Equal(t, link.KindMayRelate, f.Edges[1].Kind,
		"same node and inside the window, but nothing in the text connects them")
}

// TestNodeConditionRejectsADifferentNode keeps the real 05 shape: the memory
// eviction is on node-2, so it shares no dimension with the node-4 condition.
func TestNodeConditionRejectsADifferentNode(t *testing.T) {
	f := link.Build([]group.Finding{
		nodeCond("node-4", "NodeHasDiskPressure", at("10:15:00.000")),
		fnd("data-pipeline", "data", "Evicted", at("10:15:30.000"),
			[]string{"data-pipeline-7a960e693-836d4"}, []string{"node-2"},
			"The node was low on resource: memory."),
	})

	assert.Equal(t, -1, f.Edges[1].Parent, "no shared dimension at all")
}

// TestDeployLinksOnlyPodsOfTheNamedReplicaSet covers 03/04/06 and D-36. Same
// workload is not enough; the failing pods must belong to the replica set the
// rollout actually created.
func TestDeployLinksOnlyPodsOfTheNamedReplicaSet(t *testing.T) {
	t.Run("pods from the scaled replicaset link", func(t *testing.T) {
		f := link.Build([]group.Finding{
			deploy("payment-service", "production", "payment-service-9e3f1a2b8", at("10:17:59.977"), 3),
			fnd("payment-service", "production", "Failed", at("10:18:04.412"),
				[]string{"payment-service-9e3f1a2b8-005e2", "payment-service-9e3f1a2b8-45cbb"},
				[]string{"node-2"}, "Failed to pull image: ErrImagePull"),
		})

		assert.Equal(t, 0, f.Edges[1].Parent)
		assert.Equal(t, link.KindCaused, f.Edges[1].Kind)
		assert.Contains(t, f.Edges[1].Evidence, "payment-service-9e3f1a2b8")
	})

	t.Run("pods of the same workload but an older replicaset do not link", func(t *testing.T) {
		f := link.Build([]group.Finding{
			deploy("payment-service", "production", "payment-service-9e3f1a2b8", at("10:17:59.977"), 3),
			fnd("payment-service", "production", "Failed", at("10:18:04.412"),
				[]string{"payment-service-0000aaaa1-005e2"}, []string{"node-2"}, "Failed to pull image"),
		})

		assert.Equal(t, -1, f.Edges[1].Parent,
			"the rollout created a different replica set; these pods predate it")
	})
}

// TestSamePodOutranksDeploy is 03's depth-3 chain and the whole point of D-32.
// BackOff matches both the same-pod rule against Failed and the deploy rule
// against the rollout. Specificity ordering keeps Failed between them.
func TestSamePodOutranksDeploy(t *testing.T) {
	pods := []string{"payment-service-9e3f1a2b8-005e2"}
	f := link.Build([]group.Finding{
		deploy("payment-service", "production", "payment-service-9e3f1a2b8", at("10:17:59.977"), 3),
		fnd("payment-service", "production", "Failed", at("10:18:04.412"), pods, []string{"node-2"}, "ErrImagePull"),
		fnd("payment-service", "production", "BackOff", at("10:18:14.858"), pods, []string{"node-2"}, "Back-off pulling image"),
	})

	assert.Equal(t, -1, f.Edges[0].Parent, "the rollout is the root")
	assert.Equal(t, 0, f.Edges[1].Parent, "Failed is caused by the rollout")
	assert.Equal(t, 1, f.Edges[2].Parent, "BackOff is caused by Failed, not directly by the rollout")
}

// TestOverlappingButUnrelatedDoesNotLink is 02's quadrant-1 case. The
// order-service readiness blip sits fully inside the memory leak's interval AND
// shares node-6 with it, yet is unrelated: the parent is not a node condition,
// shares no pod, and is not a deploy marker.
func TestOverlappingButUnrelatedDoesNotLink(t *testing.T) {
	f := link.Build([]group.Finding{
		fnd("rec", "production", "OOMKilling", at("10:14:00.000"),
			[]string{"rec-5a8e2c1f4-ea1b0"}, []string{"node-6"}, "exceeded memory limit"),
		fnd("order-service", "production", "Unhealthy", at("10:17:02.562"),
			[]string{"order-service-9d3b4acc9-57544"}, []string{"node-6"}, "Readiness probe failed"),
	})

	assert.Equal(t, -1, f.Edges[1].Parent,
		"overlapping in time and sharing a node is not a causal claim")
}

// TestWindowVetoes covers D-33. 06's LALALALA sits 600s after the rollout of
// the same workload; the window is what rejects it.
func TestWindowVetoes(t *testing.T) {
	f := link.Build([]group.Finding{
		deploy("data-pipeline", "data", "data-pipeline-3c7d2e1a9", at("10:19:59.977"), 12),
		fnd("data-pipeline", "data", "LALALALA", at("10:29:59.677"),
			[]string{"data-pipeline-3c7d2e1a9-49cdd"}, nil, "unrecognised"),
	})

	assert.Equal(t, -1, f.Edges[1].Parent, "600s is outside the 300s window")
}

// TestParentIsAlwaysEarlier pins the invariant that makes cycles impossible.
func TestParentIsAlwaysEarlier(t *testing.T) {
	f := link.Build([]group.Finding{
		deploy("payment-service", "production", "payment-service-9e3f1a2b8", at("10:17:59.977"), 3),
		fnd("payment-service", "production", "Failed", at("10:18:04.412"),
			[]string{"payment-service-9e3f1a2b8-005e2"}, []string{"node-2"}, "ErrImagePull"),
		fnd("payment-service", "production", "BackOff", at("10:18:14.858"),
			[]string{"payment-service-9e3f1a2b8-005e2"}, []string{"node-2"}, "Back-off pulling image"),
	})

	for i, e := range f.Edges {
		assert.True(t, e.Parent == -1 || e.Parent < i, "Edges[%d].Parent = %d violates Parent < i", i, e.Parent)
	}
}

func TestQueries(t *testing.T) {
	pods := []string{"payment-service-9e3f1a2b8-005e2"}
	f := link.Build([]group.Finding{
		deploy("payment-service", "production", "payment-service-9e3f1a2b8", at("10:17:59.977"), 3),
		fnd("payment-service", "production", "Failed", at("10:18:04.412"), pods, []string{"node-2"}, "ErrImagePull"),
		fnd("payment-service", "production", "BackOff", at("10:18:14.858"), pods, []string{"node-2"}, "Back-off pulling image"),
		fnd("api-gateway", "production", "FailedMount", at("10:19:00.000"),
			[]string{"api-gateway-671566a46-799ae"}, []string{"node-1"}, "configmap not found"),
	})

	assert.Equal(t, []int{0, 3}, f.Roots())
	assert.Equal(t, []int{1}, f.Children(0))
	assert.Equal(t, []int{2}, f.Children(1))
	assert.Empty(t, f.Children(2))

	assert.Equal(t, 0, f.RootOf(2), "walking up from the deepest symptom reaches the rollout")
	assert.Equal(t, 0, f.RootOf(0))
	assert.Equal(t, 3, f.RootOf(3), "an orphan is its own root")
}

func TestEmptyAndSingle(t *testing.T) {
	assert.Empty(t, link.Build(nil).Edges)
	assert.Empty(t, link.Build(nil).Roots())

	one := link.Build([]group.Finding{
		fnd("api-gateway", "production", "FailedMount", at("10:06:03.999"),
			[]string{"api-gateway-671566a46-799ae"}, []string{"node-1"}, "configmap not found"),
	})
	require.Len(t, one.Edges, 1)
	assert.Equal(t, -1, one.Edges[0].Parent)
	assert.Equal(t, []int{0}, one.Roots())
}
