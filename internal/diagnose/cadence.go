package diagnose

import (
	"sort"
	"time"

	"github.com/troglodytto/daiquiri/internal/event"
	"github.com/troglodytto/daiquiri/internal/group"
)

// Sample minimums. Below these the answer would be arithmetic on noise, and a
// finding under the minimum reports nothing rather than something confident.
const (
	minCadenceSamples = 6
	minTrendSamples   = 8
)

// trendRatio is how far the late half must diverge from the early half before
// the rhythm is called anything but steady.
//
// Symmetric: easing at >= 3, tightening at <= 1/3. Measured across the corpus,
// steady sits at 0.99 and 1.01 over 217 and 171 intervals, and the one
// unmistakable departure -- Kubernetes' exponential backoff in
// 03-image-pull-failure -- sits at 5.13 and 11.49. The observed gap is
// (1.01, 5.13), whose geometric midpoint is 2.28; 3 sits above that and leaves
// 3x margin on the steady side.
//
// Deliberately too blunt to fire on 02-memory-leak, whose OOM intervals ease
// from a mean of 4m39s to 3m41s -- a ratio of 0.79 across 22 samples that
// already range from 169s to 326s. See D-58: "the leak is accelerating" is a
// claim this corpus does not support, so the tool does not make it, and the
// numbers are published so a reader can judge.
const trendRatio = 3.0

// Trend is which way a finding's rhythm is moving.
type Trend uint8

// Trends. The zero value is Unknown, which is what too few samples produce.
const (
	// TrendUnknown means fewer than minTrendSamples intervals were available.
	TrendUnknown Trend = iota
	// TrendSteady is a fixed rhythm: a probe period, a scheduler retry.
	TrendSteady
	// TrendEasing is intervals growing -- Kubernetes backing off. It tells an
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
//
// A count and a span say a failure happened 26 times over 25 minutes. The
// cadence says it happened to each pod every four minutes, which is the shape
// of the thing rather than its size.
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
// Intervals are taken **per object and then pooled**, never across the finding
// as a whole. Interleaving otherwise destroys the number: 04-test-a's five pods
// are probed independently, so the finding-wide gap is 1.4s where the actual
// probe period is 10.5s -- and 10.5s is a fact about the deployment where 1.4s
// is an artefact of how many replicas happened to be failing.
//
// Findings that name no object -- a rollout, a node condition -- fall back to
// the whole series, which for them is the same thing.
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
	// happened rather than by which object produced them.
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
//
// Pods for a pod finding, and nothing for a rollout or a node condition, where
// "per node" would imply a comparison across nodes that was never made.
func (c Cadence) Unit(f group.Finding) string {
	if f.Kind == event.KindPod && c.Objects > 0 {
		return "per pod"
	}

	return ""
}
