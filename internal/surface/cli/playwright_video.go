package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/dev-toolings/ghostchrome/internal/core/engine"
	"github.com/spf13/cobra"
)

var (
	flagVideoSize        string
	flagVideoDescription string
	flagVideoDuration    int
)

var videoStartCmd = &cobra.Command{
	Use:   "video-start [filename]",
	Short: "Start video recording with frame capture",
	Long: `Start video recording for the active session. A detached runtime captures
JPEG frames via CDP screencast until video-stop is called from any CLI process.
The artifact is a JPEG frame sequence, not a WebM file.`,
	Args: cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		b, page := openPage()
		defer b.Close()
		if !b.Connected() {
			exitErr("video-start", fmt.Errorf("requires -s/--session, --connect, or an attached default session"))
		}
		if state := b.VideoState(); state.Active {
			if stale, reason := videoStateNeedsRecovery(state, time.Now().UTC()); stale {
				if err := b.SetVideoState(engine.VideoState{}); err != nil {
					exitErr("video-start", fmt.Errorf("clear %s video state: %w", reason, err))
				}
				fmt.Fprintf(os.Stderr, "warning: cleared %s video state before starting a new recording\n", reason)
			} else {
				exitErr("video-start", fmt.Errorf("video already active since %s", state.StartedAt))
			}
		}
		filename := "video.webm"
		if len(args) > 0 && strings.TrimSpace(args[0]) != "" {
			filename = args[0]
		}
		startedAt := time.Now().UTC()
		framesDir := videoFramesDir(filename, startedAt)
		statusPath := engine.VideoRuntimeStatusPath(framesDir)
		if err := startVideoRuntime(b.ConnectURL(), string(page.TargetID), framesDir, statusPath); err != nil {
			exitErr("video-start", err)
		}

		state := engine.VideoState{
			Active:        true,
			StartedAt:     startedAt.Format(time.RFC3339),
			Filename:      filename,
			FramesDir:     framesDir,
			RuntimeStatus: statusPath,
			Size:          flagVideoSize,
		}
		if err := b.SetVideoState(state); err != nil {
			cleanupErr := stopFailedVideoRuntime(b, statusPath)
			exitErr("video-start", errors.Join(err, cleanupErr))
		}
		output(map[string]any{
			"active":             true,
			"requested_filename": state.Filename,
			"artifact_format":    "jpeg-frame-sequence",
			"webm_created":       false,
			"frames_dir":         framesDir,
			"size":               state.Size,
		}, fmt.Sprintf("video recording started → JPEG frames in %s (no WebM is created)", framesDir))
	},
}

var videoChapterCmd = &cobra.Command{
	Use:   "video-chapter <title>",
	Short: "Add a video chapter marker",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		b, _ := openPage()
		defer b.Close()
		if !b.Connected() {
			exitErr("video-chapter", fmt.Errorf("requires -s/--session, --connect, or an attached default session"))
		}
		state := b.VideoState()
		if !state.Active {
			exitErr("video-chapter", fmt.Errorf("video recording is not active"))
		}
		atMs := elapsedVideoMs(state.StartedAt, time.Now().UTC())
		chapter := engine.VideoChapter{
			Title:       args[0],
			Description: flagVideoDescription,
			DurationMs:  flagVideoDuration,
			AtMs:        atMs,
			CreatedAt:   time.Now().UTC().Format(time.RFC3339),
		}
		state.Chapters = append(state.Chapters, chapter)
		if err := b.SetVideoState(state); err != nil {
			exitErr("video-chapter", err)
		}
		output(chapter, fmt.Sprintf("video chapter added: %s", chapter.Title))
	},
}

var videoStopCmd = &cobra.Command{
	Use:   "video-stop",
	Short: "Stop video recording and write manifest",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		b, _ := openPage()
		defer b.Close()
		if !b.Connected() {
			exitErr("video-stop", fmt.Errorf("requires -s/--session, --connect, or an attached default session"))
		}
		state := b.VideoState()
		if !state.Active {
			exitErr("video-stop", fmt.Errorf("video is not active"))
		}

		runtime, runtimeErr := stopVideoRuntime(state)
		manifestPath := videoManifestPath(state.Filename)
		manifestErr := writeVideoManifest(manifestPath, state, time.Now().UTC(), runtime, state.FramesDir)
		clearErr := b.SetVideoState(engine.VideoState{})
		if err := errors.Join(runtimeErr, manifestErr, clearErr); err != nil {
			exitErr("video-stop", err)
		}
		type videoStopResult struct {
			Manifest          string `json:"manifest"`
			ArtifactFormat    string `json:"artifact_format"`
			WebMCreated       bool   `json:"webm_created"`
			RequestedFilename string `json:"requested_filename"`
			Chapters          int    `json:"chapters"`
			Frames            int64  `json:"frames_captured"`
			FramesDir         string `json:"frames_dir"`
		}
		result := videoStopResult{
			Manifest:          manifestPath,
			ArtifactFormat:    "jpeg-frame-sequence",
			WebMCreated:       false,
			RequestedFilename: state.Filename,
			Chapters:          len(state.Chapters),
			Frames:            runtime.Frames,
			FramesDir:         state.FramesDir,
		}
		text := fmt.Sprintf("video stopped: %d JPEG frames captured in %s (no WebM created)", runtime.Frames, state.FramesDir)
		output(result, text)
	},
}

