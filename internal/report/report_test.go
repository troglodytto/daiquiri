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
			Forest:    link.Forest{Findings: findings, Edges: roots(len(findings))},
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

// roots builds n parentless edges.
//
// Explicitly, never as the zero value: a zeroed link.Edge has Parent 0, which
// says "my parent is finding 0" -- and for finding 0 itself that is a self-loop
// no invariant permits.
func roots(n int) []link.Edge {
	edges := make([]link.Edge, n)
	for i := range edges {
		edges[i] = link.Edge{Parent: -1}
	}

	return edges
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
	assert.Contains(t, buf.String(), "no issues detected",
		"the brief names this string outright; it must survive verbatim")
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
			Forest:    link.Forest{Findings: findings, Edges: roots(len(findings))},
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

	// Table only. This is a test about column alignment, and the incident view
	// repeats every reason in prose where no column applies.
	var buf bytes.Buffer
	require.NoError(t, report.New(&buf).Only(report.ViewTable).Render(res))

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

// TestRenderBothViewsByDefault: the flags narrow, they do not enable. A reader
// who passes nothing should not have to know the tool had a second thing to
// show them.
func TestRenderBothViewsByDefault(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, report.New(&buf).Render(deployFailure()))

	out := buf.String()
	assert.Contains(t, out, "SEVERITY", "the table")
	assert.Contains(t, out, "ROOT CAUSE", "and the tree")
	assert.Less(t, strings.Index(out, "SEVERITY"), strings.Index(out, "ROOT CAUSE"),
		"inventory first, argument second")
}

func TestRenderTableOnly(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, report.New(&buf).Only(report.ViewTable).Render(deployFailure()))

	golden(t, "table-only.txt", buf.Bytes())
	assert.NotContains(t, buf.String(), "ROOT CAUSE")
}

func TestRenderTreeOnly(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, report.New(&buf).Only(report.ViewTree).Render(deployFailure()))

	golden(t, "tree-only.txt", buf.Bytes())
	assert.NotContains(t, buf.String(), "SEVERITY")
}

// TestTreeIsEmphasisOnly holds the incident view to the same contract as the
// table. It carries far more styles -- two inverted badges, a quoted record, our
// own prose about that record -- so it is the rendering most likely to leak a
// fact into colour alone.
func TestTreeIsEmphasisOnly(t *testing.T) {
	var plain, styled bytes.Buffer
	require.NoError(t, report.New(&plain).Only(report.ViewTree).Render(deployFailure()))
	require.NoError(t, report.NewStyled(&styled).Only(report.ViewTree).Render(deployFailure()))

	require.Contains(t, styled.String(), string(esc), "the styled renderer must actually style")
	assert.NotContains(t, plain.String(), string(esc))
	assert.Equal(t, plain.String(), stripANSI(styled.String()))
}

// TestTreeHasNoTrailingWhitespace: captured output is diffed and grepped, and
// invisible padding is noise in both.
//
// The one exception is a line ending in a badge. ROOT CAUSE and PAGED HERE are
// inverted blocks, and the space inside the inversion is what makes them read as
// blocks rather than as words -- so it is padding that does visible work, and it
// is identical in the plain and styled renderings.
func TestTreeHasNoTrailingWhitespace(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, report.New(&buf).Only(report.ViewTree).Render(deployFailure()))

	for n, line := range strings.Split(buf.String(), "\n") {
		if strings.HasSuffix(line, "PAGED HERE ") || strings.HasSuffix(line, "ROOT CAUSE ") {
			continue
		}

		assert.Equal(t, strings.TrimRight(line, " "), line, "line %d has trailing spaces", n+1)
	}
}

// TestTreeQuotesOnlyASpecificSignature covers the verdict block's restraint. A
// generic signature restates the reason, which the cause line above it already
// gave, and quoting it spends the most prominent line in the report on a
// paraphrase of the question.
func TestTreeQuotesOnlyASpecificSignature(t *testing.T) {
	res := deployFailure()
	res.Chart.Diagnoses[1].Signatures = []diagnose.Signature{
		{Text: "Error: ImagePullBackOff", Count: 18, Specific: false},
	}

	var buf bytes.Buffer
	require.NoError(t, report.New(&buf).Only(report.ViewTree).Render(res))

	verdict := strings.SplitN(buf.String(), "how we got there", 2)[0]
	assert.NotContains(t, verdict, "Error: ImagePullBackOff",
		"a restatement of the reason must not be quoted as the answer")
	assert.Contains(t, verdict, "image pull failed", "the taxonomy's own sentence still stands")
	assert.Contains(t, verdict, "the image this deployment asks for does not exist",
		"and so does the plain-language reading, which is what a service owner acts on")
}

