package health

import (
	"strings"
	"testing"
)

func TestStateString(t *testing.T) {
	tests := []struct {
		state    State
		expected string
	}{
		{Healthy, "healthy"},
		{Suspect, "suspect"},
		{Unhealthy, "unhealthy"},
		{State(99), "unknown(99)"},
	}

	for _, tt := range tests {
		got := tt.state.String()
		if got != tt.expected {
			t.Errorf("State(%d).String() = %q, want %q", int(tt.state), got, tt.expected)
		}
	}
}

func TestStateStringUnknownContainsNumber(t *testing.T) {
	s := State(42).String()
	if !strings.Contains(s, "42") {
		t.Errorf("expected unknown state to contain number, got %q", s)
	}
}
