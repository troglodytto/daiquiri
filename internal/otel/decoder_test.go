package otel_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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
