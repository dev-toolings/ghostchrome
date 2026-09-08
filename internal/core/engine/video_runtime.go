package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dev-toolings/ghostchrome/internal/core/media"
	"github.com/go-rod/rod/lib/proto"
)

const videoRuntimeStatusFilename = ".ghostchrome-video-runtime.json"

const (
	videoRuntimeHeartbeatInterval = time.Second
	// VideoRuntimeStaleAfter bounds how long a persisted recording state can
	// block a new recording when its detached runtime disappeared without
	// publishing a terminal status.
	VideoRuntimeStaleAfter = 5 * time.Second
)

// VideoRuntimeOpts describes the detached recorder process attached to a
// long-lived Chrome. The process, rather than the short-lived CLI command,
// owns the Page.screencast subscription.
type VideoRuntimeOpts struct {
	ConnectURL string
	TargetID   string
	FramesDir  string
	StatusPath string
}

// VideoRuntimeStatus is written atomically so video-start and video-stop can
// prove the detached recorder's state across CLI processes.
type VideoRuntimeStatus struct {
	State     string `json:"state"`
	Frames    int64  `json:"frames_captured"`
	StartedAt string `json:"started_at,omitempty"`
	UpdatedAt string `json:"updated_at,omitempty"`
	StoppedAt string `json:"stopped_at,omitempty"`
	Error     string `json:"error,omitempty"`
}

// IsStale reports whether a recording runtime has stopped publishing its
// heartbeat. Terminal statuses are handled by callers as recoverable too.
func (s VideoRuntimeStatus) IsStale(now time.Time, maxAge time.Duration) bool {
	if (s.State != "recording" && s.State != "starting") || maxAge <= 0 {
		return false
	}
	timestamp := s.UpdatedAt
	if timestamp == "" {
		timestamp = s.StartedAt
	}
	updatedAt, err := time.Parse(time.RFC3339, timestamp)
	if err != nil {
		return true
	}
	return now.After(updatedAt) && now.Sub(updatedAt) > maxAge
}

func VideoRuntimeStatusPath(framesDir string) string {
	return filepath.Join(framesDir, videoRuntimeStatusFilename)
}

func VideoRuntimeStopPath(statusPath string) string {
	return statusPath + ".stop"
}

