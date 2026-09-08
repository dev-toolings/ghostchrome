package cli

import (
	"testing"
	"time"
)

// TestMCPIdleTimeoutResolution locks the "reaping is on by default for the MCP
// server" invariant: unset -> 15m default, explicit values honoured, and
// 0/off/garbage disable it. If someone later reverts the default to opt-in this
// test fails loudly.
func TestMCPIdleTimeoutResolution(t *testing.T) {
	cases := []struct {
		name string
		env  string // "" via unset marker below
		set  bool
		want time.Duration
	}{
		{"unset defaults to 15m", "", false, defaultMCPIdleTimeout},
		{"blank defaults to 15m", "   ", true, defaultMCPIdleTimeout},
		{"explicit duration", "30m", true, 30 * time.Minute},
		{"bare seconds", "900", true, 900 * time.Second},
		{"zero disables", "0", true, 0},
		{"invalid disables", "banana", true, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.set {
				t.Setenv("GHOSTCHROME_IDLE_TIMEOUT", c.env)
			} else {
				t.Setenv("GHOSTCHROME_IDLE_TIMEOUT", "")
			}
			if got := mcpIdleTimeout(); got != c.want {
				t.Fatalf("mcpIdleTimeout() = %v, want %v", got, c.want)
			}
		})
	}
}
