package cli

import "testing"

func TestPreviewWaitFlagDefaultsToLoad(t *testing.T) {
	flag := previewCmd.Flags().Lookup("wait")
	if flag == nil {
		t.Fatal("expected preview command to define --wait")
	}
	if flag.DefValue != "load" {
		t.Fatalf("expected preview --wait default to be load, got %q", flag.DefValue)
	}
}

func TestPreviewWaitFlagAcceptsKnownStrategies(t *testing.T) {
	flag := previewCmd.Flags().Lookup("wait")
	if flag == nil {
		t.Fatal("expected preview command to define --wait")
	}
	for _, strategy := range []string{"load", "stable", "idle", "none"} {
		if err := flag.Value.Set(strategy); err != nil {
			t.Errorf("setting --wait=%s should succeed: %v", strategy, err)
		}
		if got := flag.Value.String(); got != strategy {
			t.Errorf("--wait roundtrip mismatch: set %q, got %q", strategy, got)
		}
	}
	// Reset to default so subsequent tests aren't poisoned.
	_ = flag.Value.Set(flag.DefValue)
}
