package classify_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/troglodytto/daiquiri/internal/classify"
	"github.com/troglodytto/daiquiri/internal/event"
)

// TestClassifySpecTable walks the severity table from the challenge brief, one
// case per row. This is the spec-fidelity test: if a change makes it fail, the
// change is wrong until the brief says otherwise.
func TestClassifySpecTable(t *testing.T) {
	tests := []struct {
		name         string
		reason       string
		severity     event.Severity
		body         string
		wantCategory classify.Category
		wantSeverity event.Severity
	}{
		{
			name:   "BackOff at Warning is a crash-loop and critical",
			reason: "BackOff", severity: event.SeverityWarning,
			body:         "Back-off restarting failed container recommendation in pod recommendation-service-5a8e2c1f4-e0556_production(d6827f19)",
			wantCategory: classify.CategoryIssue, wantSeverity: event.SeverityCritical,
		},
		{
			name:   "BackOff at Normal is image-pull retry and still critical",
			reason: "BackOff", severity: event.SeverityInfo,
			body:         `Back-off pulling image "registry.internal/payment-service:v2.14.0-rc3"`,
			wantCategory: classify.CategoryIssue, wantSeverity: event.SeverityCritical,
		},
		{
			name:   "Failed carrying an image-pull error is critical",
			reason: "Failed", severity: event.SeverityWarning,
			body:         `Failed to pull image "registry.internal/payment-service:v2.14.0-rc3": rpc error: code = NotFound`,
			wantCategory: classify.CategoryIssue, wantSeverity: event.SeverityCritical,
		},
		{
			name:   "Failed for any other reason is critical",
			reason: "Failed", severity: event.SeverityWarning,
			body:         "Error: cannot create container",
			wantCategory: classify.CategoryIssue, wantSeverity: event.SeverityCritical,
		},
		{
			name:   "FailedCreatePodSandBox is critical",
			reason: "FailedCreatePodSandBox", severity: event.SeverityWarning,
			body:         "Failed to create pod sandbox",
			wantCategory: classify.CategoryIssue, wantSeverity: event.SeverityCritical,
		},
		{
			name:   "OOMKilling is critical",
			reason: "OOMKilling", severity: event.SeverityWarning,
			body:         "Container recommendation in pod recommendation-service-5a8e2c1f4-ea1b0 exceeded memory limit (512Mi)",
			wantCategory: classify.CategoryIssue, wantSeverity: event.SeverityCritical,
		},
		{
			name:   "NodeNotReady is critical",
			reason: "NodeNotReady", severity: event.SeverityWarning,
			body:         "Node node-2 status is now: NodeNotReady",
			wantCategory: classify.CategoryIssue, wantSeverity: event.SeverityCritical,
		},
		{
			name:   "NodeHasDiskPressure is critical",
			reason: "NodeHasDiskPressure", severity: event.SeverityWarning,
			body:         "Node node-4 status is now: NodeHasDiskPressure",
			wantCategory: classify.CategoryIssue, wantSeverity: event.SeverityCritical,
		},
		{
			name:   "FailedScheduling is a warning",
			reason: "FailedScheduling", severity: event.SeverityWarning,
			body:         "0/6 nodes are available: 6 Insufficient cpu.",
			wantCategory: classify.CategoryIssue, wantSeverity: event.SeverityWarning,
		},
		{
			name:   "Unhealthy is a warning",
			reason: "Unhealthy", severity: event.SeverityWarning,
			body:         `Readiness probe failed: Get "http://10.244.13.168:8080/readyz": context deadline exceeded`,
			wantCategory: classify.CategoryIssue, wantSeverity: event.SeverityWarning,
		},
		{
			name:   "FailedMount is a warning",
			reason: "FailedMount", severity: event.SeverityWarning,
			body:         `MountVolume.SetUp failed for volume "config" : configmap "api-gateway-config" not found`,
			wantCategory: classify.CategoryIssue, wantSeverity: event.SeverityWarning,
		},
		{
			name:   "Evicted is a warning",
			reason: "Evicted", severity: event.SeverityWarning,
			body:         "The node had condition: [DiskPressure]. ",
			wantCategory: classify.CategoryIssue, wantSeverity: event.SeverityWarning,
		},
	}

	c := classify.New()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := c.Classify(event.Event{Reason: tt.reason, Severity: tt.severity, Body: tt.body})

			assert.Equal(t, tt.wantCategory, got.Category)
			assert.Equal(t, tt.wantSeverity, got.Severity)
			assert.NotEmpty(t, got.Cause, "every reportable issue must carry an interpretation, not just a count")
			assert.True(t, got.Recognised, "this reason is in the spec table and must not hit the fallback")
		})
	}
}

// TestClassifyNoiseReasons covers the brief's ignore list plus the documented
// Pulling extension.
func TestClassifyNoiseReasons(t *testing.T) {
	reasons := []string{
		"Pulled", "Created", "Started", "Scheduled", "Killing",
		"SuccessfulCreate", "SuccessfulDelete",
		"Pulling", // extension: counterpart to Pulled, 17,175 records in the corpus
	}

	c := classify.New()
	for _, reason := range reasons {
		t.Run(reason, func(t *testing.T) {
			got := c.Classify(event.Event{Reason: reason, Severity: event.SeverityInfo})

			assert.Equal(t, classify.CategoryNoise, got.Category)
			assert.True(t, got.Recognised)
		})
	}
}

