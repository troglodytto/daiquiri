package diagnose_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/troglodytto/daiquiri/internal/classify"
	"github.com/troglodytto/daiquiri/internal/diagnose"
	"github.com/troglodytto/daiquiri/internal/event"
	"github.com/troglodytto/daiquiri/internal/group"
	"github.com/troglodytto/daiquiri/internal/link"
)

// synthetic builds a forest of n findings in one chain, which is the worst case
// for this stage: RootOf walks the full depth for every finding, so the chain is
// where the O(n²) actually shows up. A flat forest of n roots would benchmark
// the easy shape and prove nothing.
func synthetic(n int) link.Forest {
	findings := make([]group.Finding, n)
	edges := make([]link.Edge, n)

	for i := range findings {
		findings[i] = group.Finding{
			Kind:       event.KindPod,
			Workload:   fmt.Sprintf("service-%d", i),
			Namespace:  "production",
			Reason:     "BackOff",
			Rule:       classify.RuleCrashLoop,
			Category:   classify.CategoryIssue,
			Severity:   event.SeverityCritical,
			Recognised: true,
			Count:      50,
			FirstSeen:  start.Add(time.Duration(i) * time.Second),
			LastSeen:   start.Add(time.Duration(i)*time.Second + 10*time.Minute),
			Pods:       []string{"a", "b", "c"},
		}

		edges[i] = link.Edge{Parent: i - 1, Kind: link.KindCaused}
	}

	return link.Forest{Findings: findings, Edges: edges}
}

// BenchmarkBuild covers the real range and two orders of magnitude past it. The
// provided captures produce 3 to 10 findings; n counts distinct failure modes,
// not records, so it does not grow with input size.
func BenchmarkBuild(b *testing.B) {
	for _, n := range []int{10, 100, 1000} {
		f := synthetic(n)

		b.Run(fmt.Sprint("n=", n), func(b *testing.B) {
			b.ReportAllocs()

			for i := 0; i < b.N; i++ {
				_ = diagnose.Build(f, start.Add(time.Hour))
			}
		})
	}
}

// BenchmarkReported measures what the renderer calls once per run.
func BenchmarkReported(b *testing.B) {
	c := diagnose.Build(synthetic(10), start.Add(time.Hour))

	b.ReportAllocs()

	for b.Loop() {
		_ = c.Reported()
	}
}
