package cmd

import (
	"github.com/dev-toolings/ghostchrome/engine"
	"github.com/dev-toolings/ghostchrome/internal/core/artifact"
	"github.com/go-rod/rod"
	"github.com/spf13/cobra"
)

var (
	flagPreviewLevel string
	flagPreviewHAR   string
	flagPreviewWait  string
)

var previewCmd = &cobra.Command{
	Use:   "preview <url>",
	Short: "Full page health report: status + errors + network + DOM",
	Long: `Preview gives a complete page analysis in one command.
Combines navigate + error collection + network monitoring + DOM extraction.

Perfect for LLM agents to verify their work after code changes.

Examples:
  ghostchrome preview http://localhost:3000
  ghostchrome preview http://localhost:3000 --level skeleton
  ghostchrome preview http://localhost:8000/admin --dismiss-cookies`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		targetURL := args[0]

		b, page := openPage()
		defer b.Close()

		applyStealthIfNeeded(page)

		level := engine.ExtractLevel(flagPreviewLevel)

		result, err := engine.Preview(page, targetURL, flagPreviewWait, level, func(page *rod.Page) error {
			dismissCookiesIfNeeded(page)
			return nil
		}, flagStealth)
		if err != nil {
			exitErr("preview", err)
		}

		if err := b.SaveSnapshot(page, result.DOM); err != nil {
			exitErr("snapshot", err)
		}

		if flagPreviewHAR != "" {
			har := artifact.BuildHAR(result.Network, result.PageInfo.URL, result.PageInfo.Title, appVersion())
			if err := artifact.WriteHAR(har, flagPreviewHAR); err != nil {
				exitErr("preview har", err)
			}
		}

		text := engine.FormatPreviewProfile(result, renderProfile())
		output(result, text)
	},
}

func init() {
	previewCmd.Flags().StringVar(&flagPreviewLevel, "level", "skeleton", "DOM extraction level: skeleton, content, or full")
	previewCmd.Flags().StringVar(&flagPreviewWait, "wait", "load", "Wait strategy: load, stable, idle, none")
	previewCmd.Flags().StringVar(&flagPreviewHAR, "har", "", "Also write a HAR 1.2 file with all network entries at this path")
	rootCmd.AddCommand(previewCmd)
}

// appVersion returns the CLI version (set by main via SetVersion, empty in go
// test or dev builds).
func appVersion() string {
	if v := rootCmd.Version; v != "" {
		return v
	}
	return "dev"
}
