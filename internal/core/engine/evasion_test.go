package engine

import "testing"

func TestEvadeRuntimeToggle(t *testing.T) {
	// Default is off — full console-error capture.
	SetEvadeRuntimeEnable(false)
	if EvadeRuntimeEnable() {
		t.Fatal("expected evade off by default after Set(false)")
	}

	SetEvadeRuntimeEnable(true)
	if !EvadeRuntimeEnable() {
		t.Fatal("expected evade on after Set(true)")
	}

	// Restore so other tests in the package see the default.
	SetEvadeRuntimeEnable(false)
	if EvadeRuntimeEnable() {
		t.Fatal("expected evade off after restore")
	}
}
