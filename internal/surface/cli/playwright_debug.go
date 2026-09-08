package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

func unsupportedDebugCommand(name string, args []string) {
	type debugResult struct {
		Supported   bool     `json:"supported"`
		Command     string   `json:"command"`
		Args        []string `json:"args,omitempty"`
		Reason      string   `json:"reason"`
		Alternative string   `json:"alternative"`
	}
	result := debugResult{
		Supported:   false,
		Command:     name,
		Args:        args,
		Reason:      "Playwright test debugging commands require a paused Playwright test debug session; ghostchrome currently controls browsers through CDP/Rod only",
		Alternative: "Use `playwright-cli` for --debug=cli test control, or expose a CDP endpoint and use `ghostchrome attach --cdp=...` for page inspection.",
	}
	output(result, fmt.Sprintf("%s unsupported: %s", name, result.Reason))
	os.Exit(2)
}

var resumeCmd = &cobra.Command{
	Use:   "resume",
	Short: "Resume a paused Playwright test (unsupported in ghostchrome)",
	Long: `Playwright CLI's resume command continues a paused Playwright test
debug session. ghostchrome does not speak the Playwright test debugging
protocol, so this command is registered as an explicit compatibility boundary.`,
	Args: cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		unsupportedDebugCommand("resume", args)
	},
}

var stepOverCmd = &cobra.Command{
	Use:   "step-over",
	Short: "Step over the next Playwright test action (unsupported in ghostchrome)",
	Long: `Playwright CLI's step-over command advances a paused Playwright test
debug session by one action. ghostchrome does not speak the Playwright test
debugging protocol, so this command is registered as an explicit compatibility
boundary.`,
	Args: cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		unsupportedDebugCommand("step-over", args)
	},
}

var pauseAtCmd = &cobra.Command{
	Use:   "pause-at <file:line>",
	Short: "Set a Playwright test breakpoint (unsupported in ghostchrome)",
	Long: `Playwright CLI's pause-at command sets a breakpoint in a paused or
debuggable Playwright test session. ghostchrome does not speak the Playwright
test debugging protocol, so this command is registered as an explicit
compatibility boundary.`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		unsupportedDebugCommand("pause-at", args)
	},
}

func init() {
	rootCmd.AddCommand(resumeCmd, stepOverCmd, pauseAtCmd)
	commandGroups["resume"] = "observe"
	commandGroups["step-over"] = "observe"
	commandGroups["pause-at"] = "observe"
}
