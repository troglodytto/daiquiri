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
