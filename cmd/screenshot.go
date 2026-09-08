package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/dev-toolings/ghostchrome/engine"
	"github.com/dev-toolings/ghostchrome/internal/core/media"
	"github.com/spf13/cobra"
)

var (
	flagFull                bool
	flagElement             string
	flagQuality             int
	flagOutputPath          string
	flagScreenshotBaseline  string
	flagScreenshotThreshold float64
	flagScreenshotTolerance int
	flagScreenshotDiffOut   string
	flagScreenshotUpdate    bool
	flagAnnotate            bool
	flagScale               float64
	flagHires               bool
)

var screenshotCmd = &cobra.Command{
	Use:   "screenshot [url|ref]",
	Short: "Capture a screenshot of the page or an element",
	Long: `Take a screenshot of the current page, full page, or a specific element.
If a URL is provided, navigates first then captures.

Examples:
  ghostchrome screenshot https://example.com
  ghostchrome screenshot https://example.com --full
  ghostchrome screenshot @3 --connect ws://...
  ghostchrome screenshot --element @3 --connect ws://...
  ghostchrome screenshot https://example.com --output page.png --quality 90`,
	Args: cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		if flagHires && cmd.Flags().Changed("scale") && flagScale != 1 {
			exitErr("screenshot", fmt.Errorf("--hires uses native device pixels and cannot be combined with --scale=%g", flagScale))
		}
		targetURL := ""
		if len(args) > 0 {
			if isSnapshotRef(args[0]) || !looksLikeURL(args[0]) {
				if flagElement != "" && flagElement != args[0] {
					exitErr("screenshot", fmt.Errorf("element specified twice: %s and %s", flagElement, args[0]))
				}
				flagElement = args[0]
			} else {
				targetURL = args[0]
			}
		}

		b, page := openPage()
		defer b.Close()

		var snapshot *engine.PageSnapshot
		if isSnapshotRef(flagElement) || targetURL != "" || flagAnnotate {
			snapshot = ensureSnapshot(b, page, targetURL, "load", engine.LevelSkeleton)
		}

		quality := flagQuality
		if flagAnnotate {
			quality = 0
		}
		data, err := engine.TakeScreenshotScaled(page, flagFull, flagElement, quality, flagScale, snapshot)
		if err != nil {
			exitIfStaleRef(err, "screenshot")
			exitErr("screenshot", err)
		}

		if flagAnnotate && snapshot != nil {
			data, err = engine.AnnotateScreenshot(page, snapshot, data)
			if err != nil {
				exitErr("annotate", err)
			}
		}

		outPath := flagOutputPath
		if outPath == "" {
			ext := "png"
			if !flagAnnotate && flagQuality > 0 && flagElement == "" {
				ext = "jpg"
			}
			dir, err := defaultScreenshotDir()
			if err != nil {
				exitErr("screenshot dir", err)
			}
			outPath = filepath.Join(dir, fmt.Sprintf("ghostchrome-screenshot-%d.%s", time.Now().UnixMilli(), ext))
		} else {
			// Validate user-supplied output path
			validated, err := validateOutputPath(outPath)
			if err != nil {
				exitErr("output path", err)
			}
			outPath = validated
		}

		// Write file with owner-only permissions (may contain sensitive page content).
		err = os.WriteFile(outPath, data, 0o600)
		if err != nil {
			exitErr("write screenshot", err)
		}

		sizeBytes := len(data)

		type screenshotResult struct {
			Path      string                 `json:"path"`
			SizeBytes int                    `json:"size_bytes"`
			Diff      *media.ImageDiffResult `json:"diff,omitempty"`
		}

		var diffResult *media.ImageDiffResult
		if flagScreenshotBaseline != "" {
			diffResult = runScreenshotDiff(outPath, data)
		}

		text := fmt.Sprintf("Screenshot saved to %s (%d bytes)", outPath, sizeBytes)
		if diffResult != nil {
			text += fmt.Sprintf(" — diff %.2f%% (%d/%d px)", diffResult.DiffRatio*100, diffResult.PixelsChanged, diffResult.PixelsTotal)
			if diffResult.DiffPath != "" {
				text += " → " + diffResult.DiffPath
			}
		}
		output(&screenshotResult{Path: outPath, SizeBytes: sizeBytes, Diff: diffResult}, text)
	},
}

func defaultScreenshotDir() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		base = os.TempDir()
	}
	dir := filepath.Join(base, "ghostchrome", "screenshots")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

