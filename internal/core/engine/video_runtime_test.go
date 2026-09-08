package engine

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestVideoRuntimeStatusPaths(t *testing.T) {
	framesDir := filepath.Join("artifacts", "video.frames")
	status := VideoRuntimeStatusPath(framesDir)
	if want := filepath.Join(framesDir, ".ghostchrome-video-runtime.json"); status != want {
		t.Fatalf("VideoRuntimeStatusPath() = %q, want %q", status, want)
	}
	if want := status + ".stop"; VideoRuntimeStopPath(status) != want {
		t.Fatalf("VideoRuntimeStopPath() = %q, want %q", VideoRuntimeStopPath(status), want)
	}
}

func TestBrowserConnectURLReturnsResolvedEndpoint(t *testing.T) {
	browser := &Browser{connectURL: "ws://127.0.0.1:9222/devtools/browser/id"}
	if got := browser.ConnectURL(); got != "ws://127.0.0.1:9222/devtools/browser/id" {
		t.Fatalf("ConnectURL() = %q", got)
	}
}

func TestRequestVideoRuntimeStopWaitsForFinalFrameCount(t *testing.T) {
	statusPath := filepath.Join(t.TempDir(), "runtime.json")
	if err := writeVideoRuntimeStatus(statusPath, VideoRuntimeStatus{State: "recording", Frames: 4}); err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			if _, err := os.Stat(VideoRuntimeStopPath(statusPath)); err == nil {
				_ = writeVideoRuntimeStatus(statusPath, VideoRuntimeStatus{State: "stopped", Frames: 7})
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()

	status, err := RequestVideoRuntimeStop(statusPath, time.Second)
	if err != nil {
		t.Fatalf("RequestVideoRuntimeStop: %v", err)
	}
	if status.Frames != 7 {
		t.Fatalf("frames = %d, want 7", status.Frames)
	}
}

func TestWaitForVideoRuntimeReturnsRuntimeFailure(t *testing.T) {
	statusPath := filepath.Join(t.TempDir(), "runtime.json")
	if err := writeVideoRuntimeStatus(statusPath, VideoRuntimeStatus{State: "failed", Error: "CDP disconnected"}); err != nil {
		t.Fatal(err)
	}
	if _, err := WaitForVideoRuntime(statusPath, "recording", time.Second); err == nil {
		t.Fatal("expected runtime failure")
	}
}

func TestVideoRuntimeStatusIsStaleAfterHeartbeatExpires(t *testing.T) {
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	status := VideoRuntimeStatus{State: "recording", UpdatedAt: now.Add(-6 * time.Second).Format(time.RFC3339)}
	if !status.IsStale(now, 5*time.Second) {
		t.Fatal("expected stale recording status")
	}
	status.UpdatedAt = now.Add(-time.Second).Format(time.RFC3339)
	if status.IsStale(now, 5*time.Second) {
		t.Fatal("fresh recording status must remain active")
	}
}
