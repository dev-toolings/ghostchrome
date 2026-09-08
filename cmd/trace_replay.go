package cmd

import (
	"fmt"

	"github.com/dev-toolings/ghostchrome/internal/core/artifact"
	"github.com/spf13/cobra"
)

var (
	flagTraceReplayFile  string
	flagTraceReplayLimit int
)

var traceReplayCmd = &cobra.Command{
	Use:   "trace-replay",
	Short: "Read a MCP session trace and print a chronological digest",
	Long: `Read a JSONL trace file produced by the MCP server (GHOSTCHROME_MCP_TRACE)
and print the last N entries as JSON.

Examples:
  ghostchrome trace-replay --file /tmp/session.jsonl
  ghostchrome trace-replay --file /tmp/session.jsonl --limit 20 --format json`,
	Run: func(cmd *cobra.Command, args []string) {
		if flagTraceReplayFile == "" {
			exitErr("trace-replay", fmt.Errorf("--file is required"))
		}
		entries, err := artifact.ReadTrace(flagTraceReplayFile, flagTraceReplayLimit)
		if err != nil {
			exitErr("trace-replay", err)
		}
		type replayResult struct {
			File    string                `json:"file"`
			Count   int                   `json:"count"`
			Entries []artifact.TraceEntry `json:"entries"`
		}
		res := replayResult{
			File:    flagTraceReplayFile,
			Count:   len(entries),
			Entries: entries,
		}
		text := fmt.Sprintf("%d entries from %s", len(entries), flagTraceReplayFile)
		output(res, text)
	},
}

func init() {
	traceReplayCmd.Flags().StringVar(&flagTraceReplayFile, "file", "", "Path to the JSONL trace file (required)")
	traceReplayCmd.Flags().IntVar(&flagTraceReplayLimit, "limit", 50, "Max entries to return (newest first); 0 = all")
	rootCmd.AddCommand(traceReplayCmd)
}