// TestClassifyBackOffIsSeveritySensitive pins the single easiest thing to get
// wrong: the same reason means two different failures depending on severity,
// and neither is noise.
func TestClassifyBackOffIsSeveritySensitive(t *testing.T) {
	c := classify.New()

	crashLoop := c.Classify(event.Event{
		Reason: "BackOff", Severity: event.SeverityWarning,
		Body: "Back-off restarting failed container checkout in pod checkout-abc_production(uid)",
	})
	imagePull := c.Classify(event.Event{
		Reason: "BackOff", Severity: event.SeverityInfo,
		Body: `Back-off pulling image "registry.internal/payment-service:v2.14.0-rc3"`,
	})

	require.Equal(t, classify.CategoryIssue, crashLoop.Category)
	require.Equal(t, classify.CategoryIssue, imagePull.Category,
		"a Normal-severity BackOff is an image-pull retry, not lifecycle noise")
	assert.NotEqual(t, crashLoop.Cause, imagePull.Cause,
		"the two must be diagnosed differently or the report misleads on-call")
	assert.Contains(t, crashLoop.Cause, "crash")
	assert.Contains(t, imagePull.Cause, "image")
}

// TestClassifyFailedDistinguishesImagePull uses the three body shapes that
// actually occur in the corpus. ImagePullBackOff and ErrImagePull are not event
// reasons -- they appear only in the body of Failed events.
func TestClassifyFailedDistinguishesImagePull(t *testing.T) {
	imagePullBodies := []string{
		`Failed to pull image "registry.internal/payment-service:v2.14.0-rc3": rpc error: code = NotFound desc = manifest not found`,
		"Error: ErrImagePull",
		"Error: ImagePullBackOff",
	}

	c := classify.New()
	for _, body := range imagePullBodies {
		t.Run(body[:min(len(body), 40)], func(t *testing.T) {
			got := c.Classify(event.Event{Reason: "Failed", Severity: event.SeverityWarning, Body: body})

			assert.Equal(t, classify.CategoryIssue, got.Category)
			assert.Contains(t, got.Cause, "image", "image-pull failures must be diagnosed as such")
		})
	}

	t.Run("non-image-pull Failed is diagnosed differently", func(t *testing.T) {
		got := c.Classify(event.Event{
			Reason: "Failed", Severity: event.SeverityWarning,
			Body: "Error: cannot create container: permission denied",
		})

		assert.Equal(t, classify.CategoryIssue, got.Category)
		assert.NotContains(t, got.Cause, "image pull")
	})
}

func TestClassifyScalingReplicaSetIsDeployMarker(t *testing.T) {
	c := classify.New()

	got := c.Classify(event.Event{
		Reason: "ScalingReplicaSet", Severity: event.SeverityInfo,
		Body: "Scaled up replica set payment-service-9e3f1a2b8 to 3",
	})

	assert.Equal(t, classify.CategoryDeployMarker, got.Category,
		"deploy markers are retained for correlation, not discarded as noise")
	assert.True(t, got.Recognised)
}

func TestClassifyUnknownReasons(t *testing.T) {
	tests := []struct {
		name         string
		reason       string
		severity     event.Severity
		wantCategory classify.Category
	}{
		{"unknown warning is surfaced conservatively", "SomeNewReason", event.SeverityWarning, classify.CategoryIssue},
		{"unknown normal is treated as lifecycle", "SomeNewReason", event.SeverityInfo, classify.CategoryNoise},
		{"empty reason at warning is surfaced", "", event.SeverityWarning, classify.CategoryIssue},
		{"empty reason at normal is noise", "", event.SeverityInfo, classify.CategoryNoise},
	}

	c := classify.New()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := c.Classify(event.Event{Reason: tt.reason, Severity: tt.severity})

			assert.Equal(t, tt.wantCategory, got.Category)
			assert.False(t, got.Recognised, "an unmatched reason must be disclosed, not silently bucketed")
		})
	}
}

func TestClassifyZeroEventIsNoise(t *testing.T) {
	got := classify.New().Classify(event.Event{})

	assert.Equal(t, classify.CategoryNoise, got.Category)
}

func TestClassifyBodySensitiveReasonsTolerateEmptyBodies(t *testing.T) {
	reasons := []string{"Failed", "Unhealthy", "Evicted", "BackOff"}

	c := classify.New()
	for _, reason := range reasons {
		t.Run(reason, func(t *testing.T) {
			got := c.Classify(event.Event{Reason: reason, Severity: event.SeverityWarning})

			assert.Equal(t, classify.CategoryIssue, got.Category, "an empty body must not drop the event")
			assert.NotEmpty(t, got.Cause)
		})
	}
}

func TestCategoryString(t *testing.T) {
	assert.Equal(t, "NOISE", classify.CategoryNoise.String())
	assert.Equal(t, "DEPLOY", classify.CategoryDeployMarker.String())
	assert.Equal(t, "ISSUE", classify.CategoryIssue.String())
	assert.Equal(t, "UNKNOWN", classify.Category(99).String())
}