// TestRemediationIsRenderedLast, because it is what the reader does after they
// have understood the rest.
func TestRemediationIsRenderedLast(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, report.New(&buf).Only(report.ViewTree).Render(deployFailure()))

	out := buf.String()
	require.Contains(t, out, "RECOMMENDED")
	assert.Greater(t, strings.Index(out, "RECOMMENDED"), strings.Index(out, "how we got there"))
	assert.Contains(t, out, "deploy/payment-service -n production", "placeholders are resolved")
}

// deployFailure mirrors 03-image-pull-failure: a rollout, the pull it broke, and
// the retry loop beneath that. Three levels, which is the deepest chain the
// corpus produces, and one of each badge.
//
// Built by hand rather than by running diagnose, for the same reason as
// nodePressure: a golden file that moved whenever a threshold was retuned would
// be testing the wrong package.
func deployFailure() triage.Result {
	rollout := group.Finding{
		Kind: "Deployment", Workload: "payment-service", Namespace: "production",
		Reason: "ScalingReplicaSet", Rule: "deploy/scaled",
		Category: classify.CategoryDeployMarker, Severity: event.SeverityInfo, Recognised: true,
		Count: 1, FirstSeen: at("10:17:59.977"), LastSeen: at("10:17:59.977"),
	}
	pull := finding(event.SeverityCritical, "payment-service", "production", "Failed", 24,
		[]string{"payment-service-9e3f1a2b8-005e2", "payment-service-9e3f1a2b8-7f871", "payment-service-9e3f1a2b8-c257b"},
		[]string{"node-2", "node-3"}, at("10:18:04.412"), at("10:28:19.001"))
	pull.Cause = "image pull failed; the tag or registry credentials are likely wrong"
	pull.Meaning = "the image this deployment asks for does not exist where it is looking -- most often a tag that was never pushed, or a typo in the version"
	pull.Fix = "confirm the tag exists, then roll back: kubectl rollout undo deploy/{workload} -n {namespace}"

	retry := finding(event.SeverityCritical, "payment-service", "production", "BackOff", 18,
		[]string{"payment-service-9e3f1a2b8-005e2", "payment-service-9e3f1a2b8-7f871", "payment-service-9e3f1a2b8-c257b"},
		[]string{"node-2", "node-3"}, at("10:18:14.858"), at("10:28:18.900"))

	findings := []group.Finding{rollout, pull, retry}

	return triage.Result{
		Ingested: 20000, Noise: 19952, Cluster: "prod-us-east-1",
		Elapsed: 133 * time.Millisecond,
		Chart: diagnose.Chart{
			Forest: link.Forest{
				Findings: findings,
				Edges: []link.Edge{
					{Parent: -1},
					{Parent: 0, Kind: link.KindCaused, Rule: "rollout-replicaset",
						Evidence: "rollout created replica set payment-service-9e3f1a2b8 4.4s earlier; all 3 affected pods belong to it"},
					{Parent: 1, Kind: link.KindCaused, Rule: "same-pod",
						Evidence: "same pod payment-service-9e3f1a2b8-005e2, 10.4s after Failed"},
				},
			},
			CaptureEnd: at("10:29:59.677"),
			Diagnoses: []diagnose.Diagnosis{
				{Pattern: diagnose.PatternNone, Confidence: diagnose.ConfidenceExplained,
					Signatures: []diagnose.Signature{
						{Text: "Scaled up replica set payment-service-9e3f1a2b8 to 3", Count: 1, Specific: true},
					}},
				{Pattern: diagnose.PatternDeployCorrelated, Confidence: diagnose.ConfidenceExplained,
					Signatures: []diagnose.Signature{
						{Text: `Failed to pull image "registry.internal/payment-service:v2.14.0-rc3": rpc error: code = NotFound desc = manifest not found`, Count: 3, Specific: true},
						{Text: "Error: ImagePullBackOff", Count: 18, Specific: false},
						{Text: "Error: ErrImagePull", Count: 3, Specific: false},
					}},
				{Pattern: diagnose.PatternDeployCorrelated, Confidence: diagnose.ConfidenceExplained,
					Signatures: []diagnose.Signature{
						{Text: `Back-off pulling image "registry.internal/payment-service:v2.14.0-rc3"`, Count: 18, Specific: true},
					}},
			},
		},
	}
}
