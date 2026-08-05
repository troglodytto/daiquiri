package otel_test

import (
	"bufio"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/troglodytto/daiquiri/internal/event"
	"github.com/troglodytto/daiquiri/internal/otel"
)

// TestDecodeNormalisesNodeIdentity covers the one record shape where the node a
// finding must correlate on is not in the field that names it.
//
// A Node-kind event carries its identity in resource["k8s.object.name"] and has
// no resource["k8s.node.name"] at all; kubelets emit that field, and a node
// condition comes from the node controller. The single NodeHasDiskPressure
// record in the provided captures is exactly this shape, and it is the anchor
// for every node-correlation rule downstream. Leaving Node empty makes those
// rules silently fail to match the very event they exist to find.
func TestDecodeNormalisesNodeIdentity(t *testing.T) {
	tests := []struct {
		name     string
		line     string
		wantNode string
	}{
		{
			name:     "node event without node.name adopts its object name",
			line:     `{"timestamp":"2024-06-01T10:15:00.000Z","severity_text":"Warning","body":"Node node-4 status is now: NodeHasDiskPressure","attributes":{"k8s.event.reason":"NodeHasDiskPressure","k8s.namespace.name":"default"},"resource":{"k8s.object.kind":"Node","k8s.object.name":"node-4"}}`,
			wantNode: "node-4",
		},
		{
			name:     "node event that already carries node.name is left alone",
			line:     `{"timestamp":"2024-06-01T10:15:00.000Z","severity_text":"Warning","attributes":{"k8s.event.reason":"NodeNotReady","k8s.namespace.name":"default"},"resource":{"k8s.object.kind":"Node","k8s.object.name":"node-4","k8s.node.name":"node-9"}}`,
			wantNode: "node-9",
		},
		{
			name:     "pod event keeps the node its kubelet reported",
			line:     `{"timestamp":"2024-06-01T10:22:07.319Z","severity_text":"Warning","attributes":{"k8s.event.reason":"Unhealthy","k8s.namespace.name":"production"},"resource":{"k8s.object.kind":"Pod","k8s.object.name":"checkout-service-7d4f8b9c5-005e2","k8s.node.name":"node-2"}}`,
			wantNode: "node-2",
		},
		{
			name:     "scheduler event on a pod has no node, and that is not an error",
			line:     `{"timestamp":"2024-06-01T10:20:04.679Z","severity_text":"Warning","attributes":{"k8s.event.reason":"FailedScheduling","k8s.namespace.name":"data"},"resource":{"k8s.object.kind":"Pod","k8s.object.name":"data-pipeline-3c7d2e1a9-83990"}}`,
			wantNode: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := otel.New(strings.NewReader(tt.line))

			ev, ok := d.Next()
			require.True(t, ok, "record should decode")
			require.NoError(t, d.Err())

			assert.Equal(t, tt.wantNode, ev.Node)
		})
	}
}

// TestStatsCountDecodedAndDamagedRecordsSeparately pins the disclosure contract:
// a record that cannot be interpreted is counted, not raised and not dropped in
// silence, so a truncated capture can never be mistaken for a clean one.
func TestStatsCountDecodedAndDamagedRecordsSeparately(t *testing.T) {
	const good = `{"timestamp":"2024-06-01T10:15:00.000Z","severity_text":"Warning","attributes":{"k8s.event.reason":"BackOff"},"resource":{"k8s.object.kind":"Pod","k8s.object.name":"p"}}`

	tests := []struct {
		name              string
		capture           string
		ingested, skipped int
	}{
		{"a clean capture skips nothing", good + "\n" + good, 2, 0},
		{"unparseable json is skipped", good + "\n" + "not json at all", 1, 1},
		{"a truncated final line is skipped", good + "\n" + `{"timestamp":`, 1, 1},
		{"an unparseable timestamp is skipped", `{"timestamp":"whenever","resource":{}}`, 0, 1},
		{"an absent timestamp is skipped", `{"body":"no clock on this one"}`, 0, 1},
		{"blank lines are formatting, not damage", good + "\n\n   \n\t\n\r\n" + good, 2, 0},
		{"an empty capture yields nothing at all", "", 0, 0},
		{"whitespace only yields nothing at all", "\n  \n\t\n", 0, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := otel.New(strings.NewReader(tt.capture))

			var got int
			for _, ok := d.Next(); ok; _, ok = d.Next() {
				got++
			}

			require.NoError(t, d.Err(), "damaged records are not stream errors")

			assert.Equal(t, tt.ingested, got, "Next yielded a different count than Stats claims")
			assert.Equal(t, tt.ingested, d.Stats().Ingested)
			assert.Equal(t, tt.skipped, d.Stats().Skipped)
		})
	}
}

