package cli

import (
	"archive/zip"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

var flagTracingOutput string

var tracingStartCmd = &cobra.Command{
	Use:   "tracing-start",
	Short: "Start browser tracing (explicitly unsupported across CLI processes)",
	Long: `Tracing is an explicit compatibility boundary. CDP tracing is scoped to
the DevTools client that starts it, while ghostchrome commands use separate
short-lived clients. A truthful implementation therefore requires daemon-owned
capture, and the resulting CDP stream would still not be Playwright Trace Viewer
compatible.`,
	Args: cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		unsupportedPlaywrightCommand("tracing-start", args,
			"cross-process tracing requires a daemon-owned recorder and ghostchrome does not emit the Playwright Trace Viewer schema",
			"Use Chrome DevTools tracing for CDP diagnostics or Playwright CLI when a Trace Viewer artifact is required.")
	},
}

var tracingStopCmd = &cobra.Command{
	Use:   "tracing-stop",
	Short: "Stop browser tracing (explicitly unsupported across CLI processes)",
	Long: `Tracing is intentionally unavailable until the persistent daemon owns
the complete capture lifecycle and ghostchrome can emit an honest artifact.
Merely naming a CDP JSON archive trace.zip does not make it compatible with the
Playwright Trace Viewer.`,
	Args: cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		unsupportedPlaywrightCommand("tracing-stop", args,
			"no daemon-owned trace is active and a CDP JSON archive is not Playwright Trace Viewer-compatible",
			"Use Chrome DevTools tracing for CDP diagnostics or Playwright CLI when a Trace Viewer artifact is required.")
	},
}

func defaultTraceOutput(path string) string {
	if path != "" {
		return path
	}
	return playwrightArtifactPath("trace.zip")
}

func writeTraceJSON(path string, events []map[string]any, startedAt string, dataLoss bool) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	payload := map[string]any{
		"traceEvents":           events,
		"metadata":              map[string]any{"tool": "ghostchrome", "started_at": startedAt, "format": "cdp-json"},
		"data_loss_occurred":    dataLoss,
		"playwright_compatible": false,
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o600)
}

func writeTraceOutput(path string, events []map[string]any, startedAt string, dataLoss bool) (string, error) {
	if strings.EqualFold(filepath.Ext(path), ".zip") {
		return "ghostchrome-cdp-zip", writeTraceZip(path, events, startedAt, dataLoss)
	}
	return "cdp-json", writeTraceJSON(path, events, startedAt, dataLoss)
}

func writeTraceZip(path string, events []map[string]any, startedAt string, dataLoss bool) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()

	zw := zip.NewWriter(file)
	if err := zipJSON(zw, "cdp-trace.json", map[string]any{
		"traceEvents":        events,
		"data_loss_occurred": dataLoss,
	}); err != nil {
		_ = zw.Close()
		return err
	}
	if err := zipJSON(zw, "metadata.json", map[string]any{
		"tool":                    "ghostchrome",
		"started_at":              startedAt,
		"format":                  "ghostchrome-cdp-zip",
		"playwright_compatible":   false,
		"trace_viewer_compatible": false,
		"events":                  len(events),
	}); err != nil {
		_ = zw.Close()
		return err
	}
	if err := zipText(zw, "README.md", "This archive was written by ghostchrome.\n\nIt contains Chrome DevTools Protocol trace events in cdp-trace.json.\nIt is intentionally marked as not Playwright Trace Viewer-compatible until ghostchrome emits the real Playwright trace schema.\n"); err != nil {
		_ = zw.Close()
		return err
	}
	return zw.Close()
}

func zipJSON(zw *zip.Writer, name string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return zipText(zw, name, string(data))
}

func zipText(zw *zip.Writer, name string, text string) error {
	w, err := zw.Create(name)
	if err != nil {
		return err
	}
	_, err = w.Write([]byte(text))
	return err
}

func init() {
	tracingStopCmd.Flags().StringVar(&flagTracingOutput, "output", "", "Output trace path (default .playwright-cli/trace.zip; use .json for raw CDP JSON)")
	rootCmd.AddCommand(tracingStartCmd, tracingStopCmd)
	commandGroups["tracing-start"] = "observe"
	commandGroups["tracing-stop"] = "observe"
}
