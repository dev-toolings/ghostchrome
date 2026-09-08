package cmd

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/dev-toolings/ghostchrome/engine"
	"github.com/dev-toolings/ghostchrome/internal/core/artifact"
	"github.com/spf13/cobra"
)

var (
	flagRecordOutput         string
	flagRecordIncludeScrolls bool
	flagRecordIncludeHovers  bool
	flagRecordStopOnNavigate bool
)

var recordCmd = &cobra.Command{
	Use:   "record <url>",
	Short: "Record user actions in Chrome and emit agent-ready JSONL ops",
	Long: `Opens Chrome (or attaches via --connect), navigates to <url>, and listens
for user interactions. Each action is emitted as a JSONL op line compatible
with the agent op format consumed by "ghostchrome agent".

Emitted op types:
  navigate  {url}
  click     {target, byText?}
  type      {target, text}          (debounced — one op per field per settle)
  press     {key}                   (Enter, Tab, Escape only)
  select    {target, value}
  scroll_by {dy}                    (only with --include-scrolls)
  hover     {target}                (only with --include-hovers)

Output goes to stdout by default. Use --output to write to a file.
Stop with Ctrl-C; the file is flushed and closed cleanly.

Examples:
  ghostchrome record https://example.com --connect=auto
  ghostchrome record https://app.com --output /tmp/session.jsonl`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		url := args[0]

		var out *os.File
		if flagRecordOutput != "" {
			safe, err := validateOutputPath(flagRecordOutput)
			if err != nil {
				exitErr("record: --output", err)
			}
			f, err := os.OpenFile(safe, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
			if err != nil {
				exitErr("record: open output", err)
			}
			defer f.Close()
			out = f
		} else {
			out = os.Stdout
		}

		b, page := openPage()
		defer b.Close()

		applyStealthIfNeeded(page)

		rec := artifact.NewRecorder(page, out, artifact.RecorderOpts{
			IncludeScrolls: flagRecordIncludeScrolls,
			IncludeHovers:  flagRecordIncludeHovers,
			StopOnNavigate: flagRecordStopOnNavigate,
		})

		if err := rec.Start(); err != nil {
			exitErr("record: start", err)
		}

		if url != "" {
			if _, err := engine.Navigate(page, url, "load"); err != nil {
				fmt.Fprintf(os.Stderr, "[record] navigate: %v\n", err)
			}
		}

		fmt.Fprintf(os.Stderr, "[record] recording %s — Ctrl-C to stop\n", url)

		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
		defer signal.Stop(sig)

		select {
		case <-sig:
			fmt.Fprintln(os.Stderr, "[record] interrupted")
		case <-rec.Done():
			fmt.Fprintln(os.Stderr, "[record] stopped (browser closed)")
		}

		rec.Stop()

		if flagRecordOutput != "" {
			fmt.Fprintf(os.Stderr, "[record] wrote ops to %s\n", flagRecordOutput)
		}
	},
}

func init() {
	recordCmd.Flags().StringVar(&flagRecordOutput, "output", "", "Write JSONL ops to this file (default: stdout)")
	recordCmd.Flags().BoolVar(&flagRecordIncludeScrolls, "include-scrolls", false, "Emit scroll_by ops (noisy by default)")
	recordCmd.Flags().BoolVar(&flagRecordIncludeHovers, "include-hovers", false, "Emit hover ops (noisy by default)")
	recordCmd.Flags().BoolVar(&flagRecordStopOnNavigate, "stop-on-navigate", false, "Stop recording after the first navigation")
	rootCmd.AddCommand(recordCmd)
}