// RunVideoRuntime continuously records the requested target until a stop file
// is requested. A file control channel is portable and survives independent
// CLI processes without trusting a reused PID.
func RunVideoRuntime(ctx context.Context, opts VideoRuntimeOpts) (err error) {
	if strings.TrimSpace(opts.ConnectURL) == "" || strings.TrimSpace(opts.TargetID) == "" || strings.TrimSpace(opts.FramesDir) == "" || strings.TrimSpace(opts.StatusPath) == "" {
		return fmt.Errorf("video runtime requires connect URL, target ID, frames directory, and status path")
	}
	if err := os.MkdirAll(opts.FramesDir, 0o700); err != nil {
		return fmt.Errorf("video runtime mkdir: %w", err)
	}
	_ = os.Remove(VideoRuntimeStopPath(opts.StatusPath))
	if err := writeVideoRuntimeStatus(opts.StatusPath, VideoRuntimeStatus{State: "starting"}); err != nil {
		return err
	}
	var recorder *media.ScreenRecorder
	var startedAt time.Time
	defer func() {
		if err == nil {
			return
		}
		frames := int64(0)
		if recorder != nil {
			frames = recorder.Stop()
		}
		status := VideoRuntimeStatus{State: "failed", Frames: frames, Error: err.Error()}
		if !startedAt.IsZero() {
			now := time.Now().UTC().Format(time.RFC3339)
			status.StartedAt = startedAt.Format(time.RFC3339)
			status.UpdatedAt = now
			status.StoppedAt = now
		}
		_ = writeVideoRuntimeStatus(opts.StatusPath, status)
	}()

	browser, err := connectRodBrowser(opts.ConnectURL, 15*time.Second, 0, nil)
	if err != nil {
		return fmt.Errorf("video runtime connect: %w", err)
	}
	// Do not call browser.Close(): Rod maps it to Browser.close, which would
	// terminate the managed daemon Chrome instead of only disconnecting us.
	page, err := browser.PageFromTarget(proto.TargetTargetID(opts.TargetID))
	if err != nil {
		return fmt.Errorf("video runtime target %q: %w", opts.TargetID, err)
	}
	// Chrome emits at most an initial screencast frame for a background target.
	// The managed-session tab is ours, so foreground it before subscribing to
	// keep the renderer producing frames throughout the detached runtime.
	if err := (proto.PageBringToFront{}).Call(page); err != nil {
		return fmt.Errorf("video runtime bring target to front: %w", err)
	}

	recorder = media.NewScreenRecorder(page, media.ScreenRecorderOpts{OutputDir: opts.FramesDir})
	if err := recorder.Start(); err != nil {
		return err
	}
	startedAt = time.Now().UTC()
	if err := writeVideoRuntimeStatus(opts.StatusPath, VideoRuntimeStatus{
		State:     "recording",
		StartedAt: startedAt.Format(time.RFC3339),
		UpdatedAt: startedAt.Format(time.RFC3339),
	}); err != nil {
		_ = recorder.Stop()
		recorder = nil
		return err
	}

	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	lastHeartbeat := startedAt
	for {
		select {
		case <-ctx.Done():
			frames := recorder.Stop()
			now := time.Now().UTC().Format(time.RFC3339)
			return writeVideoRuntimeStatus(opts.StatusPath, VideoRuntimeStatus{
				State:     "stopped",
				Frames:    frames,
				StartedAt: startedAt.Format(time.RFC3339),
				UpdatedAt: now,
				StoppedAt: now,
			})
		case <-ticker.C:
			if _, aliveErr := browser.Timeout(time.Second).Version(); aliveErr != nil {
				frames := recorder.Stop()
				return writeVideoRuntimeStatus(opts.StatusPath, VideoRuntimeStatus{
					State:     "failed",
					Frames:    frames,
					StartedAt: startedAt.Format(time.RFC3339),
					UpdatedAt: time.Now().UTC().Format(time.RFC3339),
					StoppedAt: time.Now().UTC().Format(time.RFC3339),
					Error:     fmt.Sprintf("browser disconnected: %v", aliveErr),
				})
			}
			if _, statErr := os.Stat(VideoRuntimeStopPath(opts.StatusPath)); statErr == nil {
				frames := recorder.Stop()
				return writeVideoRuntimeStatus(opts.StatusPath, VideoRuntimeStatus{
					State:     "stopped",
					Frames:    frames,
					StartedAt: startedAt.Format(time.RFC3339),
					UpdatedAt: time.Now().UTC().Format(time.RFC3339),
					StoppedAt: time.Now().UTC().Format(time.RFC3339),
				})
			} else if !os.IsNotExist(statErr) {
				return fmt.Errorf("video runtime check stop request: %w", statErr)
			}
			now := time.Now().UTC()
			if now.Sub(lastHeartbeat) >= videoRuntimeHeartbeatInterval {
				if err := writeVideoRuntimeStatus(opts.StatusPath, VideoRuntimeStatus{
					State:     "recording",
					Frames:    recorder.FrameCount(),
					StartedAt: startedAt.Format(time.RFC3339),
					UpdatedAt: now.Format(time.RFC3339),
				}); err != nil {
					return fmt.Errorf("video runtime heartbeat: %w", err)
				}
				lastHeartbeat = now
			}
		}
	}
}

func ReadVideoRuntimeStatus(path string) (VideoRuntimeStatus, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return VideoRuntimeStatus{}, err
	}
	var status VideoRuntimeStatus
	if err := json.Unmarshal(data, &status); err != nil {
		return VideoRuntimeStatus{}, fmt.Errorf("parse video runtime status: %w", err)
	}
	return status, nil
}

func WaitForVideoRuntime(path, want string, timeout time.Duration) (VideoRuntimeStatus, error) {
	deadline := time.Now().Add(timeout)
	var last VideoRuntimeStatus
	var lastErr error
	for time.Now().Before(deadline) {
		status, err := ReadVideoRuntimeStatus(path)
		if err == nil {
			last = status
			if status.State == want {
				return status, nil
			}
			if status.State == "failed" {
				return status, fmt.Errorf("video runtime failed: %s", status.Error)
			}
		} else if !os.IsNotExist(err) {
			lastErr = err
		}
		time.Sleep(25 * time.Millisecond)
	}
	if last.State != "" {
		return last, fmt.Errorf("video runtime did not reach %q before %s (last state %q)", want, timeout, last.State)
	}
	if lastErr != nil {
		return VideoRuntimeStatus{}, lastErr
	}
	return VideoRuntimeStatus{}, fmt.Errorf("video runtime did not create status before %s", timeout)
}

// RequestVideoRuntimeStop asks the detached recorder to finish its current
// frame, stop CDP screencast, and publish its final count.
func RequestVideoRuntimeStop(statusPath string, timeout time.Duration) (VideoRuntimeStatus, error) {
	if err := os.WriteFile(VideoRuntimeStopPath(statusPath), []byte("stop\n"), 0o600); err != nil {
		return VideoRuntimeStatus{}, fmt.Errorf("request video runtime stop: %w", err)
	}
	return WaitForVideoRuntime(statusPath, "stopped", timeout)
}

func writeVideoRuntimeStatus(path string, status VideoRuntimeStatus) error {
	data, err := json.Marshal(status)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".video-runtime-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}