var flagVideoShowDuration int
var flagVideoShowPosition string
var flagVideoShowCursor string

var videoShowActionsCompatCmd = &cobra.Command{
	Use:   "video-show-actions",
	Short: "Display action markers during video capture (unsupported in ghostchrome)",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		unsupportedPlaywrightCommand("video-show-actions", args, "ghostchrome captures raw JPEG screencast frames and has no compositor for action overlays", "Use trace tooling for rendered action cards; the --duration, --position, and --cursor flags remain accepted for CLI compatibility.")
	},
}

var videoHideActionsCompatCmd = &cobra.Command{
	Use:   "video-hide-actions",
	Short: "Hide action markers during video capture (unsupported in ghostchrome)",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		unsupportedPlaywrightCommand("video-hide-actions", args, "ghostchrome does not render action overlays into its raw JPEG screencast frames", "Use trace tooling for rendered action cards.")
	},
}

func elapsedVideoMs(startedAt string, now time.Time) int64 {
	started, err := time.Parse(time.RFC3339, startedAt)
	if err != nil {
		return 0
	}
	if now.Before(started) {
		return 0
	}
	return now.Sub(started).Milliseconds()
}

func videoManifestPath(filename string) string {
	base := strings.TrimSuffix(filepath.Base(filename), filepath.Ext(filename))
	if base == "" || base == "." {
		base = "video"
	}
	return playwrightArtifactPath(base + ".video.json")
}

func videoFramesDir(filename string, startedAt time.Time) string {
	base := strings.TrimSuffix(filepath.Base(filename), filepath.Ext(filename))
	if base == "" || base == "." {
		base = "video"
	}
	return playwrightArtifactPath(fmt.Sprintf("%s-%s.frames", base, startedAt.UTC().Format("20060102T150405.000Z")))
}

func writeVideoManifest(path string, state engine.VideoState, stoppedAt time.Time, runtime engine.VideoRuntimeStatus, framesDir string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	payload := map[string]any{
		"requested_filename":    state.Filename,
		"artifact_format":       "jpeg-frame-sequence",
		"webm_created":          false,
		"requested_size":        state.Size,
		"auto_started":          state.Auto,
		"source":                state.Source,
		"started_at":            state.StartedAt,
		"stopped_at":            stoppedAt.UTC().Format(time.RFC3339),
		"chapters":              state.Chapters,
		"frames_captured":       runtime.Frames,
		"frames_dir":            framesDir,
		"runtime_state":         runtime.State,
		"runtime_error":         runtime.Error,
		"recording_complete":    runtime.State == "stopped",
		"playwright_compatible": false,
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
}

func videoStateNeedsRecovery(state engine.VideoState, now time.Time) (bool, string) {
	if state.RuntimeStatus == "" || state.FramesDir == "" {
		return true, "legacy"
	}
	status, err := engine.ReadVideoRuntimeStatus(state.RuntimeStatus)
	if err != nil {
		return true, "missing"
	}
	if status.State == "failed" || status.State == "stopped" {
		return true, status.State
	}
	if status.IsStale(now, engine.VideoRuntimeStaleAfter) {
		return true, "stale"
	}
	return false, ""
}

func stopVideoRuntime(state engine.VideoState) (engine.VideoRuntimeStatus, error) {
	if state.RuntimeStatus == "" || state.FramesDir == "" {
		const message = "video recording has no detached runtime; it may have been started by an older ghostchrome binary"
		return engine.VideoRuntimeStatus{State: "unavailable", Error: message}, errors.New(message)
	}
	runtime, err := engine.RequestVideoRuntimeStop(state.RuntimeStatus, 10*time.Second)
	if err != nil && runtime.State == "" {
		runtime.State = "unknown"
		runtime.Error = err.Error()
	}
	return runtime, err
}

func stopFailedVideoRuntime(b *engine.Browser, statusPath string) error {
	_, runtimeErr := engine.RequestVideoRuntimeStop(statusPath, 2*time.Second)
	clearErr := b.SetVideoState(engine.VideoState{})
	return errors.Join(runtimeErr, clearErr)
}

func autoStartVideoIfConfigured(b *engine.Browser) {
	if flagAutoVideoSize == "" || b == nil || !b.Connected() {
		return
	}
	fmt.Fprintf(os.Stderr, "warning: %s=%s requests automatic video, but automatic recording is unsupported; run video-start explicitly (JPEG frames only, no WebM)\n", flagAutoVideoSource, flagAutoVideoSize)
}

var videoRuntimeCmd = &cobra.Command{
	Use:    "video-runtime",
	Hidden: true,
	Args:   cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
		defer stop()
		if err := engine.RunVideoRuntime(ctx, engine.VideoRuntimeOpts{
			ConnectURL: flagConnect,
			TargetID:   flagVideoRuntimeTarget,
			FramesDir:  flagVideoRuntimeFramesDir,
			StatusPath: flagVideoRuntimeStatus,
		}); err != nil {
			fmt.Fprintf(os.Stderr, "video runtime: %v\n", err)
			return
		}
	},
}

