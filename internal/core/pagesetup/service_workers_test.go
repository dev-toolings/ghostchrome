package pagesetup

import "testing"

func TestApplyServiceWorkersModeAllowIsNoop(t *testing.T) {
	if err := ApplyServiceWorkersMode(nil, "allow"); err != nil {
		t.Fatalf("ApplyServiceWorkersMode allow: %v", err)
	}
	if err := ApplyServiceWorkersMode(nil, ""); err != nil {
		t.Fatalf("ApplyServiceWorkersMode empty: %v", err)
	}
}

func TestApplyServiceWorkersModeRejectsUnknownMode(t *testing.T) {
	if err := ApplyServiceWorkersMode(nil, "maybe"); err == nil {
		t.Fatal("expected unknown mode error")
	}
}
