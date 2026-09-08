package cli

import (
	"testing"

	"github.com/dev-toolings/ghostchrome/internal/core/feedback"
)

// TestAgentWaitFlagsRegistered ensures the interaction commands have
// --wait-for and --wait-timeout-ms flags registered.
func TestAgentWaitFlagsRegistered(t *testing.T) {
	for _, flagName := range []string{"wait-for", "wait-timeout-ms"} {
		if f := clickCmd.Flags().Lookup(flagName); f == nil {
			t.Errorf("click: missing flag --%s", flagName)
		}
		if f := typeCmd.Flags().Lookup(flagName); f == nil {
			t.Errorf("type: missing flag --%s", flagName)
		}
		if f := hoverCmd.Flags().Lookup(flagName); f == nil {
			t.Errorf("hover: missing flag --%s", flagName)
		}
		if f := pressCmd.Flags().Lookup(flagName); f == nil {
			t.Errorf("press: missing flag --%s", flagName)
		}
		if f := selectCmd.Flags().Lookup(flagName); f == nil {
			t.Errorf("select: missing flag --%s", flagName)
		}
	}
}

// TestAgentRecoveryHooksInitialised verifies the default session has recovery hooks.
func TestAgentRecoveryHooksInitialised(t *testing.T) {
	hooks := feedback.DefaultRecoveryHooks()
	if len(hooks) == 0 {
		t.Fatal("expected at least one default recovery hook")
	}
}
