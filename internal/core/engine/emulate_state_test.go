package engine

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/go-rod/rod/lib/launcher"
)

// responsiveTestDocument carries the meta viewport tag every responsive app
// ships; without it Chrome reports the 980px mobile fallback layout width.
const responsiveTestDocument = `<!doctype html>
<html>
  <head><meta name="viewport" content="width=device-width, initial-scale=1"></head>
  <body><h1>viewport probe</h1></body>
</html>`

func TestEmulationStateSurvivesSessionStateRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.json")
	want := EmulationState{
		Device:      "iphone-14",
		Width:       390,
		Height:      844,
		DPR:         3,
		Mobile:      true,
		Touch:       true,
		UserAgent:   "Mozilla/5.0 (iPhone)",
		ColorScheme: "dark",
		Timezone:    "Europe/Paris",
	}

	if err := saveSessionState(path, &sessionState{Emulation: want}); err != nil {
		t.Fatalf("save session state: %v", err)
	}
	loaded, err := loadSessionState(path)
	if err != nil {
		t.Fatalf("load session state: %v", err)
	}
	if loaded.Emulation != want {
		t.Fatalf("emulation round trip = %+v, want %+v", loaded.Emulation, want)
	}
}

func TestEmulationStateEmptyAndSummary(t *testing.T) {
	if !(EmulationState{}).Empty() {
		t.Fatal("zero EmulationState should be empty")
	}
	// A device name alone is not replayable — Empty() must not be fooled by it.
	if !(EmulationState{Device: "iphone-14"}).Empty() {
		t.Fatal("device name without metrics should be empty")
	}
	if (EmulationState{Width: 390, Height: 844}).Empty() {
		t.Fatal("viewport metrics should not be empty")
	}
	if (EmulationState{Timezone: "Europe/Paris"}).Empty() {
		t.Fatal("timezone override should not be empty")
	}

	got := EmulationState{Device: "iphone-14", Width: 390, Height: 844, DPR: 3, Mobile: true, Touch: true}.Summary()
	if want := "iphone-14 390x844@3x mobile touch"; got != want {
		t.Fatalf("summary = %q, want %q", got, want)
	}
	if got := (EmulationState{}).Summary(); got != "none" {
		t.Fatalf("empty summary = %q, want %q", got, "none")
	}
}

func TestEmulationFromDeviceMirrorsPreset(t *testing.T) {
	d, ok := DeviceByName("iphone-14")
	if !ok {
		t.Fatal("iphone-14 preset missing")
	}
	got := EmulationFromDevice(d)
	want := EmulationState{
		Device:    d.Name,
		Width:     d.Width,
		Height:    d.Height,
		DPR:       d.DPR,
		Mobile:    d.Mobile,
		Touch:     d.Touch,
		UserAgent: d.UserAgent,
	}
	if got != want {
		t.Fatalf("EmulationFromDevice = %+v, want %+v", got, want)
	}
}

// TestApplyDeviceHandlesTouchAndNonTouchPresets covers the CDP contract that
// used to break every non-touch preset: Chrome rejects setTouchEmulationEnabled
// with "Touch points must be between 1 and 16" when maxTouchPoints is sent
// while disabling touch.
func TestApplyDeviceHandlesTouchAndNonTouchPresets(t *testing.T) {
	if testing.Short() {
		t.Skip("requires Chrome")
	}
	_, page := newIsolatedPage(t)

	touchDevice, ok := DeviceByName("iphone-14")
	if !ok {
		t.Fatal("iphone-14 preset missing")
	}
	if err := ApplyDevice(page, touchDevice); err != nil {
		t.Fatalf("apply touch preset: %v", err)
	}
	points, err := EvalJS(page, `navigator.maxTouchPoints`, "", nil)
	if err != nil {
		t.Fatalf("eval maxTouchPoints: %v", err)
	}
	if points != "5" {
		t.Fatalf("maxTouchPoints after touch preset = %s, want 5", points)
	}

	desktop, ok := DeviceByName("desktop")
	if !ok {
		t.Fatal("desktop preset missing")
	}
	if err := ApplyDevice(page, desktop); err != nil {
		t.Fatalf("apply non-touch preset: %v", err)
	}

	if err := ResetEmulation(page); err != nil {
		t.Fatalf("reset emulation: %v", err)
	}
	points, err = EvalJS(page, `navigator.maxTouchPoints`, "", nil)
	if err != nil {
		t.Fatalf("eval maxTouchPoints: %v", err)
	}
	if points != "0" {
		t.Fatalf("maxTouchPoints after reset = %s, want 0", points)
	}
}

