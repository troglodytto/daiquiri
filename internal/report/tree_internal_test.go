package report

// The rest of this package is tested from outside, through the rendered output.
// These two are reached directly because they are pure functions encoding a
// stated rule about printed precision, and asserting that rule through a full
// render would test the fixture that produced the durations rather than the
// rule itself.

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/troglodytto/daiquiri/internal/diagnose"
	"github.com/troglodytto/daiquiri/internal/event"
	"github.com/troglodytto/daiquiri/internal/group"
)

// TestRoughlyPrintsOnlyThePrecisionAMedianSupports pins the documented rule:
// tenths below a minute, whole seconds at or above it. "4m11.2s" implies a
// measurement two decimal places finer than a median over 22 samples supports.
func TestRoughlyPrintsOnlyThePrecisionAMedianSupports(t *testing.T) {
	tests := []struct {
		name string
		d    time.Duration
		want string
	}{
		{"zero", 0, "0s"},
		{"sub-second rounds to a tenth", 1234 * time.Millisecond, "1.2s"},
		{"sub-second rounds up at the half", 1260 * time.Millisecond, "1.3s"},
		{"tenths survive just below the minute", 59940 * time.Millisecond, "59.9s"},
		{"the minute boundary switches to whole seconds", time.Minute, "1m0s"},
		{"above a minute drops the fraction", 4*time.Minute + 11200*time.Millisecond, "4m11s"},
		{"above a minute rounds to the nearest second", 2*time.Minute + 1600*time.Millisecond, "2m2s"},
		{"hours keep whole seconds", time.Hour + 30*time.Second, "1h0m30s"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, roughly(tt.d))
		})
	}
}

// TestRhythmNamesTheTrendOnlyWhenOneWasMeasured covers all four trend arms and
// the unmeasured case. A cadence below the sample floor renders as nothing at
// all rather than as a rhythm nobody measured.
func TestRhythmNamesTheTrendOnlyWhenOneWasMeasured(t *testing.T) {
	pod := group.Finding{Kind: event.KindPod}
	node := group.Finding{Kind: event.KindNode}

	// Samples clears the floor at which a cadence is considered known.
	const known = 10

	tests := []struct {
		name    string
		cadence diagnose.Cadence
		finding group.Finding
		want    string
	}{
		{
			name:    "too few intervals renders nothing",
			cadence: diagnose.Cadence{Samples: 1, Median: 30 * time.Second},
			finding: pod,
			want:    "",
		},
		{
			name:    "a zero cadence renders nothing",
			cadence: diagnose.Cadence{},
			finding: pod,
			want:    "",
		},
		{
			name:    "a steady rhythm on pods is qualified per pod",
			cadence: diagnose.Cadence{Samples: known, Objects: 3, Median: 10500 * time.Millisecond, Trend: diagnose.TrendSteady},
			finding: pod,
			want:    "every ~10.5s per pod",
		},
		{
			name:    "a node finding carries no per-pod qualifier",
			cadence: diagnose.Cadence{Samples: known, Objects: 3, Median: 2 * time.Minute, Trend: diagnose.TrendSteady},
			finding: node,
			want:    "every ~2m0s",
		},
		{
			name:    "a pod finding with no objects carries no qualifier",
			cadence: diagnose.Cadence{Samples: known, Objects: 0, Median: 45 * time.Second, Trend: diagnose.TrendSteady},
			finding: pod,
			want:    "every ~45s",
		},
		{
			name: "easing publishes the movement it was decided by",
			cadence: diagnose.Cadence{
				Samples: known, Objects: 2, Median: time.Minute,
				Early: 30 * time.Second, Late: 90 * time.Second,
				Trend: diagnose.TrendEasing,
			},
			finding: pod,
			want:    "every ~1m0s per pod, easing 30s → 1m30s",
		},
		{
			name: "tightening publishes the movement it was decided by",
			cadence: diagnose.Cadence{
				Samples: known, Objects: 2, Median: 4*time.Minute + 26*time.Second,
				Early: 4*time.Minute + 41*time.Second, Late: 4*time.Minute + 12*time.Second,
				Trend: diagnose.TrendTightening,
			},
			finding: pod,
			want:    "every ~4m26s per pod, tightening 4m41s → 4m12s",
		},
		{
			name: "an unnamed trend states the rhythm and stops",
			cadence: diagnose.Cadence{
				Samples: known, Objects: 2, Median: 12 * time.Second,
				Early: 11 * time.Second, Late: 13 * time.Second,
				Trend: diagnose.TrendUnknown,
			},
			finding: pod,
			want:    "every ~12s per pod",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, rhythm(tt.cadence, tt.finding))
		})
	}
}
