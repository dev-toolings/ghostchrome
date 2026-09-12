package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestMCPIdleTimeoutResolution(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  time.Duration
	}{
		{name: "unset defaults to 15m", value: "", want: defaultMCPIdleTimeout},
		{name: "blank defaults to 15m", value: "   ", want: defaultMCPIdleTimeout},
		{name: "duration", value: "30m", want: 30 * time.Minute},
		{name: "bare seconds", value: "900", want: 900 * time.Second},
		{name: "zero disables", value: "0", want: 0},
		{name: "off disables", value: "off", want: 0},
		{name: "invalid disables", value: "not-a-duration", want: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("GHOSTCHROME_IDLE_TIMEOUT", tt.value)
			if got := mcpIdleTimeout(); got != tt.want {
				t.Fatalf("mcpIdleTimeout() = %v, want %v", got, tt.want)
			}
		})
	}
}

// GHOSTCHROME_POLICY used to be read by the CLI only, so the standalone MCP
// binary started unrestricted whatever the variable said. It must now bind the
// policy, and refuse to start when the file cannot be read.
func TestOptionsFromEnvBindsPolicy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "policy.json")
	if err := os.WriteFile(path, []byte(`{"allow_eval":false}`), 0o600); err != nil {
		t.Fatalf("write policy: %v", err)
	}

	t.Setenv("GHOSTCHROME_POLICY", path)
	opts, err := optionsFromEnv()
	if err != nil {
		t.Fatalf("options: %v", err)
	}
	if opts.Policy == nil {
		t.Fatal("expected the policy file to be bound to the server options")
	}
	if err := opts.Policy.AllowAction("eval"); err == nil {
		t.Fatal("expected the bound policy to deny eval")
	}
}

func TestOptionsFromEnvFailsClosedOnUnreadablePolicy(t *testing.T) {
	t.Setenv("GHOSTCHROME_POLICY", filepath.Join(t.TempDir(), "missing.json"))
	if _, err := optionsFromEnv(); err == nil {
		t.Fatal("expected an unreadable policy to fail the server startup")
	}
}

func TestOptionsFromEnvWithoutPolicy(t *testing.T) {
	t.Setenv("GHOSTCHROME_POLICY", "")
	opts, err := optionsFromEnv()
	if err != nil {
		t.Fatalf("options: %v", err)
	}
	if opts.Policy != nil {
		t.Fatalf("expected no policy when the variable is unset, got %+v", opts.Policy)
	}
}
