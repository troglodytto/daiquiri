package link_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/troglodytto/daiquiri/internal/classify"
	"github.com/troglodytto/daiquiri/internal/event"
	"github.com/troglodytto/daiquiri/internal/group"
	"github.com/troglodytto/daiquiri/internal/link"
)

// synth builds n findings shaped like a real capture: a rollout followed by
// failures on its pods, repeated, so roughly half the findings acquire a parent.
func synth(n int) []group.Finding {
	base := at("10:00:00.000")
	out := make([]group.Finding, 0, n)

	for i := 0; i < n; i++ {
		w := fmt.Sprintf("svc-%d", i/2)
		ts := base.Add(time.Duration(i) * 10 * time.Second)

		if i%2 == 0 {
			out = append(out, deploy(w, "production", w+"-0123456ab", ts, 3))
			continue
		}

		out = append(out, group.Finding{
			Kind: event.KindPod, Workload: w, Namespace: "production", Reason: "Failed",
			Category: classify.CategoryIssue, Severity: event.SeverityCritical,
			Count: 8, FirstSeen: ts, LastSeen: ts.Add(time.Minute),
			Pods:  []string{w + "-0123456ab-aaaaa", w + "-0123456ab-bbbbb"},
			Nodes: []string{"node-1"},
			Events: []event.Event{{
				Timestamp: ts, Reason: "Failed", Body: "Failed to pull image: ErrImagePull",
				Object: event.Object{Kind: event.KindPod, Name: w + "-0123456ab-aaaaa"},
			}},
		})
	}

	return out
}

// BenchmarkBuild measures the O(R*n^2) link pass. n counts findings, not
// records: the provided captures yield 3 to 10, so 10 is the real working point
// and the larger sizes exist to show the quadratic term is affordable well past
// anything a cluster produces.
func BenchmarkBuild(b *testing.B) {
	for _, n := range []int{10, 100, 1000} {
		findings := synth(n)

		b.Run(fmt.Sprintf("findings=%d", n), func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				_ = link.Build(findings)
			}
		})
	}
}

// BenchmarkQueries measures the read side, which the report walks once per
// incident.
func BenchmarkQueries(b *testing.B) {
	f := link.Build(synth(1000))

	b.Run("Roots", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = f.Roots()
		}
	})

	b.Run("RootOf", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = f.RootOf(len(f.Findings) - 1)
		}
	})
}
