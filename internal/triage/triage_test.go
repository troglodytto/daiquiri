package triage_test

import (
	"os"
	"strings"
	"sync"
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

// fixtures memoises the pipeline result per capture.
//
// Every test in this file drives one of the same six 16MB, 20,000-record
// captures, and the pipeline is deterministic, so re-running it per call bought
// nothing: the suite was doing 66 full decode-classify-coalesce-link passes over
// 6 distinct inputs, about a gigabyte of JSON. Under -race that is the
// difference between a ten-second gate and a hundred-second one.
//
// The Result handed back is shared. No test mutates one, and none should; if a
// test ever needs to sort or rewrite what it is given, it must copy first.
var fixtures sync.Map // fixture name -> *capture

type capture struct {
	once sync.Once
	res  triage.Result
	err  error
}

// run drives the whole pipeline over a fixture, once per fixture per process.
func run(t *testing.T, fixture string) triage.Result {
	t.Helper()

	v, _ := fixtures.LoadOrStore(fixture, &capture{})
	c, ok := v.(*capture)
	require.True(t, ok)

	c.once.Do(func() {
		f, err := os.Open("../../testdata/" + fixture)
		if err != nil {
			c.err = err

			return
		}
		defer func() { _ = f.Close() }()

		c.res, c.err = triage.New(classify.New()).Run(f)
	})

	require.NoError(t, c.err)

	return c.res
}

// find returns the single finding matching workload and reason.
func find(t *testing.T, res triage.Result, workload, reason string) group.Finding {
	t.Helper()

	var hits []group.Finding
	for _, f := range res.Chart.Findings {
		if f.Workload == workload && f.Reason == reason {
			hits = append(hits, f)
		}
	}

	require.Len(t, hits, 1, "expected exactly one %s/%s finding", workload, reason)

	return hits[0]
}

// TestPipelineCoalescesEveryFixture pins the coalescing outcome for all six
// provided captures. These counts are the acceptance data for Step 2: they were
// derived independently from the raw JSONL before the grouper existed. A change
// here is a change in behaviour, and the test does not follow the code.
func TestPipelineCoalescesEveryFixture(t *testing.T) {
	tests := []struct {
		fixture      string
		wantRecords  int
		wantSkipped  int
		wantFindings int
	}{
		{"01-healthy.jsonl", 20000, 0, 3},
		{"02-memory-leak.jsonl", 20000, 0, 5},
		{"03-image-pull-failure.jsonl", 20000, 0, 6},
		{"04-test-a.jsonl", 20000, 0, 5},
		{"05-test-b.jsonl", 20000, 0, 10},

		// 06 carries one deliberately malformed line. It is skipped, counted and
		// disclosed in the header, and the run continues. See
		// TestMalformedLineIsSkippedAndDisclosed.
		{"06-test-c.jsonl", 20000, 1, 5},
	}
	for _, tt := range tests {
		t.Run(tt.fixture, func(t *testing.T) {
			res := run(t, tt.fixture)

			assert.Equal(t, tt.wantRecords, res.Ingested)
			assert.Equal(t, tt.wantSkipped, res.Skipped, "a line we could not read is counted, never hidden")
			assert.Len(t, res.Chart.Findings, tt.wantFindings)

			assert.Len(t, res.Records, tt.wantRecords,
				"every record is retained, including the ~19,800 filtered as noise")
			assert.Less(t, res.Elapsed, budget)
		})
	}
}

// TestHealthyCaptureSurfacesNothingSustained is the brief's "no false
// positives on 01-healthy.jsonl" criterion, expressed at the grouping stage.
func TestHealthyCaptureSurfacesNothingSustained(t *testing.T) {
	res := run(t, "01-healthy.jsonl")

	for _, f := range res.Chart.Findings {
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
// ErrImagePull are not Kubernetes event reasons; they appear only inside the
// body of Failed events; so only a body-sensitive rule tells an image-pull
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
func TestEvictionsSplitByFailureMode(t *testing.T) {
	res := run(t, "05-test-b.jsonl")

	var evictions []group.Finding
	for _, f := range res.Chart.Findings {
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

// TestMalformedLineIsSkippedAndDisclosed covers the probe line planted at the
// end of 06, which is commented out and therefore not JSON.
func TestMalformedLineIsSkippedAndDisclosed(t *testing.T) {
	res := run(t, "06-test-c.jsonl")

	assert.Equal(t, 1, res.Skipped, "the header discloses what could not be read")
	assert.Equal(t, 20000, res.Ingested, "the other 20,000 records still arrive")
	assert.Zero(t, res.Unrecognised)

	assert.Contains(t, res.Summarise(), "Skipped: 1")
}

// TestFindingsAreOrderedAndDeterministic guards the trap that Go randomises map
// iteration. 05-test-b has evictions inside the same second, so a sort on
// FirstSeen alone leaves ties broken by map order, and golden tests would then
// fail intermittently, which is the worst way to find this.
func TestFindingsAreOrderedAndDeterministic(t *testing.T) {
	first := run(t, "05-test-b.jsonl").Chart.Findings

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
		assert.Equal(t, first, run(t, "05-test-b.jsonl").Chart.Findings, "run %d diverged", i)
	}
}

// TestForestMatchesTheFixtures pins the causal edge derived for every finding in
// every provided capture.
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
		}},
	}

	label := func(f group.Finding) string { return f.Workload + "/" + f.Reason + "[" + f.Rule + "]" }

	for _, tt := range tests {
		t.Run(tt.fixture, func(t *testing.T) {
			forest := run(t, tt.fixture).Chart.Forest
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
// underlying issue", answered by the tool before the reader has to.
func TestNodePressureIncidentIsOneTree(t *testing.T) {
	forest := run(t, "05-test-b.jsonl").Chart.Forest

	node := -1
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

// TestForestInvariantsHoldOnEveryFixture verifies the structure itself rather
// than any particular edge, and re-derives every edge's claim from the findings
// without consulting the evidence string that asserts it.
func TestForestInvariantsHoldOnEveryFixture(t *testing.T) {
	fixtures := []string{
		"01-healthy.jsonl", "02-memory-leak.jsonl", "03-image-pull-failure.jsonl",
		"04-test-a.jsonl", "05-test-b.jsonl", "06-test-c.jsonl",
	}

	for _, fx := range fixtures {
		t.Run(fx, func(t *testing.T) {
			f := run(t, fx).Chart.Forest

			require.Len(t, f.Edges, len(f.Findings), "one edge per finding")

			t.Run("structure", func(t *testing.T) {
				childCount := make(map[int]int, len(f.Findings))
				for i := range f.Findings {
					for _, c := range f.Children(i) {
						childCount[c]++
					}
				}

				for i, e := range f.Edges {
					if e.Parent == -1 {
						assert.Zero(t, childCount[i], "a root must not also be someone's child")
						continue
					}

					require.Less(t, e.Parent, i, "[%d] parent must be earlier in the slice", i)
					require.GreaterOrEqual(t, e.Parent, 0, "[%d] parent index out of range", i)
					assert.True(t, f.Findings[i].FirstSeen.After(f.Findings[e.Parent].FirstSeen),
						"[%d] a cause must strictly precede its effect", i)
					assert.Equal(t, 1, childCount[i], "[%d] a non-root is exactly one finding's child", i)

					root := f.RootOf(i)
					assert.Equal(t, -1, f.Edges[root].Parent, "RootOf(%d) must land on a root", i)
				}
			})

			t.Run("evidence is true", func(t *testing.T) {
				for i, e := range f.Edges {
					if e.Parent == -1 {
						continue
					}

					parent, child := f.Findings[e.Parent], f.Findings[i]

					switch {
					case strings.HasPrefix(e.Evidence, "same pod "):
						shared := intersect(parent.Pods, child.Pods)
						require.NotEmpty(t, shared, "[%d] claims a shared pod but shares none", i)
						assert.Contains(t, e.Evidence, shared[0], "[%d] names a pod it does not share", i)

					case strings.Contains(e.Evidence, " reported "):
						assert.NotEmpty(t, intersect(parent.Nodes, child.Nodes),
							"[%d] claims a node condition but shares no node", i)

						condition := strings.TrimPrefix(parent.Reason, "NodeHas")
						assert.True(t, anyBodyContains(child, "["+condition+"]"),
							"[%d] claims the record names %q, but no record does", i, condition)

					case strings.HasPrefix(e.Evidence, "rollout created "):
						assert.Equal(t, classify.CategoryDeployMarker, parent.Category,
							"[%d] claims a rollout parent that is not a deploy marker", i)
						require.NotEmpty(t, child.Pods, "[%d] a rollout edge needs pods to check", i)

						for _, pod := range child.Pods {
							assert.True(t, strings.HasPrefix(pod, parent.Workload+"-"),
								"[%d] pod %s does not belong to %s", i, pod, parent.Workload)
						}

					default:
						t.Errorf("[%d] unrecognised evidence shape: %q", i, e.Evidence)
					}
				}
			})
		})
	}
}

func intersect(a, b []string) []string {
	var out []string
	for _, x := range a {
		for _, y := range b {
			if x == y {
				out = append(out, x)
			}
		}
	}
	return out
}

func anyBodyContains(f group.Finding, token string) bool {
	for _, e := range f.Events {
		if strings.Contains(e.Body, token) {
			return true
		}
	}
	return false
}

// TestDiagnosisMatchesTheFixtures is the acceptance test for the diagnose stage.
//
// The two numbers that matter most are at the ends. 01-healthy reports nothing,
// which is the whole point of suppressing anything at all. 05-test-b reports
// seven, because a node condition and the six workloads it evicted are one
// incident and the predicate must not take shape as permission to break it up.
func TestDiagnosisMatchesTheFixtures(t *testing.T) {
	tests := []struct {
		fixture    string
		suppressed int
		want       map[string]string // workload/reason -> pattern
	}{
		{"01-healthy.jsonl", 3, map[string]string{}},

		{"02-memory-leak.jsonl", 3, map[string]string{
			"recommendation-service/OOMKilling": "sustained crash-loop",
			"recommendation-service/BackOff":    "sustained crash-loop",
		}},

		{"03-image-pull-failure.jsonl", 3, map[string]string{
			"payment-service/ScalingReplicaSet": "",
			"payment-service/Failed":            "deploy-correlated failure",
			"payment-service/BackOff":           "deploy-correlated failure",
		}},

		{"04-test-a.jsonl", 3, map[string]string{
			"checkout-service/ScalingReplicaSet": "",
			"checkout-service/Unhealthy":         "deploy-correlated failure",
		}},

		// The condition and all six evictions share one label, so the reader sees
		// one node failure where a flat report shows seven scattered warnings.
		{"05-test-b.jsonl", 3, map[string]string{
			"node-4/NodeHasDiskPressure":   "node issue",
			"batch-reporter/Evicted":       "node issue",
			"data-pipeline/Evicted":        "node issue",
			"metrics-collector/Evicted":    "node issue",
			"auth-service/Evicted":         "node issue",
			"inventory-service/Evicted":    "node issue",
			"notification-service/Evicted": "node issue",
		}},

		// FailedScheduling is truthfully both capacity and deploy-correlated. The
		// mechanism wins, because the trigger is already stated by the causal edge.
		{"06-test-c.jsonl", 3, map[string]string{
			"data-pipeline/ScalingReplicaSet": "",
			"data-pipeline/FailedScheduling":  "capacity issue",
		}},
	}

	for _, tt := range tests {
		t.Run(tt.fixture, func(t *testing.T) {
			c := run(t, tt.fixture).Chart

			reported := c.Reported()
			assert.Equal(t, tt.suppressed, c.SuppressedCount(), "background findings held back")
			assert.Len(t, c.Findings, len(reported)+tt.suppressed, "nothing may be deleted")
			require.Len(t, reported, len(tt.want), "expectation must cover every reported finding")

			for _, i := range reported {
				label := c.Findings[i].Workload + "/" + c.Findings[i].Reason
				want, known := tt.want[label]
				require.True(t, known, "unexpected reported finding %s", label)
				assert.Equal(t, want, c.Diagnoses[i].Pattern.String(), "pattern for %s", label)
			}
		})
	}
}

// TestEveryCaptureHoldsBackTheSameThreeShapes is corroboration. The counts
// above are asserted elsewhere; this asks whether they land on the same shapes.
//
// The brief plants an identical background floor in all six files; one
// eviction for node memory pressure, one failed mount, one three-event readiness
// blip; and a predicate tuned to whichever capture it was written against
// would not land on the same three in the other five. That it does is the
// evidence that D-43's bounds are not fitted to this corpus.
func TestEveryCaptureHoldsBackTheSameThreeShapes(t *testing.T) {
	fixtures := []string{
		"01-healthy.jsonl", "02-memory-leak.jsonl", "03-image-pull-failure.jsonl",
		"04-test-a.jsonl", "05-test-b.jsonl", "06-test-c.jsonl",
	}

	for _, fx := range fixtures {
		t.Run(fx, func(t *testing.T) {
			c := run(t, fx).Chart

			var held []string
			for i, d := range c.Diagnoses {
				if !d.Suppressed() {
					continue
				}

				assert.Equal(t, "transient blip", d.Pattern.String(),
					"anything held back must be labelled as what it is")
				held = append(held, c.Findings[i].Reason)
			}

			assert.ElementsMatch(t, []string{"Evicted", "FailedMount", "Unhealthy"}, held)
		})
	}
}

// TestConfidenceMatchesTheFixtures pins D-34's root classification, which needs
// no threshold: a finding is as explained as whatever sits at the top of its
// incident.
func TestConfidenceMatchesTheFixtures(t *testing.T) {
	tests := []struct {
		fixture, workload, reason, want string
	}{
		// The trail runs off the front of the capture. The OOM kills explain the
		// crash-loop; what drove the memory growth is not here.
		{"02-memory-leak.jsonl", "recommendation-service", "BackOff", "partially explained"},
		{"02-memory-leak.jsonl", "recommendation-service", "OOMKilling", "partially explained"},

		{"03-image-pull-failure.jsonl", "payment-service", "BackOff", "explained"},
		{"04-test-a.jsonl", "checkout-service", "Unhealthy", "explained"},
		{"05-test-b.jsonl", "auth-service", "Evicted", "explained"},
		{"06-test-c.jsonl", "data-pipeline", "FailedScheduling", "explained"},
	}

	for _, tt := range tests {
		t.Run(tt.fixture+"/"+tt.workload+"/"+tt.reason, func(t *testing.T) {
			c := run(t, tt.fixture).Chart

			for i := range c.Findings {
				if c.Findings[i].Workload == tt.workload && c.Findings[i].Reason == tt.reason {
					assert.Equal(t, tt.want, c.Diagnoses[i].Confidence.String())
					return
				}
			}

			t.Fatalf("no finding %s/%s", tt.workload, tt.reason)
		})
	}
}

// TestIncidentsMatchTheFixtures is the acceptance test for the incident view:
// what the report leads with, for every capture.
func TestIncidentsMatchTheFixtures(t *testing.T) {
	tests := []struct {
		fixture      string
		incidents    int
		root         string // workload/reason of what set it off
		mechanism    string // workload/reason of what actually broke
		paged        string // "" when several symptoms tie
		leading      string // substring the mechanism's top signature must contain
		stillFailing bool
	}{
		{
			fixture: "02-memory-leak.jsonl", incidents: 1,
			root: "recommendation-service/OOMKilling", mechanism: "recommendation-service/OOMKilling",
			paged: "recommendation-service/BackOff", leading: "exceeded memory limit (512Mi)",
			stillFailing: true,
		},
		{
			fixture: "03-image-pull-failure.jsonl", incidents: 1,
			root: "payment-service/ScalingReplicaSet", mechanism: "payment-service/Failed",
			paged: "payment-service/BackOff", leading: "v2.14.0-rc3",
			stillFailing: true,
		},
		{
			fixture: "04-test-a.jsonl", incidents: 1,
			root: "checkout-service/ScalingReplicaSet", mechanism: "checkout-service/Unhealthy",
			paged: "checkout-service/Unhealthy", leading: "statuscode: 404",
			stillFailing: true,
		},
		{
			// Six equally-bad evictions: no single symptom paged, and the node
			// condition itself is both the root and the mechanism.
			fixture: "05-test-b.jsonl", incidents: 1,
			root: "node-4/NodeHasDiskPressure", mechanism: "node-4/NodeHasDiskPressure",
			paged: "", leading: "NodeHasDiskPressure",
			stillFailing: false,
		},
		{
			fixture: "06-test-c.jsonl", incidents: 1,
			root: "data-pipeline/ScalingReplicaSet", mechanism: "data-pipeline/FailedScheduling",
			paged: "data-pipeline/FailedScheduling", leading: "Insufficient cpu",
			stillFailing: true,
		},
	}

	label := func(f group.Finding) string { return f.Workload + "/" + f.Reason }

	for _, tt := range tests {
		t.Run(tt.fixture, func(t *testing.T) {
			c := run(t, tt.fixture).Chart

			incidents := c.Incidents()
			require.Len(t, incidents, tt.incidents)

			in := incidents[0]
			assert.Equal(t, tt.root, label(c.Findings[in.Root]), "what set it off")
			assert.Equal(t, tt.mechanism, label(c.Findings[in.Mechanism]), "what actually broke")
			assert.Equal(t, tt.stillFailing, in.StillFailing)

			if tt.paged == "" {
				assert.Equal(t, -1, in.Paged, "several symptoms tie; naming one would be a fabrication")
			} else {
				require.GreaterOrEqual(t, in.Paged, 0)
				assert.Equal(t, tt.paged, label(c.Findings[in.Paged]), "what raised the alert")
			}

			s, ok := c.Diagnoses[in.Mechanism].Leading()
			require.True(t, ok, "the mechanism must have something to say")
			assert.Contains(t, s.Text, tt.leading, "the verdict quotes this line")

			assert.NotEmpty(t, c.Remediation(in), "every recognised failure has a next move")
		})
	}
}

// TestHealthyCaptureHasNoIncidents is the whole point of suppression, stated at
// the level the report actually renders.
func TestHealthyCaptureHasNoIncidents(t *testing.T) {
	assert.Empty(t, run(t, "01-healthy.jsonl").Chart.Incidents())
}

// TestEveryReportedFindingCarriesASignature: the "why" must never be missing
// from a finding the report will print, because the verdict block quotes it.
func TestEveryReportedFindingCarriesASignature(t *testing.T) {
	fixtures := []string{
		"02-memory-leak.jsonl", "03-image-pull-failure.jsonl",
		"04-test-a.jsonl", "05-test-b.jsonl", "06-test-c.jsonl",
	}

	for _, fx := range fixtures {
		t.Run(fx, func(t *testing.T) {
			c := run(t, fx).Chart

			for _, i := range c.Reported() {
				s, ok := c.Diagnoses[i].Leading()
				require.True(t, ok, "%s has no signature", c.Findings[i].Reason)
				assert.NotEmpty(t, s.Text)
				assert.Positive(t, s.Count)
			}
		})
	}
}

// TestCadenceMatchesTheFixtures pins the rhythm of every repeating finding.
//
// These are per-object medians, and the distinction matters: 04's finding-wide
// interval is 1.4s because five pods are probed independently, where 10.5s is
// the probe period configured on the deployment.
func TestCadenceMatchesTheFixtures(t *testing.T) {
	tests := []struct {
		fixture, workload, reason string
		objects, samples          int
		median                    time.Duration
		trend                     string
	}{
		// The sentence that makes a memory leak legible: one kill per pod every
		// four minutes. Reported steady; the means do contract, 4m43s to
		// 3m43s, and that is below the threshold this corpus supports.
		{"02-memory-leak.jsonl", "recommendation-service", "OOMKilling", 4, 22, 251240 * time.Millisecond, "steady"},
		{"02-memory-leak.jsonl", "recommendation-service", "BackOff", 4, 87, 10800 * time.Millisecond, "steady"},

		// Kubernetes' exponential backoff, unmistakable at 5x and 11x.
		{"03-image-pull-failure.jsonl", "payment-service", "Failed", 3, 21, 40538 * time.Millisecond, "easing"},
		{"03-image-pull-failure.jsonl", "payment-service", "BackOff", 3, 15, 80428 * time.Millisecond, "easing"},

		// Fixed periods: a readiness probe and the scheduler's retry interval.
		{"04-test-a.jsonl", "checkout-service", "Unhealthy", 5, 217, 10472 * time.Millisecond, "steady"},
		{"06-test-c.jsonl", "data-pipeline", "FailedScheduling", 6, 171, 20372 * time.Millisecond, "steady"},
	}

	for _, tt := range tests {
		t.Run(tt.fixture+"/"+tt.reason, func(t *testing.T) {
			c := run(t, tt.fixture).Chart

			for i := range c.Findings {
				if c.Findings[i].Workload != tt.workload || c.Findings[i].Reason != tt.reason {
					continue
				}

				cd := c.Diagnoses[i].Cadence
				require.True(t, cd.Known())

				assert.Equal(t, tt.objects, cd.Objects)
				assert.Equal(t, tt.samples, cd.Samples)
				assert.Equal(t, tt.trend, cd.Trend.String())
				assert.InDelta(t, tt.median, cd.Median, float64(50*time.Millisecond),
					"median cadence %s", cd.Median)

				return
			}

			t.Fatalf("no finding %s/%s", tt.workload, tt.reason)
		})
	}
}

// TestBackgroundFindingsHaveNoCadence: three occurrences over twelve seconds is
// not a rhythm, and reporting one would dress noise as a measurement.
func TestBackgroundFindingsHaveNoCadence(t *testing.T) {
	c := run(t, "01-healthy.jsonl").Chart

	for i := range c.Findings {
		assert.False(t, c.Diagnoses[i].Cadence.Known(),
			"%s has too few intervals to measure", c.Findings[i].Reason)
	}
}

// TestProbeSignaturesCarryDistinctReadings covers D-57 end to end. 04's three
// probe outcomes prove three different things, and the finding-level meaning
// cannot be right for all of them.
func TestProbeSignaturesCarryDistinctReadings(t *testing.T) {
	c := run(t, "04-test-a.jsonl").Chart

	var got map[string]string

	for i := range c.Findings {
		if c.Findings[i].Workload != "checkout-service" || c.Findings[i].Reason != "Unhealthy" {
			continue
		}

		got = map[string]string{}
		for _, s := range c.Diagnoses[i].Signatures {
			got[s.Text] = s.Means
		}
	}

	require.Len(t, got, 3, "404, connection refused, and timeout")

	var status, refused, timeout string
	for text, means := range got {
		switch {
		case strings.Contains(text, "statuscode"):
			status = means
		case strings.Contains(text, "connection refused"):
			refused = means
		case strings.Contains(text, "deadline exceeded"):
			timeout = means
		}
	}

	assert.Contains(t, status, "the server answered")
	assert.Contains(t, refused, "nothing was listening")
	assert.Contains(t, timeout, "not refused")

	assert.NotEqual(t, status, refused, "these prove opposite things about the process")
}