// TestManagedSessionReplaysEmulationOnAttach is the regression test for the
// daemon viewport bug: CDP emulation overrides die with the DevTools session,
// so a viewport set by one CLI invocation was gone by the next one.
//
// The profile is only ever written to the session state — never applied live —
// so the second connection can only report 390x844 if it replayed it on attach.
func TestManagedSessionReplaysEmulationOnAttach(t *testing.T) {
	if testing.Short() {
		t.Skip("requires Chrome")
	}

	l := launcher.New().Headless(true).Leakless(false).
		UserDataDir(filepath.Join(t.TempDir(), "chrome-profile"))
	if needsNoSandbox() {
		l = l.NoSandbox(true)
	}
	controlURL, err := l.Launch()
	if err != nil {
		CleanupFailedLauncher(l, true)
		t.Fatalf("launch browser: %v", err)
	}
	defer func() {
		l.Kill()
		l.Cleanup()
	}()

	statePath, err := sessionStatePath(controlURL)
	if err != nil {
		t.Fatalf("session state path: %v", err)
	}
	_ = os.Remove(statePath)
	defer os.Remove(statePath)

	writer, err := NewBrowserWith(BrowserOpts{ConnectURL: controlURL, TimeoutSec: 10, ManagedSession: true})
	if err != nil {
		t.Fatalf("new managed browser: %v", err)
	}
	if _, err := writer.Page(); err != nil {
		writer.Close()
		t.Fatalf("page: %v", err)
	}
	device, _ := DeviceByName("iphone-14")
	if err := writer.SetEmulationState(EmulationFromDevice(device)); err != nil {
		writer.Close()
		t.Fatalf("persist emulation: %v", err)
	}
	writer.Close()

	t.Run("managed session restores the viewport", func(t *testing.T) {
		b, err := NewBrowserWith(BrowserOpts{ConnectURL: controlURL, TimeoutSec: 10, ManagedSession: true})
		if err != nil {
			t.Fatalf("new managed browser: %v", err)
		}
		defer b.Close()
		page, err := b.Page()
		if err != nil {
			t.Fatalf("page: %v", err)
		}
		// Without a meta viewport tag a mobile-emulated page falls back to the
		// 980px layout width, so measure on a document a real app would ship.
		if _, err := Navigate(page, dataURL(responsiveTestDocument), "load"); err != nil {
			t.Fatalf("navigate: %v", err)
		}

		width, err := EvalJS(page, `window.innerWidth`, "", nil)
		if err != nil {
			t.Fatalf("eval innerWidth: %v", err)
		}
		height, err := EvalJS(page, `window.innerHeight`, "", nil)
		if err != nil {
			t.Fatalf("eval innerHeight: %v", err)
		}
		dpr, err := EvalJS(page, `window.devicePixelRatio`, "", nil)
		if err != nil {
			t.Fatalf("eval devicePixelRatio: %v", err)
		}
		if width != "390" || height != "844" {
			t.Fatalf("restored viewport = %sx%s, want 390x844", width, height)
		}
		if dpr != "3" {
			t.Fatalf("restored devicePixelRatio = %s, want 3", dpr)
		}
	})

	t.Run("foreign connect leaves the tab untouched", func(t *testing.T) {
		b, err := NewBrowserWith(BrowserOpts{ConnectURL: controlURL, TimeoutSec: 10})
		if err != nil {
			t.Fatalf("new browser: %v", err)
		}
		defer b.Close()
		page, err := b.Page()
		if err != nil {
			t.Fatalf("page: %v", err)
		}
		if _, err := Navigate(page, dataURL(responsiveTestDocument), "load"); err != nil {
			t.Fatalf("navigate: %v", err)
		}

		width, err := EvalJS(page, `window.innerWidth`, "", nil)
		if err != nil {
			t.Fatalf("eval innerWidth: %v", err)
		}
		if width == "390" {
			t.Fatal("emulation replayed on a non-managed connection")
		}
		if state := b.EmulationState(); !state.Empty() {
			t.Fatalf("non-managed browser exposed emulation state %+v", state)
		}
		if err := b.SetEmulationState(EmulationFromDevice(device)); err != nil {
			t.Fatalf("SetEmulationState on non-managed browser: %v", err)
		}
	})
}