// TestOverlongLineSurfacesAsAStreamError separates the two ways a capture can
// fail. A damaged record is counted and the run continues; a line past the
// scanner's ceiling stops the stream, and Err is what tells them apart. Without
// the bound this is an out-of-memory kill instead.
func TestOverlongLineSurfacesAsAStreamError(t *testing.T) {
	d := otel.New(strings.NewReader(`{"body":"` + strings.Repeat("x", 2*1024*1024) + `"}`))

	_, ok := d.Next()

	assert.False(t, ok, "the stream stops rather than yielding a partial record")
	require.Error(t, d.Err(), "a stream-level failure must be visible through Err")
	assert.ErrorIs(t, d.Err(), bufio.ErrTooLong)
	assert.Zero(t, d.Stats().Skipped, "an unreadable line is not a skipped record")
}

// TestSeverityIsWarningOrInfoAndNothingElse covers the mapping's default arm:
// anything that is not exactly "Warning" is informational, so an unfamiliar
// severity_text can never be promoted into a warning by accident.
func TestSeverityIsWarningOrInfoAndNothingElse(t *testing.T) {
	tests := []struct {
		text string
		want event.Severity
	}{
		{"Warning", event.SeverityWarning},
		{"Normal", event.SeverityInfo},
		{"", event.SeverityInfo},
		{"warning", event.SeverityInfo},
		{"Error", event.SeverityInfo},
	}

	for _, tt := range tests {
		t.Run("severity_text "+strconv.Quote(tt.text), func(t *testing.T) {
			line := `{"timestamp":"2024-06-01T10:15:00.000Z","severity_text":` +
				strconv.Quote(tt.text) + `,"resource":{"k8s.object.kind":"Pod"}}`

			d := otel.New(strings.NewReader(line))

			ev, ok := d.Next()
			require.True(t, ok)
			assert.Equal(t, tt.want, ev.Severity)
		})
	}
}

// TestAbsentCountFallsBackRatherThanZeroing keeps a record that omits
// k8s.event.count worth one occurrence. Zero would erase it from every total
// downstream.
func TestAbsentCountFallsBackRatherThanZeroing(t *testing.T) {
	for _, tt := range []struct {
		name, attrs string
		want        int
	}{
		{"absent", `{"k8s.event.reason":"BackOff"}`, 1},
		{"explicitly zero", `{"k8s.event.reason":"BackOff","k8s.event.count":0}`, 1},
		{"negative", `{"k8s.event.reason":"BackOff","k8s.event.count":-5}`, 1},
		{"present", `{"k8s.event.reason":"BackOff","k8s.event.count":7}`, 7},
	} {
		t.Run(tt.name, func(t *testing.T) {
			line := `{"timestamp":"2024-06-01T10:15:00.000Z","attributes":` + tt.attrs +
				`,"resource":{"k8s.object.kind":"Pod"}}`

			d := otel.New(strings.NewReader(line))

			ev, ok := d.Next()
			require.True(t, ok)
			assert.Equal(t, tt.want, ev.Count)
		})
	}
}

// TestTheReusedRecordDoesNotLeakBetweenLines guards the clear in unmarshal. The
// record struct is reused across every line to keep allocations flat, and
// encoding/json leaves absent fields untouched; without the clear, an event
// with no node inherits the previous line's, manufacturing a false correlation
// rather than merely losing data.
func TestTheReusedRecordDoesNotLeakBetweenLines(t *testing.T) {
	withNode := `{"timestamp":"2024-06-01T10:15:00.000Z","attributes":{"k8s.event.reason":"Unhealthy","k8s.namespace.name":"production"},"resource":{"k8s.object.kind":"Pod","k8s.object.name":"a","k8s.node.name":"node-2","k8s.cluster.name":"prod"}}`
	without := `{"timestamp":"2024-06-01T10:16:00.000Z","attributes":{"k8s.event.reason":"FailedScheduling"},"resource":{"k8s.object.kind":"Pod","k8s.object.name":"b"}}`

	d := otel.New(strings.NewReader(withNode + "\n" + without))

	first, ok := d.Next()
	require.True(t, ok)
	require.Equal(t, "node-2", first.Node)

	second, ok := d.Next()
	require.True(t, ok)

	assert.Empty(t, second.Node, "the second record inherited the first record's node")
	assert.Empty(t, second.Namespace, "the second record inherited the first record's namespace")
	assert.Empty(t, second.Cluster, "the second record inherited the first record's cluster")
	assert.Equal(t, "FailedScheduling", second.Reason)
}
