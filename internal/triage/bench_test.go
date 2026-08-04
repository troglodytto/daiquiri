package triage_test

import (
	"bytes"
	"os"
	"testing"

	"github.com/troglodytto/daiquiri/internal/classify"
	"github.com/troglodytto/daiquiri/internal/triage"
)

// BenchmarkPipeline measures the whole run against the brief's acceptance
// criterion: a 20,000-record capture in under five seconds.
//
// The capture is read into memory once and replayed from a bytes.Reader, so the
// figure is decode plus classify plus coalesce plus link, without disk in it.
// 04-test-a is the heaviest of the six, with 227 reportable records.
func BenchmarkPipeline(b *testing.B) {
	for _, fixture := range []string{"01-healthy.jsonl", "04-test-a.jsonl", "05-test-b.jsonl"} {
		raw, err := os.ReadFile("../../testdata/" + fixture)
		if err != nil {
			b.Fatal(err)
		}

		b.Run(fixture, func(b *testing.B) {
			p := triage.New(classify.New())

			b.SetBytes(int64(len(raw)))
			b.ReportAllocs()
			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				if _, err := p.Run(bytes.NewReader(raw)); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
