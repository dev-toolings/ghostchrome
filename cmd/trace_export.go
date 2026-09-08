package cmd

import (
	"fmt"
	"os"

	"github.com/dev-toolings/ghostchrome/internal/core/artifact"
	"github.com/spf13/cobra"
)

var (
	flagTraceExportFile   string
	flagTraceExportOutput string
)

var traceExportCmd = &cobra.Command{
	Use:   "trace-export",
	Short: "Render a MCP session trace as a scrubbable HTML viewer",
	Long: `Read a JSONL trace file and write a self-contained HTML viewer (single file,
no external deps) that can be opened directly in any browser.

Embeds per-action WebP screenshots as data URIs when the trace was recorded
with GHOSTCHROME_MCP_TRACE_SHOTS=1.

Examples:
  ghostchrome trace-export --file /tmp/session.jsonl
  ghostchrome trace-export --file /tmp/session.jsonl --output /tmp/viewer.html`,
	Run: func(cmd *cobra.Command, args []string) {
		if flagTraceExportFile == "" {
			exitErr("trace-export", fmt.Errorf("--file is required"))
		}
		out := flagTraceExportOutput
		if out == "" {
			out = flagTraceExportFile + ".html"
		}
		entries, err := artifact.ReadTrace(flagTraceExportFile, 0)
		if err != nil {
			exitErr("trace-export", err)
		}
		htmlContent := artifact.RenderTraceHTML(entries, flagTraceExportFile)
		if err := os.WriteFile(out, []byte(htmlContent), 0o600); err != nil {
			exitErr("trace-export write", err)
		}
		type exportResult struct {
			Path    string `json:"path"`
			Entries int    `json:"entries"`
			Bytes   int    `json:"bytes"`
		}
		res := exportResult{
			Path:    out,
			Entries: len(entries),
			Bytes:   len(htmlContent),
		}
		text := fmt.Sprintf("trace HTML written: %s (%d entries, %d bytes)", out, len(entries), len(htmlContent))
		output(res, text)
	},
}

func init() {
	traceExportCmd.Flags().StringVar(&flagTraceExportFile, "file", "", "Path to the JSONL trace file (required)")
	traceExportCmd.Flags().StringVar(&flagTraceExportOutput, "output", "", "Output HTML path (default: <file>.html)")
	rootCmd.AddCommand(traceExportCmd)
}
