package diagnose_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/troglodytto/daiquiri/internal/diagnose"
	"github.com/troglodytto/daiquiri/internal/event"
	"github.com/troglodytto/daiquiri/internal/group"
)

// withBodies attaches record bodies to a finding, one event each.
func withBodies(f group.Finding, bodies ...string) group.Finding {
	f.Count = len(bodies)
	f.Events = nil

	for i, b := range bodies {
		f.Events = append(f.Events, event.Event{
			Timestamp: start.Add(time.Duration(i) * time.Second),
			Body:      b,
		})
	}

	return f
}

func signatures(t *testing.T, f group.Finding) []diagnose.Signature {
	t.Helper()

	c := diagnose.Build(forest([]group.Finding{f}, root), start.Add(time.Hour))

	return c.Diagnoses[0].Signatures
}

// TestSignaturesCollapseVolatileTokens covers D-48. 222 readiness failures
// differ only in which replica was probed, and quoting one of them verbatim
// would present an accident of scheduling as a fact about the failure.
func TestSignaturesCollapseVolatileTokens(t *testing.T) {
	f := withBodies(issue("checkout-service", "unhealthy/readiness", 0, 3, 0),
		`Readiness probe failed: Get "http://10.244.11.243:8080/healthcheck": connection refused`,
		`Readiness probe failed: Get "http://10.244.12.148:8080/healthcheck": connection refused`,
		`Readiness probe failed: Get "http://10.244.1.25:8080/healthcheck": connection refused`,
	)

	got := signatures(t, f)

	require.Len(t, got, 1, "three replicas, one failure")
	assert.Equal(t, 3, got[0].Count)
	assert.Contains(t, got[0].Text, "<ip>")
	assert.Contains(t, got[0].Text, "8080",
		"the port says which service was probed and must survive; only the address is volatile")
}

// TestSignaturesKeepNumbersWithUnits is the normaliser's most important
// restraint. 512Mi is the answer.
func TestSignaturesKeepNumbersWithUnits(t *testing.T) {
	f := withBodies(issue("recommendation-service", "oom-killed", 0, 1, 0),
		"Container recommendation in pod recommendation-service-5a8e2c1f4-b35ff exceeded memory limit (512Mi)",
		"Container recommendation in pod recommendation-service-5a8e2c1f4-e0556 exceeded memory limit (512Mi)",
	)

	got := signatures(t, f)

	require.Len(t, got, 1, "the pod name is volatile, the limit is not")
	assert.Equal(t, "Container recommendation in pod <pod> exceeded memory limit (512Mi)", got[0].Text)
	assert.True(t, got[0].Specific)
}

// TestUIDsAreStrippedButAmountsAreNot guards the one regex that could eat a
// number: a parenthesised object UID is hex and hyphens, and "(512Mi)" is not.
func TestUIDsAreStrippedButAmountsAreNot(t *testing.T) {
	f := withBodies(issue("recommendation-service", "backoff/crash-loop", 0, 1, 0),
		"Back-off restarting failed container recommendation in pod recommendation-service-5a8e2c1f4-b35ff_production(cb8c2e08-ab05-c6b4-3f6f-f2403d6e7b98)",
		"Back-off restarting failed container recommendation in pod recommendation-service-5a8e2c1f4-e0556_production(d6827f19-03ef-64bc-66a8-211ef259caa4)",
	)

	got := signatures(t, f)

	require.Len(t, got, 1, "two pods, two UIDs, one failure")
	assert.NotContains(t, got[0].Text, "cb8c2e08")
	assert.False(t, got[0].Specific,
		"nothing concrete survives -- this line only restates the reason")
}

// TestSpecificSignatureOutranksAFrequentOne is D-49, and 03-image-pull-failure
// is the case that forced it: the rarest line is the only one that says
// anything, and leading with the common one buries the answer under a
// paraphrase of the question.
func TestSpecificSignatureOutranksAFrequentOne(t *testing.T) {
	bodies := make([]string, 0, 24)
	for i := 0; i < 18; i++ {
		bodies = append(bodies, "Error: ImagePullBackOff")
	}
	for i := 0; i < 3; i++ {
		bodies = append(bodies, "Error: ErrImagePull")
	}
	for i := 0; i < 3; i++ {
		bodies = append(bodies, `Failed to pull image "registry.internal/payment-service:v2.14.0-rc3": manifest not found`)
	}

	got := signatures(t, withBodies(issue("payment-service", "failed/image-pull", 0, 3, 0), bodies...))

	require.Len(t, got, 3)
	assert.Contains(t, got[0].Text, "v2.14.0-rc3", "the tag that does not exist leads, at 3 of 24")
	assert.Equal(t, 3, got[0].Count)
	assert.True(t, got[0].Specific)

	assert.Equal(t, "Error: ImagePullBackOff", got[1].Text, "generic, and most frequent among generics")
	assert.Equal(t, 18, got[1].Count)
	assert.False(t, got[1].Specific)
}