var (
	flagVideoRuntimeTarget    string
	flagVideoRuntimeFramesDir string
	flagVideoRuntimeStatus    string
)

func startVideoRuntime(connectURL, targetID, framesDir, statusPath string) error {
	if strings.TrimSpace(connectURL) == "" || strings.TrimSpace(targetID) == "" {
		return fmt.Errorf("start detached video runtime: connect URL and target ID are required")
	}
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate ghostchrome executable: %w", err)
	}
	if err := os.MkdirAll(framesDir, 0o700); err != nil {
		return fmt.Errorf("create video frames directory: %w", err)
	}
	_ = os.Remove(statusPath)
	_ = os.Remove(engine.VideoRuntimeStopPath(statusPath))
	logFile, err := os.OpenFile(filepath.Join(framesDir, "runtime.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open video runtime log: %w", err)
	}
	defer logFile.Close()
	runtime := exec.Command(exe,
		"--connect", connectURL,
		"video-runtime",
		"--target-id", targetID,
		"--frames-dir", framesDir,
		"--status", statusPath,
	)
	runtime.Stdout = logFile
	runtime.Stderr = logFile
	runtime.Env = os.Environ()
	runtime.SysProcAttr = engine.DetachSysProcAttr()
	if err := runtime.Start(); err != nil {
		return fmt.Errorf("start detached video runtime: %w", err)
	}
	if err := runtime.Process.Release(); err != nil {
		_, cleanupErr := engine.RequestVideoRuntimeStop(statusPath, 2*time.Second)
		return fmt.Errorf("detach video runtime: %w", errors.Join(err, cleanupErr))
	}
	_, err = engine.WaitForVideoRuntime(statusPath, "recording", 10*time.Second)
	if err != nil {
		_, cleanupErr := engine.RequestVideoRuntimeStop(statusPath, 2*time.Second)
		return errors.Join(err, cleanupErr)
	}
	return nil
}

func init() {
	videoStartCmd.Flags().StringVar(&flagVideoSize, "size", "", "Requested video size, e.g. 800x600")
	videoChapterCmd.Flags().StringVar(&flagVideoDescription, "description", "", "Chapter description")
	videoChapterCmd.Flags().IntVar(&flagVideoDuration, "duration", 0, "Chapter card duration in milliseconds")
	videoShowActionsCompatCmd.Flags().IntVar(&flagVideoShowDuration, "duration", 500, "Action-card metadata duration in milliseconds")
	videoShowActionsCompatCmd.Flags().StringVar(&flagVideoShowPosition, "position", "top-right", "Action-card metadata position")
	videoShowActionsCompatCmd.Flags().StringVar(&flagVideoShowCursor, "cursor", "pointer", "Action cursor metadata")
	videoRuntimeCmd.Flags().StringVar(&flagVideoRuntimeTarget, "target-id", "", "internal video runtime target")
	videoRuntimeCmd.Flags().StringVar(&flagVideoRuntimeFramesDir, "frames-dir", "", "internal video runtime frame directory")
	videoRuntimeCmd.Flags().StringVar(&flagVideoRuntimeStatus, "status", "", "internal video runtime status path")
	rootCmd.AddCommand(videoStartCmd, videoChapterCmd, videoStopCmd, videoShowActionsCompatCmd, videoHideActionsCompatCmd, videoRuntimeCmd)
	commandGroups["video-start"] = "observe"
	commandGroups["video-chapter"] = "observe"
	commandGroups["video-stop"] = "observe"
	commandGroups["video-show-actions"] = "observe"
	commandGroups["video-hide-actions"] = "observe"
}
