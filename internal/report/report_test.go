package report_test

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/troglodytto/daiquiri/internal/classify"
	"github.com/troglodytto/daiquiri/internal/diagnose"
	"github.com/troglodytto/daiquiri/internal/event"
	"github.com/troglodytto/daiquiri/internal/group"
	"github.com/troglodytto/daiquiri/internal/link"
	"github.com/troglodytto/daiquiri/internal/report"
	"github.com/troglodytto/daiquiri/internal/triage"
)

// update regenerates the golden files. Every regenerated diff is read before it
// is accepted -- a blindly updated golden is worse than no test at all.
var update = flag.Bool("update", false, "regenerate golden files")

// esc is the byte that begins every ANSI escape sequence. Its absence is what
// "degrades to clean plain text" actually means, and the submission requires
// captured stdout files that a grader will open in a text editor.
const esc = '\x1b'

func at(hhmmss string) time.Time {
	ts, err := time.Parse(time.RFC3339Nano, "2024-06-01T"+hhmmss+"Z")
	if err != nil {
		panic(err)
	}
	return ts
}

func finding(sev event.Severity, workload, ns, reason string, n int, pods, nodes []string, first, last time.Time) group.Finding {
	return group.Finding{
		Kind: event.KindPod, Workload: workload, Namespace: ns, Reason: reason,
		Category: classify.CategoryIssue, Severity: sev,
		Cause: "cause for " + reason,
		Count: n, FirstSeen: first, LastSeen: last, Pods: pods, Nodes: nodes,
	}
}

// nodePressure mirrors the shape of 05-test-b closely enough to exercise every
// column width, severity, pattern and empty-node case, without coupling this
// package's tests to the fixtures.
//
// The diagnoses are stated outright rather than derived by calling diagnose.
// This package's prohibition is that it decides nothing, so its tests supply the
// decisions -- and a golden file that moved whenever a threshold was retuned
// would be testing the wrong package.
func nodePressure() triage.Result {
	findings := []group.Finding{
		finding(event.SeverityWarning, "batch-reporter", "data", "Unhealthy", 3,
			[]string{"batch-reporter-19cb12bab-7143a"}, []string{"node-5"}, at("10:01:29.089"), at("10:01:44.735")),
		nodeFinding("node-4", at("10:15:00.000")),
		finding(event.SeverityWarning, "data-pipeline", "data", "Evicted", 3,
			[]string{"a", "b", "c"}, []string{"node-4"}, at("10:15:25.779"), at("10:19:56.562")),
		finding(event.SeverityInfo, "data-pipeline", "data", "FailedScheduling", 177,
			[]string{"a", "b", "c", "d", "e", "f"}, nil, at("10:20:04.679"), at("10:29:59.677")),
	}

	// One of each: a held-back blip, an unlabelled finding, and two patterns.
	diagnoses := []diagnose.Diagnosis{
		{Pattern: diagnose.PatternTransient, Confidence: diagnose.ConfidenceUnexplained, Suppressed: true},
		{Pattern: diagnose.PatternNodeIssue, Confidence: diagnose.ConfidenceExplained},
		{Pattern: diagnose.PatternNodeIssue, Confidence: diagnose.ConfidenceExplained},
		{Pattern: diagnose.PatternNone, Confidence: diagnose.ConfidenceUnexplained},
	}

	return triage.Result{
		Ingested: 20000, Noise: 19984, Cluster: "prod-us-east-1",
		Elapsed: 131 * time.Millisecond,
		Chart: diagnose.Chart{
			Forest:    link.Forest{Findings: findings, Edges: make([]link.Edge, len(findings))},
			Diagnoses: diagnoses,
		},
	}
}

// nodeFinding is a Node-kind finding: it carries no pods, because a node
// condition is not about pods.
func nodeFinding(node string, first time.Time) group.Finding {
	return group.Finding{
		Kind: event.KindNode, Workload: node, Namespace: "default",
		Reason: "NodeHasDiskPressure", Category: classify.CategoryIssue,
		Severity: event.SeverityCritical, Cause: "node is under disk pressure",
		Count: 1, FirstSeen: first, LastSeen: first, Nodes: []string{node},
	}
}

func golden(t *testing.T, name string, got []byte) {
	t.Helper()

	path := filepath.Join("testdata", "golden", name)
	if *update {
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, got, 0o644))
		return
	}

	want, err := os.ReadFile(path)
	require.NoError(t, err, "golden missing; regenerate with -update and read the diff")
	assert.Equal(t, string(want), string(got))
}

func TestRenderPlain(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, report.New(&buf).Render(nodePressure()))

	golden(t, "node-pressure.txt", buf.Bytes())
}

// TestRenderPlainHasNoEscapeSequences is the contract that makes the captured
// output files the brief demands actually readable. A bytes.Buffer is not a
// terminal, so the renderer must produce no styling at all.
func TestRenderPlainHasNoEscapeSequences(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, report.New(&buf).Render(nodePressure()))

	assert.NotContains(t, buf.String(), string(esc))
}

// TestStylingIsEmphasisOnly pins engineering-standards.md §5: text and styled
// output must carry identical information. Strip the escapes from the styled
// rendering and it must be byte-identical to the plain one -- so no fact is
// ever conveyed by colour alone.
func TestStylingIsEmphasisOnly(t *testing.T) {
	var plain, styled bytes.Buffer
	require.NoError(t, report.New(&plain).Render(nodePressure()))
	require.NoError(t, report.NewStyled(&styled).Render(nodePressure()))

	require.Contains(t, styled.String(), string(esc), "the styled renderer must actually style")
	assert.Equal(t, plain.String(), stripANSI(styled.String()))
}

