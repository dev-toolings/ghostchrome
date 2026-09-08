package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dev-toolings/ghostchrome/internal/core/engine"
)

func TestVideoManifestPath(t *testing.T) {
	restore := snapshotConfigGlobals()
	defer restore()
	got := videoManifestPath(".playwright-cli/demo.webm")
	want := filepath.Join(".playwright-cli", "demo.video.json")
	if got != want {
		t.Fatalf("videoManifestPath() = %q, want %q", got, want)
	}
}

func TestVideoManifestPathUsesConfiguredOutputDir(t *testing.T) {
	restore := snapshotConfigGlobals()
	defer restore()
	flagConfigOutputDir = filepath.Join("tmp", "pw-output")

	got := videoManifestPath(".playwright-cli/demo.webm")
	want := filepath.Join("tmp", "pw-output", "demo.video.json")
	if got != want {
		t.Fatalf("videoManifestPath() = %q, want %q", got, want)
	}
}

func TestElapsedVideoMs(t *testing.T) {
	start := "2026-06-13T12:00:00Z"
	now := time.Date(2026, 6, 13, 12, 0, 2, 500*int(time.Millisecond), time.UTC)
	if got := elapsedVideoMs(start, now); got != 2500 {
		t.Fatalf("elapsedVideoMs() = %d, want 2500", got)
	}
}

func TestVideoFramesDirUsesRecordingTimestamp(t *testing.T) {
	startedAt := time.Date(2026, 6, 13, 12, 0, 3, 456000000, time.UTC)
	got := videoFramesDir("demo.webm", startedAt)
	want := filepath.Join(".playwright-cli", "demo-20260613T120003.456Z.frames")
	if got != want {
		t.Fatalf("videoFramesDir() = %q, want %q", got, want)
	}
}

func TestWriteVideoManifest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "video.json")
	state := engine.VideoState{
		Filename:  ".playwright-cli/demo.webm",
		StartedAt: "2026-06-13T12:00:00Z",
		Size:      "800x600",
		Auto:      true,
		Source:    "config.saveVideo",
		Chapters:  []engine.VideoChapter{{Title: "Intro", AtMs: 1200}},
	}
	runtime := engine.VideoRuntimeStatus{State: "stopped", Frames: 42}
	if err := writeVideoManifest(path, state, time.Date(2026, 6, 13, 12, 0, 3, 0, time.UTC), runtime, "/tmp/frames"); err != nil {
		t.Fatalf("writeVideoManifest: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("parse manifest: %v", err)
	}
	if payload["frames_captured"] != float64(42) {
		t.Fatalf("expected frames_captured=42, got %#v", payload["frames_captured"])
	}
	if payload["playwright_compatible"] != false {
		t.Fatalf("expected playwright_compatible=false, got %#v", payload["playwright_compatible"])
	}
	if payload["auto_started"] != true {
		t.Fatalf("expected auto_started=true, got %#v", payload["auto_started"])
	}
	if payload["source"] != "config.saveVideo" {
		t.Fatalf("source = %#v", payload["source"])
	}
	if payload["artifact_format"] != "jpeg-frame-sequence" {
		t.Fatalf("artifact_format = %#v", payload["artifact_format"])
	}
	if payload["webm_created"] != false {
		t.Fatalf("webm_created = %#v", payload["webm_created"])
	}
	if payload["requested_filename"] != ".playwright-cli/demo.webm" {
		t.Fatalf("requested_filename = %#v", payload["requested_filename"])
	}
	if _, ok := payload["filename"]; ok {
		t.Fatalf("manifest must not claim a filename artifact: %#v", payload["filename"])
	}
	if payload["recording_complete"] != true {
		t.Fatalf("recording_complete = %#v", payload["recording_complete"])
	}
}

func TestWriteVideoManifestMarksRuntimeFailurePartial(t *testing.T) {
	path := filepath.Join(t.TempDir(), "video.json")
	state := engine.VideoState{Filename: "demo.webm", FramesDir: "/tmp/frames"}
	runtime := engine.VideoRuntimeStatus{State: "failed", Frames: 8, Error: "CDP disconnected"}
	if err := writeVideoManifest(path, state, time.Now().UTC(), runtime, state.FramesDir); err != nil {
		t.Fatalf("writeVideoManifest: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("parse manifest: %v", err)
	}
	if payload["recording_complete"] != false || payload["runtime_state"] != "failed" || payload["runtime_error"] != "CDP disconnected" {
		t.Fatalf("partial manifest = %#v", payload)
	}
}

func TestVideoStateNeedsRecoveryForFailedAndStaleRuntime(t *testing.T) {
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	statusPath := filepath.Join(t.TempDir(), "runtime.json")
	state := engine.VideoState{Active: true, FramesDir: filepath.Dir(statusPath), RuntimeStatus: statusPath}
	if err := writeVideoRuntimeStatusForTest(statusPath, engine.VideoRuntimeStatus{State: "failed", Error: "CDP disconnected"}); err != nil {
		t.Fatal(err)
	}
	if recover, reason := videoStateNeedsRecovery(state, now); !recover || reason != "failed" {
		t.Fatalf("failed runtime recovery = (%v, %q)", recover, reason)
	}
	if err := writeVideoRuntimeStatusForTest(statusPath, engine.VideoRuntimeStatus{
		State:     "recording",
		StartedAt: now.Add(-2 * engine.VideoRuntimeStaleAfter).Format(time.RFC3339),
		UpdatedAt: now.Add(-2 * engine.VideoRuntimeStaleAfter).Format(time.RFC3339),
	}); err != nil {
		t.Fatal(err)
	}
	if recover, reason := videoStateNeedsRecovery(state, now); !recover || reason != "stale" {
		t.Fatalf("stale runtime recovery = (%v, %q)", recover, reason)
	}
}

func TestVideoStateKeepsFreshRuntimeActive(t *testing.T) {
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	statusPath := filepath.Join(t.TempDir(), "runtime.json")
	state := engine.VideoState{Active: true, FramesDir: filepath.Dir(statusPath), RuntimeStatus: statusPath}
	if err := writeVideoRuntimeStatusForTest(statusPath, engine.VideoRuntimeStatus{
		State:     "recording",
		StartedAt: now.Add(-time.Second).Format(time.RFC3339),
		UpdatedAt: now.Add(-time.Second).Format(time.RFC3339),
	}); err != nil {
		t.Fatal(err)
	}
	if recover, reason := videoStateNeedsRecovery(state, now); recover || reason != "" {
		t.Fatalf("fresh runtime recovery = (%v, %q)", recover, reason)
	}
}

func writeVideoRuntimeStatusForTest(path string, status engine.VideoRuntimeStatus) error {
	data, err := json.Marshal(status)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}