// TestSpecificityIsQuotedStringOrSurvivingDigit pins the predicate itself,
// since every ranking decision rests on it.
func TestSpecificityIsQuotedStringOrSurvivingDigit(t *testing.T) {
	tests := []struct {
		body string
		want bool
	}{
		{"Readiness probe failed: HTTP probe failed with statuscode: 404", true},
		{`Failed to pull image "registry.internal/payment-service:v2.14.0-rc3"`, true},
		{"0/6 nodes are available: 6 Insufficient cpu", true},
		{"Container recommendation exceeded memory limit (512Mi)", true},
		{`MountVolume.SetUp failed for volume "config" : configmap "x-config" not found`, true},

		{"Error: ImagePullBackOff", false},
		{"Error: ErrImagePull", false},
		{"The node had condition: [DiskPressure].", false},
		{"Back-off restarting failed container", false},
	}

	for _, tt := range tests {
		t.Run(tt.body, func(t *testing.T) {
			got := signatures(t, withBodies(issue("svc", "failed-mount", 0, 1, 0), tt.body))

			require.Len(t, got, 1)
			assert.Equal(t, tt.want, got[0].Specific)
		})
	}
}

// TestSignatureOrderIsTotal guards the trap that sank the finding sort: Go
// randomises map iteration, and signatures are counted in a map.
func TestSignatureOrderIsTotal(t *testing.T) {
	f := withBodies(issue("svc", "failed-mount", 0, 1, 0),
		"Error: Alpha", "Error: Bravo", "Error: Charlie", "Error: Delta",
	)

	want := signatures(t, f)
	require.Len(t, want, 4, "four distinct generic signatures, all tied on count")

	for i := 0; i < 50; i++ {
		assert.Equal(t, want, signatures(t, f), "run %d diverged", i)
	}
}

// TestFindingWithNoRecordsHasNoSignatures covers the synthetic findings the
// other tests build, which carry no events at all.
func TestFindingWithNoRecordsHasNoSignatures(t *testing.T) {
	got := signatures(t, issue("svc", "failed-mount", 1, 1, 0))

	assert.Empty(t, got)

	_, ok := diagnose.Diagnosis{}.Leading()
	assert.False(t, ok)
}

// TestSignatureReadings covers D-57: one reason carrying situations that mean
// opposite things, where the finding-level meaning cannot be right for both.
func TestSignatureReadings(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			"a 404 proves the server answered",
			"Readiness probe failed: HTTP probe failed with statuscode: 404",
			"the server answered the probe -- with a status saying this path is not what it wants",
		},
		{
			"refused proves nothing was listening",
			`Readiness probe failed: Get "http://10.0.0.1:8080/healthz": dial tcp 10.0.0.1:8080: connect: connection refused`,
			"nothing was listening on that port when the probe fired",
		},
		{
			"a timeout proves neither",
			`Readiness probe failed: Get "http://10.0.0.1:8080/healthz": context deadline exceeded`,
			"the connection was not refused, and no answer arrived inside the probe's timeout",
		},
		{
			"the scheduler names which resource ran out",
			"0/6 nodes are available: 6 Insufficient cpu.",
			"no node had enough spare CPU for these pods",
		},
		{
			"and says so when it is both",
			"0/6 nodes are available: 3 Insufficient cpu, 3 Insufficient memory.",
			"no node had enough of either CPU or memory for these pods",
		},

		// These carry no reading at all. The rule-level meaning already covers
		// them, and a body matcher nobody has seen fire is a guess with a
		// rule's authority.
		{"an image pull carries no extra reading", `Failed to pull image "registry/x:v1": not found`, ""},
		{"nor does an OOM kill", "Container x in pod <pod> exceeded memory limit (512Mi)", ""},
		{"nor an HTTP 503, which no capture contains", "Readiness probe failed: HTTP probe failed with statuscode: 503", "the server answered the probe -- with a status saying this path is not what it wants"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := signatures(t, withBodies(issue("svc", "unhealthy/readiness", 0, 1, 0), tt.body))

			require.Len(t, got, 1)
			assert.Equal(t, tt.want, got[0].Means)
		})
	}
}

// TestReadingsAreOrderedNarrowestFirst: "Insufficient cpu" is a substring of the
// both-resources body, so a table ordered the other way would report a CPU
// shortage on a cluster short of memory too.
func TestReadingsAreOrderedNarrowestFirst(t *testing.T) {
	both := signatures(t, withBodies(issue("svc", "failed-scheduling", 0, 1, 0),
		"0/6 nodes are available: 3 Insufficient cpu, 3 Insufficient memory."))

	require.Len(t, both, 1)
	assert.Contains(t, both[0].Means, "either CPU or memory")
	assert.NotContains(t, both[0].Means, "spare CPU for these pods")
}
