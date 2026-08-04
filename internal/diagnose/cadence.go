package diagnose

import (
	"sort"
	"time"

	"github.com/troglodytto/daiquiri/internal/event"
	"github.com/troglodytto/daiquiri/internal/group"
)

// Sample minimums. Below these the answer would be arithmetic on noise, and a
// finding under the minimum reports nothing.
const (
	minCadenceSamples = 6
	minTrendSamples   = 8
)

// trendRatio is how far the late half must move from the early half before the
// rhythm gets called anything but steady. Symmetric: easing at >= 3, tightening
// at <= 1/3.
//
// Too blunt to fire on 02, whose OOM intervals move 0.79 across 22 samples that
// already range from 169s to 326s. The corpus doesn't support calling that
// acceleration, so the tool doesn't, and both means are published. See D-58.
const trendRatio = 3.0

// Trend is which way a finding's rhythm is moving.
type Trend uint8

// Trends. The zero value is Unknown, which is what too few samples produce.
const (
	// TrendUnknown means fewer than minTrendSamples intervals were available.
	TrendUnknown Trend = iota
	// TrendSteady is a fixed rhythm: a probe period, a scheduler retry.
	TrendSteady
	// TrendEasing is intervals growing; Kubernetes backing off. It tells an
	// on-call engineer the gaps will keep widening whatever they do until the
	// underlying fault is fixed.
	TrendEasing
	// TrendTightening is intervals shrinking: the failure is arriving faster
	// than it was.
	TrendTightening
)

// String returns the label used in rendered output.
func (t Trend) String() string {
	switch t {
	case TrendSteady:
		return "steady"

	case TrendEasing:
		return "easing"

	case TrendTightening:
		return "tightening"

	default:
		return ""
	}
}

// Cadence is the rhythm of a repeating finding.
type Cadence struct {
	// Samples is how many intervals were measured, and Objects how many
	// distinct pods or nodes contributed them.
	Samples int
	Objects int

	Median   time.Duration
	Shortest time.Duration
	Longest  time.Duration

	// Early and Late are the mean interval over the first and second halves of
	// the measured intervals, ordered by time. Published rather than reduced to
	// Trend, so a reader can see a movement the threshold declined to name.
	Early time.Duration
	Late  time.Duration

	Trend Trend
}

// Known reports whether enough intervals existed to measure anything.
func (c Cadence) Known() bool { return c.Samples >= minCadenceSamples }

// cadenceOf measures the rhythm of a finding.
//
// Intervals are taken per object and then pooled. Interleaving otherwise
// destroys the number: 04's five pods are probed independently, so the
// finding-wide gap reads 1.4s where the probe period is 10.5s.
func cadenceOf(f group.Finding) Cadence {
	byObject := make(map[string][]time.Time, len(f.Events))
	for _, e := range f.Events {
		key := e.Object.Name
		if key == "" {
			key = f.Workload
		}

		byObject[key] = append(byObject[key], e.Timestamp)
	}

	// Intervals keep their start time so the halves can be split by when they
	// happened.
	type interval struct {
		at  time.Time
		gap time.Duration
	}

	var intervals []interval

	for _, stamps := range byObject {
		sort.Slice(stamps, func(i, j int) bool { return stamps[i].Before(stamps[j]) })

		for i := 1; i < len(stamps); i++ {
			intervals = append(intervals, interval{at: stamps[i-1], gap: stamps[i].Sub(stamps[i-1])})
		}
	}

	c := Cadence{Samples: len(intervals), Objects: len(byObject)}
	if !c.Known() {
		return Cadence{Samples: len(intervals), Objects: len(byObject)}
	}

	sort.Slice(intervals, func(i, j int) bool { return intervals[i].at.Before(intervals[j].at) })

	gaps := make([]time.Duration, len(intervals))
	for i, in := range intervals {
		gaps[i] = in.gap
	}

	c.Early = mean(gaps[:len(gaps)/2])
	c.Late = mean(gaps[len(gaps)/2:])

	sort.Slice(gaps, func(i, j int) bool { return gaps[i] < gaps[j] })

	c.Shortest = gaps[0]
	c.Longest = gaps[len(gaps)-1]
	c.Median = gaps[len(gaps)/2]
	c.Trend = trendOf(c)

	return c
}

// trendOf classifies the movement between the two halves.
func trendOf(c Cadence) Trend {
	if c.Samples < minTrendSamples || c.Early <= 0 {
		return TrendUnknown
	}

	switch ratio := float64(c.Late) / float64(c.Early); {
	case ratio >= trendRatio:
		return TrendEasing

	case ratio <= 1/trendRatio:
		return TrendTightening

	default:
		return TrendSteady
	}
}

// mean averages a non-empty run of durations.
func mean(d []time.Duration) time.Duration {
	if len(d) == 0 {
		return 0
	}

	var total time.Duration
	for _, v := range d {
		total += v
	}

	return total / time.Duration(len(d))
}

// Unit names what the cadence is measured per, for rendering.
func (c Cadence) Unit(f group.Finding) string {
	if f.Kind == event.KindPod && c.Objects > 0 {
		return "per pod"
	}

	return ""
}