func TestRenderNoFindings(t *testing.T) {
	var buf bytes.Buffer
	res := triage.Result{Ingested: 20000, Noise: 20000, Cluster: "prod-us-east-1", Elapsed: 137 * time.Millisecond}
	require.NoError(t, report.New(&buf).Render(res))

	golden(t, "no-findings.txt", buf.Bytes())
	assert.Contains(t, buf.String(), "ALL CLEAR")
	assert.Contains(t, buf.String(), "Nothing was held back")
}

// TestAllClearDisclosesWhatWasHeldBack is 01-healthy's actual shape: a clean
// cluster whose three background findings were suppressed. "No findings" from a
// tool that quietly dropped three things is a claim the reader cannot check, and
// the entire suppression design rests on that count staying visible -- so it has
// to survive into the one output where there is nothing else to read.
func TestAllClearDisclosesWhatWasHeldBack(t *testing.T) {
	var buf bytes.Buffer
	res := healthy()
	require.NoError(t, report.New(&buf).Render(res))

	golden(t, "all-clear-with-background.txt", buf.Bytes())
	assert.Contains(t, buf.String(), "3 transient blips held back as background")
}

// TestAllClearIsEmphasisOnly holds the healthy path to the same contract as the
// table: it is the one output whose whole point is a colour, and a captured file
// of it must still read as a sentence.
func TestAllClearIsEmphasisOnly(t *testing.T) {
	var plain, styled bytes.Buffer
	require.NoError(t, report.New(&plain).Render(healthy()))
	require.NoError(t, report.NewStyled(&styled).Render(healthy()))

	require.Contains(t, styled.String(), string(esc), "the styled renderer must actually style")
	assert.NotContains(t, plain.String(), string(esc))
	assert.Equal(t, plain.String(), stripANSI(styled.String()))
}

// healthy is 01-healthy: nothing to report, three findings held back.
func healthy() triage.Result {
	findings := []group.Finding{
		finding(event.SeverityWarning, "data-pipeline", "data", "Evicted", 1,
			[]string{"data-pipeline-7a960e693-836d4"}, []string{"node-2"}, at("10:02:32.345"), at("10:02:32.345")),
		finding(event.SeverityWarning, "recommendation-service", "production", "FailedMount", 1,
			[]string{"recommendation-service-5a8e2c1f4-ea1b0"}, []string{"node-3"}, at("10:06:04.117"), at("10:06:04.117")),
		finding(event.SeverityWarning, "notification-service", "staging", "Unhealthy", 3,
			[]string{"notification-service-1afa35f80-97fed"}, []string{"node-1"}, at("10:13:55.021"), at("10:14:07.443")),
	}

	diagnoses := make([]diagnose.Diagnosis, len(findings))
	for i := range diagnoses {
		diagnoses[i] = diagnose.Diagnosis{Pattern: diagnose.PatternTransient, Suppressed: true}
	}

	return triage.Result{
		Ingested: 20000, Noise: 19997, Cluster: "prod-us-east-1",
		Elapsed: 128 * time.Millisecond,
		Chart: diagnose.Chart{
			Forest:    link.Forest{Findings: findings, Edges: make([]link.Edge, len(findings))},
			Diagnoses: diagnoses,
		},
	}
}

// TestRenderDisclosesSkippedAndUnrecognised covers the two counters that exist
// so a capture the tool could not fully read cannot masquerade as a clean one.
func TestRenderDisclosesSkippedAndUnrecognised(t *testing.T) {
	var buf bytes.Buffer
	res := nodePressure()
	res.Skipped = 4
	res.Unrecognised = 1
	require.NoError(t, report.New(&buf).Render(res))

	assert.Contains(t, buf.String(), "skipped 4")
	assert.Contains(t, buf.String(), "unrecognised 1")
}

// TestColumnsFitTheirContent guards the table against a workload name wider
// than its column, which would push every following column out of alignment.
func TestColumnsFitTheirContent(t *testing.T) {
	res := nodePressure()
	res.Chart.Findings[1].Workload = "a-workload-with-a-very-long-name-indeed"

	var buf bytes.Buffer
	require.NoError(t, report.New(&buf).Render(res))

	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")

	// Every reason cell must start at the same offset as the REASON heading. If
	// the widened workload column failed to push the rest along, they diverge.
	want := columnOffset(t, lines, "REASON")

	rows := 0
	for _, l := range lines {
		for _, reason := range []string{"NodeHasDiskPressure", "Evicted", "FailedScheduling"} {
			if i := strings.Index(l, reason); i >= 0 {
				assert.Equal(t, want, i, "row misaligned: %q", l)
				rows++
				break
			}
		}
	}

	assert.Equal(t, len(res.Chart.Reported()), rows, "every reported finding must have produced a row")
}

func columnOffset(t *testing.T, lines []string, heading string) int {
	t.Helper()

	for _, l := range lines {
		if i := strings.Index(l, heading); i >= 0 {
			return i
		}
	}

	t.Fatalf("heading %q not found in output", heading)

	return -1
}

// stripANSI removes CSI sequences so styled and plain output can be compared.
func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] == esc && i+1 < len(s) && s[i+1] == '[' {
			i += 2
			for i < len(s) && (s[i] < '@' || s[i] > '~') {
				i++
			}
			i++ // the final byte
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}
