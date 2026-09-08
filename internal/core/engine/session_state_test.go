package engine

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestSessionStateMergesStaleWriters(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	a, err := loadSessionState(path)
	if err != nil {
		t.Fatal(err)
	}
	b, err := loadSessionState(path)
	if err != nil {
		t.Fatal(err)
	}
	a.Emulation = EmulationState{Width: 390, Height: 844}
	a.Snapshots["a"] = PageSnapshot{TargetID: "a", URL: "https://a.test"}
	if err := saveSessionState(path, a); err != nil {
		t.Fatal(err)
	}
	b.CurrentTargetID = "b"
	b.Snapshots["b"] = PageSnapshot{TargetID: "b", URL: "https://b.test"}
	if err := saveSessionState(path, b); err != nil {
		t.Fatal(err)
	}
	got, err := loadSessionState(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Emulation.Width != 390 || got.CurrentTargetID != "b" || len(got.Snapshots) != 2 {
		t.Fatalf("independent changes lost: %+v", got)
	}
	// Explicit removal by a stale reader must not remove another tab.
	delete(a.Snapshots, "a")
	a.Emulation = EmulationState{}
	if err := saveSessionState(path, a); err != nil {
		t.Fatal(err)
	}
	got, err = loadSessionState(path)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Emulation.Empty() || got.CurrentTargetID != "b" || len(got.Snapshots) != 1 || got.Snapshots["b"].TargetID != "b" {
		t.Fatalf("merge did not preserve explicit deletion and independent update: %+v", got)
	}
}

func TestSetVideoStateRestoresInMemoryStateWhenPersistenceFails(t *testing.T) {
	previous := VideoState{Active: true, Filename: "previous.webm"}
	browser := &Browser{
		connected: true,
		statePath: t.TempDir(), // Rename onto a directory fails after writing the temp state.
		state:     &sessionState{Video: previous},
	}
	if err := browser.SetVideoState(VideoState{Active: true, Filename: "next.webm"}); err == nil {
		t.Fatal("expected session-state persistence error")
	}
	if got := browser.VideoState(); !reflect.DeepEqual(got, previous) {
		t.Fatalf("VideoState() = %#v, want %#v", got, previous)
	}
}