// runScreenshotDiff compares currentPNG to the baseline file on disk and
// exits 1 when the diff ratio exceeds --threshold. If --update is set, the
// current PNG replaces the baseline and the function returns nil.
func runScreenshotDiff(currentPath string, current []byte) *media.ImageDiffResult {
	// Validate baseline path
	baselinePath, err := validateOutputPath(flagScreenshotBaseline)
	if err != nil {
		exitErr("baseline path", err)
	}

	if flagScreenshotUpdate {
		if err := os.WriteFile(baselinePath, current, 0o600); err != nil {
			exitErr("screenshot baseline update", err)
		}
		fmt.Fprintf(os.Stderr, "[screenshot] baseline updated: %s\n", baselinePath)
		return nil
	}

	baseline, err := os.ReadFile(baselinePath)
	if err != nil {
		if os.IsNotExist(err) {
			if err := os.WriteFile(baselinePath, current, 0o600); err != nil {
				exitErr("screenshot baseline create", err)
			}
			fmt.Fprintf(os.Stderr, "[screenshot] baseline created: %s (first run)\n", baselinePath)
			return nil
		}
		exitErr("screenshot baseline read", err)
	}

	diffPath := flagScreenshotDiffOut
	if diffPath == "" {
		diffPath = currentPath + ".diff.png"
	} else {
		// Validate diff-output path if user-supplied
		validatedDiffPath, err := validateOutputPath(diffPath)
		if err != nil {
			exitErr("diff-output path", err)
		}
		diffPath = validatedDiffPath
	}

	res, err := media.DiffImages(baseline, current, flagScreenshotTolerance, diffPath)
	if err != nil {
		exitErr("screenshot diff", err)
	}
	if res.Skipped {
		fmt.Fprintf(os.Stderr, "FAIL [screenshot] %s\n", res.SkipReason)
		exitNow(1)
	}
	if res.DiffRatio > flagScreenshotThreshold {
		fmt.Fprintf(os.Stderr, "FAIL [screenshot] diff %.4f > threshold %.4f (%d/%d px)\n",
			res.DiffRatio, flagScreenshotThreshold, res.PixelsChanged, res.PixelsTotal)
		exitNow(1)
	}
	return res
}

func init() {
	screenshotCmd.Flags().BoolVar(&flagFull, "full", false, "Capture full scrollable page")
	screenshotCmd.Flags().BoolVar(&flagFull, "full-page", false, "Capture full scrollable page (Playwright CLI-compatible alias)")
	screenshotCmd.Flags().StringVar(&flagElement, "element", "", "Capture specific element by @ref")
	screenshotCmd.Flags().IntVar(&flagQuality, "quality", 80, "JPEG quality 1-100 (PNG if <= 0)")
	screenshotCmd.Flags().StringVar(&flagOutputPath, "output", "", "Output file path (default: $XDG_CACHE_HOME/ghostchrome/screenshots/*.png)")
	screenshotCmd.Flags().StringVar(&flagOutputPath, "filename", "", "Output file path (Playwright CLI-compatible alias for --output)")
	screenshotCmd.Flags().StringVar(&flagScreenshotBaseline, "baseline", "", "Compare the new screenshot to this PNG (creates it on first run)")
	screenshotCmd.Flags().Float64Var(&flagScreenshotThreshold, "threshold", 0.02, "Max acceptable diff ratio (default 2%%) — exit 1 if exceeded")
	screenshotCmd.Flags().IntVar(&flagScreenshotTolerance, "tolerance", 4, "Per-channel color tolerance (0-255) before a pixel counts as changed")
	screenshotCmd.Flags().StringVar(&flagScreenshotDiffOut, "diff-output", "", "Path for the diff overlay PNG (default: <output>.diff.png)")
	screenshotCmd.Flags().BoolVar(&flagScreenshotUpdate, "update", false, "Overwrite the baseline with the new screenshot (no diff)")
	screenshotCmd.Flags().BoolVar(&flagAnnotate, "annotate", false, "Overlay numbered borders on interactive elements (forces PNG output)")
	screenshotCmd.Flags().Float64Var(&flagScale, "scale", 1.0, "Device scale factor for full-page captures (0.5 = half resolution, smaller file)")
	screenshotCmd.Flags().BoolVar(&flagHires, "hires", false, "Capture using native device pixels (the default; Playwright CLI-compatible alias)")
	rootCmd.AddCommand(screenshotCmd)
}
