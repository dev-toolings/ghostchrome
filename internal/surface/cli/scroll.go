package cli

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/dev-toolings/ghostchrome/internal/core/engine"
	"github.com/spf13/cobra"
)

var scrollCmd = &cobra.Command{
	Use:   "scroll <ref>",
	Short: "Scroll an element into view by ref",
	Long: `Scroll the page so that the element at the given @ref becomes visible.
For absolute or relative scroll positions, see "scroll-to" and "scroll-by".

Examples:
  ghostchrome scroll @5
  ghostchrome scroll @12 --connect ws://...`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		ref := args[0]

		b, page := openPage()
		defer b.Close()

		snapshot := ensureSnapshot(b, page, "", "none", engine.LevelSkeleton)

		if err := engine.ScrollToRef(page, ref, snapshot); err != nil {
			exitIfStaleRef(err, "scroll")
			exitErr("scroll", err)
		}

		type scrollResult struct {
			Action string `json:"action"`
			Ref    string `json:"ref"`
		}
		output(&scrollResult{Action: "scroll", Ref: ref}, fmt.Sprintf("Scrolled to %s", ref))
	},
}

var scrollToCmd = &cobra.Command{
	Use:   "scroll-to <target>",
	Short: "Scroll to an absolute Y position or to 'top' / 'bottom'",
	Long: `Move the page scroll position. target can be:
  top       - window.scrollTo(0, 0)
  bottom    - document.body.scrollHeight
  <number>  - absolute pixel value on the Y axis

Examples:
  ghostchrome scroll-to bottom
  ghostchrome scroll-to top
  ghostchrome scroll-to 800`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		target := args[0]

		b, page := openPage()
		defer b.Close()

		y, err := scrollTargetToY(target)
		if err != nil {
			exitErr("scroll-to", err)
		}

		finalY, err := engine.ScrollToY(page, y, target == "bottom")
		if err != nil {
			exitErr("scroll-to", err)
		}

		type scrollToResult struct {
			Action string `json:"action"`
			Target string `json:"target"`
			Y      int    `json:"y"`
		}
		output(&scrollToResult{Action: "scroll-to", Target: target, Y: finalY},
			fmt.Sprintf("Scrolled to y=%d (%s)", finalY, target))
	},
}

var scrollByCmd = &cobra.Command{
	Use:   "scroll-by <dy>",
	Short: "Scroll by a relative Y offset (positive = down, negative = up)",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		dy, err := strconv.Atoi(args[0])
		if err != nil {
			exitErr("scroll-by", fmt.Errorf("parse dy: %w", err))
		}

		b, page := openPage()
		defer b.Close()

		finalY, err := engine.ScrollBy(page, dy)
		if err != nil {
			exitErr("scroll-by", err)
		}

		type scrollByResult struct {
			Action string `json:"action"`
			Dy     int    `json:"dy"`
			Y      int    `json:"y"`
		}
		output(&scrollByResult{Action: "scroll-by", Dy: dy, Y: finalY},
			fmt.Sprintf("Scrolled by %d → y=%d", dy, finalY))
	},
}

// scrollTargetToY parses "top", "bottom" or a pixel number. Returns the pixel
// Y intent (-1 for bottom since we let ScrollToY compute from scrollHeight).
func scrollTargetToY(target string) (int, error) {
	switch strings.ToLower(strings.TrimSpace(target)) {
	case "top":
		return 0, nil
	case "bottom":
		return -1, nil
	}
	n, err := strconv.Atoi(target)
	if err != nil {
		return 0, fmt.Errorf("expected 'top', 'bottom' or a number, got %q", target)
	}
	return n, nil
}

func init() {
	rootCmd.AddCommand(scrollCmd, scrollToCmd, scrollByCmd)
}
