package cli

import (
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
)

var flagRunCodeFilename string

var runCodeCmd = &cobra.Command{
	Use:   "run-code <code>",
	Short: "Run arbitrary Playwright code (unsupported in ghostchrome)",
	Long: `Playwright CLI's run-code executes arbitrary scripts with full Playwright
API access. ghostchrome is CDP/Rod-native and does not embed a Playwright
runtime, so this command is exposed as an explicit compatibility boundary.
Use eval for JavaScript executed in the page context.`,
	Args: cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		code, err := readRunCode(args, flagRunCodeFilename)
		if err != nil {
			exitErr("run-code", err)
		}
		type runCodeResult struct {
			Supported   bool   `json:"supported"`
			Reason      string `json:"reason"`
			CodeBytes   int    `json:"code_bytes"`
			Alternative string `json:"alternative"`
		}
		result := runCodeResult{
			Supported:   false,
			Reason:      "ghostchrome is CDP/Rod-native and does not embed the Playwright runtime required for page/context API scripts",
			CodeBytes:   len(code),
			Alternative: "Use `ghostchrome eval` for page-context JavaScript, or run Playwright scripts with playwright-cli/node.",
		}
		output(result, result.Reason)
		os.Exit(2)
	},
}

func readRunCode(args []string, filename string) (string, error) {
	if filename != "" {
		if len(args) > 0 {
			return "", fmt.Errorf("provide code either as an argument or --filename, not both")
		}
		var data []byte
		var err error
		if filename == "-" {
			data, err = io.ReadAll(os.Stdin)
		} else {
			data, err = os.ReadFile(filename)
		}
		if err != nil {
			return "", err
		}
		return string(data), nil
	}
	if len(args) == 0 {
		return "", fmt.Errorf("code required (or use --filename)")
	}
	return args[0], nil
}

func init() {
	runCodeCmd.Flags().StringVar(&flagRunCodeFilename, "filename", "", "Read Playwright code from file ('-' for stdin)")
	rootCmd.AddCommand(runCodeCmd)
	commandGroups["run-code"] = "observe"
}
