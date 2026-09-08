package cli

import (
	"testing"
	"time"
)

func TestServeIdleTimeoutDefaultIsOneHour(t *testing.T) {
	t.Setenv("GHOSTCHROME_IDLE_TIMEOUT", "")
	if got := serveIdleTimeout(); got != time.Hour {
		t.Fatalf("serveIdleTimeout() = %v, want 1h", got)
	}
}

func TestServeIdleTimeoutExplicitZeroDisables(t *testing.T) {
	t.Setenv("GHOSTCHROME_IDLE_TIMEOUT", "0")
	if got := serveIdleTimeout(); got != 0 {
		t.Fatalf("serveIdleTimeout() = %v, want 0", got)
	}
}

func TestServeIdleHeadedDisablesReaper(t *testing.T) {
	t.Setenv("GHOSTCHROME_IDLE_TIMEOUT", "")
	if got := serveIdleTimeoutForSession(true); got != time.Hour {
		t.Fatalf("headless idle = %v, want 1h", got)
	}
	if got := serveIdleTimeoutForSession(false); got != 0 {
		t.Fatalf("headed idle = %v, want 0", got)
	}
}
