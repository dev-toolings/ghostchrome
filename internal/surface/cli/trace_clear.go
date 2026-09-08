package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var flagTraceClearFile string

var traceClearCmd = &cobra.Command{
	Use:   "trace-clear",
	Short: "Truncate a MCP session trace file to zero bytes",
	Long: `Truncate the given JSONL trace file to zero bytes. Use between unrelated
sessions so the file does not grow unbounded.

The file is preserved (not deleted) so that MCP server processes appending
to it don't need to reopen it. If the file does not exist, a no-op with
exit 0.

Examples:
  ghostchrome trace-clear --file /tmp/session.jsonl`,
	Run: func(cmd *cobra.Command, args []string) {
		if flagTraceClearFile == "" {
			exitErr("trace-clear", fmt.Errorf("--file is required"))
		}
		if err := os.Truncate(flagTraceClearFile, 0); err != nil && !os.IsNotExist(err) {
			exitErr("trace-clear", err)
		}
		type clearResult struct {
			Path    string `json:"path"`
			Cleared bool   `json:"cleared"`
		}
		res := clearResult{Path: flagTraceClearFile, Cleared: true}
		text := "trace cleared: " + flagTraceClearFile
		output(res, text)
	},
}

func init() {
	traceClearCmd.Flags().StringVar(&flagTraceClearFile, "file", "", "Path to the JSONL trace file to truncate (required)")
	rootCmd.AddCommand(traceClearCmd)
}
