package event_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/troglodytto/daiquiri/internal/event"
)

func TestSeverityString(t *testing.T) {
	tests := []struct {
		name string
		in   event.Severity
		want string
	}{
		{"info renders as INFO", event.SeverityInfo, "INFO"},
		{"warning renders as WARNING", event.SeverityWarning, "WARNING"},
		{"critical renders as CRITICAL", event.SeverityCritical, "CRITICAL"},
		{"out-of-range value renders as UNKNOWN", event.Severity(99), "UNKNOWN"},
		{"negative value renders as UNKNOWN", event.Severity(-1), "UNKNOWN"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.in.String())
		})
	}
}

// TestSeverityOrdersCriticalHighest protects the ordering that report relies on
// to sort findings without a lookup table.
func TestSeverityOrdersCriticalHighest(t *testing.T) {
	assert.Greater(t, event.SeverityCritical, event.SeverityWarning)
	assert.Greater(t, event.SeverityWarning, event.SeverityInfo)
}

// TestSeverityZeroValueIsInfo protects the property that a zero Event is the
// least urgent thing rather than accidentally the most urgent. Reordering the
// const block would silently break this.
func TestSeverityZeroValueIsInfo(t *testing.T) {
	var s event.Severity
	assert.Equal(t, event.SeverityInfo, s)

	var e event.Event
	assert.Equal(t, event.SeverityInfo, e.Severity)
}

// TestObjectWorkload covers the owner derivation that grouping keys on.
//
// Measured across the six provided captures: 94,686 Pod names and 25,311
// ReplicaSet names, all matching their shape exactly, over twelve distinct
// workloads none of which itself ends in a hash-shaped segment.
func TestObjectWorkload(t *testing.T) {
	tests := []struct {
		name string
		kind string
		obj  string
		want string
	}{
		{
			"pod loses its replicaset hash and pod suffix",
			"Pod", "checkout-service-7d4f8b9c5-abc12", "checkout-service",
		},
		{
			"pod whose workload name contains hyphens",
			"Pod", "recommendation-service-5a8e2c1f4-b35ff", "recommendation-service",
		},
		{
			"replicaset loses only its hash",
			"ReplicaSet", "api-gateway-027774d6c", "api-gateway",
		},
		{
			"deployment is already the workload",
			"Deployment", "checkout-service", "checkout-service",
		},
		{
			// Regression: a kind-blind strip mangles node names, and the one
			// record in the corpus that names a node is the anchor for every
			// node-correlation rule.
			"node name is never stripped",
			"Node", "node-4", "node-4",
		},
		{
			// Greedy match must take the longest prefix, so a workload that
			// legitimately ends in nine hex characters survives.
			"workload ending in a hash-shaped segment is preserved",
			"Pod", "svc-deadbeef1-5a8e2c1f4-b35ff", "svc-deadbeef1",
		},
		{
			"pod suffix of the wrong length is not stripped",
			"Pod", "checkout-service-7d4f8b9c5-abc1", "checkout-service-7d4f8b9c5-abc1",
		},
		{
			"replicaset hash containing a non-hex character is not stripped",
			"ReplicaSet", "api-gateway-027774d6g", "api-gateway-027774d6g",
		},
		{
			"a bare name with no owner encoded is returned unchanged",
			"Pod", "standalone", "standalone",
		},
		{
			"empty name is returned unchanged",
			"Pod", "", "",
		},
		{
			"unknown kind is never stripped",
			"StatefulSet", "postgres-0", "postgres-0",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := event.Object{Kind: tt.kind, Name: tt.obj}
			assert.Equal(t, tt.want, o.Workload())
		})
	}
}
