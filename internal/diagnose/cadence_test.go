package diagnose_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/troglodytto/daiquiri/internal/diagnose"
	"github.com/troglodytto/daiquiri/internal/event"
	"github.com/troglodytto/daiquiri/internal/group"
)

// beats builds a finding whose pods each fire on the given offsets.
func beats(pods int, offsets ...time.Duration) group.Finding {
	f := issue("checkout-service", "unhealthy/readiness", 0, pods, 0)

	for p := 0; p < pods; p++ {
		// Pods are staggered so their series interleave, which is what makes a
		// finding-wide interval meaningless.
		stagger := time.Duration(p) * 2 * time.Second

		for i, off := range offsets {
			f.Events = append(f.Events, event.Event{
				Timestamp: start.Add(stagger + off),
				Object:    event.Object{Kind: event.KindPod, Name: fmt.Sprintf("pod-%d", p)},
				EventUID:  fmt.Sprintf("p%d-%d", p, i),
				Count:     1,
			})
		}
	}

	f.Count = len(f.Events)
	f.LastSeen = f.Events[len(f.Events)-1].Timestamp

	return f
}

func cadenceOf(t *testing.T, f group.Finding) diagnose.Cadence {
	t.Helper()

	return diagnose.Build(forest([]group.Finding{f}, root), start.Add(time.Hour)).Diagnoses[0].Cadence
}

// TestCadenceIsMeasuredPerObject is the whole reason this is not a one-liner.
//
// Five pods probed independently on a 10s period produce a finding-wide gap of
// about 2s, which is an artefact of how many replicas happened to be failing.
// 10s is a fact about the deployment.
func TestCadenceIsMeasuredPerObject(t *testing.T) {
	var offsets []time.Duration
	for i := 0; i < 10; i++ {
		offsets = append(offsets, time.Duration(i)*10*time.Second)
	}

	c := cadenceOf(t, beats(5, offsets...))

	require.True(t, c.Known())
	assert.Equal(t, 5, c.Objects)
	assert.Equal(t, 45, c.Samples, "nine intervals per pod")
	assert.Equal(t, 10*time.Second, c.Median,
		"the probe period, not the rate at which the fleet as a whole complains")
}

// TestCadenceNeedsEnoughIntervals -- below the minimum the answer would be
// arithmetic on noise, so there is no answer.
func TestCadenceNeedsEnoughIntervals(t *testing.T) {
	c := cadenceOf(t, beats(1, 0, 10*time.Second, 20*time.Second))

	assert.False(t, c.Known())
	assert.Zero(t, c.Median)
	assert.Equal(t, diagnose.TrendUnknown, c.Trend)
	assert.Empty(t, c.Trend.String())
}

// TestTrendClassification pins D-58's thresholds, including the deliberate
// refusal to call a mild movement.
func TestTrendClassification(t *testing.T) {
	// steady: a fixed 10s period.
	steady := make([]time.Duration, 0, 12)
	for i := 0; i < 12; i++ {
		steady = append(steady, time.Duration(i)*10*time.Second)
	}

	// easing: intervals doubling, as Kubernetes backs off.
	easing := []time.Duration{0}
	for gap, at := 10*time.Second, 10*time.Second; len(easing) < 12; gap *= 2 {
		easing = append(easing, at)
		at += gap
	}

	// mild: 02-memory-leak's shape -- intervals contracting by about a fifth,
	// which is visible and below the threshold.
	mild := []time.Duration{0}
	for i, at := 0, time.Duration(0); i < 11; i++ {
		gap := 300 * time.Second
		if i >= 5 {
			gap = 240 * time.Second
		}
		at += gap
		mild = append(mild, at)
	}

	tests := []struct {
		name    string
		offsets []time.Duration
		want    diagnose.Trend
	}{
		{"a fixed period is steady", steady, diagnose.TrendSteady},
		{"doubling intervals are easing", easing, diagnose.TrendEasing},
		{"a fifth faster is not a claim worth making", mild, diagnose.TrendSteady},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := cadenceOf(t, beats(1, tt.offsets...))

			require.True(t, c.Known())
			assert.Equal(t, tt.want, c.Trend)
		})
	}
}

// TestCadencePublishesTheHalvesItJudged: the classification is a threshold
// applied to two numbers, and both are published so a reader can disagree with
// the threshold rather than with the tool.
func TestCadencePublishesTheHalvesItJudged(t *testing.T) {
	mild := []time.Duration{0}
	for i, at := 0, time.Duration(0); i < 11; i++ {
		gap := 300 * time.Second
		if i >= 5 {
			gap = 240 * time.Second
		}
		at += gap
		mild = append(mild, at)
	}

	c := cadenceOf(t, beats(1, mild...))

	assert.Equal(t, diagnose.TrendSteady, c.Trend)
	assert.Greater(t, c.Early, c.Late, "the contraction is visible in the published numbers")
	assert.Positive(t, c.Shortest)
	assert.GreaterOrEqual(t, c.Longest, c.Median)
}

// TestUnitIsPodsOnlyForPodFindings -- "per node" beside a node condition would
// imply a comparison across nodes that was never made.
func TestUnitIsPodsOnlyForPodFindings(t *testing.T) {
	pods := beats(2, 0, 10*time.Second, 20*time.Second, 30*time.Second)
	assert.Equal(t, "per pod", cadenceOf(t, pods).Unit(pods))

	node := pods
	node.Kind = event.KindNode
	assert.Empty(t, cadenceOf(t, node).Unit(node))
}
