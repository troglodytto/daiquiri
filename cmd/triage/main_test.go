package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// capture is a sustained crash-loop: 14 occurrences across 2 pods spanning 14
// minutes, which clears all three transient bounds (>10 occurrences, >1 pod,
// >60s span) so the run produces a real finding rather than background noise.
//
// It is built here rather than read from testdata/ because the CLI tests are
// about the argument contract and the exit codes, not about triage itself, and
// the real captures cost 16MB each to parse.
var capture = buildCapture()

func buildCapture() string {
	const line = `{"timestamp":"2024-06-01T10:%02d:00.000Z","severity_text":"Warning",` +
		`"body":"Back-off restarting failed container","attributes":{"k8s.event.reason":"BackOff",` +
		`"k8s.namespace.name":"production"},"resource":{"k8s.object.kind":"Pod",` +
		`"k8s.object.name":"checkout-service-a58a8beb6-%s","service.name":"k8s-events",` +
		`"k8s.cluster.name":"prod-us-east-1"}}` + "\n"

	var b strings.Builder
	for i := range 14 {
		pod := []string{"3d9eb", "7f2ac"}[i%2]
		fmt.Fprintf(&b, line, i, pod)
	}

	return b.String()
}

// elapsed matches the run duration the header prints, which varies per run.
var elapsed = regexp.MustCompile(`\(\d+(\.\d+)?m?s\)`)

// normalise removes the one nondeterministic part of a rendered report, so two
// runs can be compared for the content they chose to show.
func normalise(s string) string { return elapsed.ReplaceAllString(s, "(T)") }

// write drops a capture in a temp dir and returns its path.
func write(t *testing.T, body string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "capture.jsonl")
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))

	return path
}

// exec runs the CLI over a fixture and returns code, stdout and stderr.
func exec(t *testing.T, args ...string) (int, string, string) {
	t.Helper()

	var stdout, stderr bytes.Buffer
	code := run(args, &stdout, &stderr)

	return code, stdout.String(), stderr.String()
}

func TestCleanRunReportsSuccessAndWritesToStdout(t *testing.T) {
	code, stdout, stderr := exec(t, write(t, capture))

	assert.Equal(t, exitOK, code)
	assert.NotEmpty(t, stdout, "the report goes to stdout")
	assert.Empty(t, stderr, "a clean run says nothing on stderr")
}

func TestMissingFilenameIsAUsageError(t *testing.T) {
	code, stdout, stderr := exec(t)

	assert.Equal(t, exitUsage, code)
	assert.Empty(t, stdout, "diagnostics never contaminate stdout")
	assert.Contains(t, stderr, "filename is required")
	assert.Contains(t, stderr, "usage: triage", "the usage block follows the complaint")
}

func TestSurplusArgumentsAreAUsageError(t *testing.T) {
	path := write(t, capture)
	code, _, stderr := exec(t, path, path)

	assert.Equal(t, exitUsage, code)
	assert.Contains(t, stderr, "filename is required")
}

func TestUnparseableFlagIsAUsageError(t *testing.T) {
	code, stdout, stderr := exec(t, "--nonesuch", write(t, capture))

	assert.Equal(t, exitUsage, code)
	assert.Empty(t, stdout)
	assert.Contains(t, stderr, "nonesuch")
}

func TestUnreadableFileFails(t *testing.T) {
	code, stdout, stderr := exec(t, filepath.Join(t.TempDir(), "absent.jsonl"))

	assert.Equal(t, exitFail, code)
	assert.Empty(t, stdout)
	assert.Contains(t, stderr, "triage: ")
}

// The conflicting-flag rules are settled before the capture is read, so they
// report a usage error even when the path is also bad. The exit code names the
// mistake the caller actually made.
func TestConflictingFlagsAreRejectedBeforeTheFileIsRead(t *testing.T) {
	absent := filepath.Join(t.TempDir(), "absent.jsonl")

	for _, tc := range []struct {
		name  string
		args  []string
		wants string
	}{
		{"json with table", []string{"--json", "--table"}, "--json replaces the human views"},
		{"json with tree", []string{"--json", "--tree"}, "--json replaces the human views"},
		{"table with tree", []string{"--table", "--tree"}, "mutually exclusive"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, stdout, stderr := exec(t, append(tc.args, absent)...)

			assert.Equal(t, exitUsage, code, "a bad argument list is not a read failure")
			assert.Empty(t, stdout)
			assert.Contains(t, stderr, tc.wants)
		})
	}
}

