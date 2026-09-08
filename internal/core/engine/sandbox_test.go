package engine

import "testing"

func TestNeedsNoSandboxPlaywrightEnv(t *testing.T) {
	t.Setenv("PLAYWRIGHT_MCP_NO_SANDBOX", "true")
	if !needsNoSandbox() {
		t.Fatal("expected PLAYWRIGHT_MCP_NO_SANDBOX=true to enable no-sandbox")
	}
}