func TestJSONViewEmitsParseableJSONCarryingTheVersion(t *testing.T) {
	code, stdout, stderr := exec(t, "--json", write(t, capture))

	require.Equal(t, exitOK, code, stderr)

	var got struct {
		Schema int `json:"schema"`
		Tool   struct {
			Version string `json:"version"`
		} `json:"tool"`
		Capture struct {
			Ingested int `json:"records_ingested"`
		} `json:"capture"`
	}

	require.NoError(t, json.Unmarshal([]byte(stdout), &got), "stdout must be JSON and nothing else")
	assert.Equal(t, version, got.Tool.Version, "the document names the build it came from")
	assert.NotZero(t, got.Schema)
	assert.Equal(t, 14, got.Capture.Ingested)
}

func TestNarrowingFlagsSelectOneViewEach(t *testing.T) {
	path := write(t, capture)

	_, both, _ := exec(t, path)
	_, table, _ := exec(t, "--table", path)
	_, tree, _ := exec(t, "--tree", path)

	assert.NotEqual(t, normalise(both), normalise(table), "--table drops a view the default renders")
	assert.NotEqual(t, normalise(both), normalise(tree), "--tree drops a view the default renders")
	assert.NotEqual(t, normalise(table), normalise(tree), "the two views are not the same output")
	assert.Less(t, len(table), len(both), "narrowing renders less, not more")
	assert.Less(t, len(tree), len(both), "narrowing renders less, not more")
}

// --trace implies the incident view: the table has no notion of a trail, so
// narrowing it to one workload would only hide rows.
func TestTraceImpliesTheTreeViewEvenAlongsideTable(t *testing.T) {
	path := write(t, capture)

	_, tree, _ := exec(t, "--tree", path)
	code, traced, stderr := exec(t, "--trace", "checkout-service", path)

	require.Equal(t, exitOK, code, stderr)

	_, tracedWithTable, _ := exec(t, "--table", "--trace", "checkout-service", path)
	assert.Equal(t, normalise(traced), normalise(tracedWithTable),
		"--trace overrides --table rather than combining")

	assert.NotEqual(t, normalise(tree), normalise(traced),
		"tracing marks the workload it was given")
}

func TestTraceOnAnUnknownWorkloadStillSucceeds(t *testing.T) {
	code, stdout, stderr := exec(t, "--trace", "no-such-service", write(t, capture))

	assert.Equal(t, exitOK, code, "an unmatched trace is an empty result, not a failure")
	assert.NotEmpty(t, stdout)
	assert.Empty(t, stderr)
}

// A capture whose every line is malformed still parses as a run: bad records
// are counted and disclosed, not raised as errors.
func TestMalformedCaptureIsReportedRatherThanFailing(t *testing.T) {
	code, stdout, stderr := exec(t, write(t, "not json at all\n{\"broken\":\n\n"))

	assert.Equal(t, exitOK, code)
	assert.NotEmpty(t, stdout)
	assert.Empty(t, stderr)
}

func TestEmptyCaptureIsNotAnError(t *testing.T) {
	code, stdout, stderr := exec(t, write(t, ""))

	assert.Equal(t, exitOK, code)
	assert.NotEmpty(t, stdout, "an empty capture still renders a report")
	assert.Empty(t, stderr)
}

func TestUsageNamesTheVersionAndEveryFlag(t *testing.T) {
	_, _, stderr := exec(t)

	assert.Contains(t, stderr, "version: "+version)

	for _, flag := range []string{"-table", "-tree", "-json", "-trace"} {
		assert.Contains(t, stderr, flag, "usage documents every flag")
	}
}

// A writer that fails on the first byte, standing in for a closed pipe.
type brokenWriter struct{}

func (brokenWriter) Write([]byte) (int, error) { return 0, os.ErrClosed }

func TestUnwritableStdoutIsReportedAsAFailure(t *testing.T) {
	path := write(t, capture)

	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"human view", []string{path}, "writing report"},
		{"json view", []string{"--json", path}, "writing json"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stderr bytes.Buffer
			code := run(tc.args, brokenWriter{}, &stderr)

			assert.Equal(t, exitFail, code)
			assert.Contains(t, stderr.String(), tc.want)
		})
	}
}

func TestAnalyseWrapsTheFailingPath(t *testing.T) {
	_, err := analyse(filepath.Join(t.TempDir(), "absent.jsonl"))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "absent.jsonl", "the message names the file it could not read")
}

func TestAnalyseReadsACaptureIntoAResult(t *testing.T) {
	res, err := analyse(write(t, capture))

	require.NoError(t, err)
	assert.NotZero(t, res.Ingested, "the records were counted")
	assert.NotEmpty(t, strings.TrimSpace(res.Chart.Findings[0].Reason))
}
