package cli

import (
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/dev-toolings/ghostchrome/internal/core/engine"
	"github.com/spf13/cobra"
)

var (
	flagInterceptBlock       string
	flagInterceptFulfill     string
	flagInterceptBody        string
	flagInterceptStatus      int
	flagInterceptContentType string
	flagInterceptRules       string
)

var interceptCmd = &cobra.Command{
	Use:   "intercept",
	Short: "Block or fulfill network requests matching URL glob patterns",
	Long: `Install a Fetch-domain interceptor on the connected browser. The command
blocks until it receives SIGINT / SIGTERM, so run it in a dedicated terminal
(or wire it into "batch" for in-flow interception).

Patterns use glob syntax (same as Chrome DevTools): "*" matches any sequence,
"?" matches a single char. Pass a comma-separated list to --block.

Block examples:
  ghostchrome intercept --block "*.png,*.jpg,*analytics*" --connect ws://...
  ghostchrome intercept --block "https://ads.example.com/*" --connect ws://...

Fulfill examples (mock an API):
  ghostchrome intercept --fulfill "*/api/users" --body '@mock.json' --status 200
  ghostchrome intercept --fulfill "https://x/config" --body '{"flag":true}'

The --body flag accepts an inline string or "@path" to read from a file.`,
	Run: func(cmd *cobra.Command, args []string) {
		if flagInterceptBlock == "" && flagInterceptFulfill == "" && flagInterceptRules == "" {
			exitErr("intercept", fmt.Errorf("need --block, --fulfill, or --rules"))
		}

		b, _ := openPage()
		defer b.Close()

		spec := engine.InterceptSpec{
			BlockPatterns: engine.ParseBlockList(flagInterceptBlock),
		}
		if flagInterceptFulfill != "" {
			body, err := engine.LoadFulfillBody(flagInterceptBody)
			if err != nil {
				exitErr("intercept", err)
			}
			spec.FulfillPattern = flagInterceptFulfill
			spec.FulfillBody = body
			spec.FulfillStatus = flagInterceptStatus
			spec.FulfillContentType = flagInterceptContentType
		}
		if flagInterceptRules != "" {
			baseDir := filepath.Dir(flagInterceptRules)
			rules, err := engine.LoadRules(flagInterceptRules, baseDir)
			if err != nil {
				exitErr("intercept", err)
			}
			spec.Rules = rules
		}

		session, err := engine.StartIntercept(b.RodBrowser(), spec)
		if err != nil {
			exitErr("intercept", err)
		}

		fmt.Fprintf(os.Stderr, "[intercept] running (block=%d, fulfill=%q, rules=%d). Ctrl-C to stop.\n",
			len(spec.BlockPatterns), spec.FulfillPattern, len(spec.Rules))

		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
		<-sig

		if err := session.Stop(); err != nil {
			fmt.Fprintf(os.Stderr, "[intercept] stop: %v\n", err)
		}

		blocked, fulfilled, passed := session.Stats().Snapshot()
		type interceptResult struct {
			Action    string `json:"action"`
			Blocked   int    `json:"blocked"`
			Fulfilled int    `json:"fulfilled"`
			Passed    int    `json:"passed"`
		}
		text := fmt.Sprintf("[intercept] stopped — blocked=%d fulfilled=%d passed=%d", blocked, fulfilled, passed)
		output(&interceptResult{Action: "intercept", Blocked: blocked, Fulfilled: fulfilled, Passed: passed}, text)
	},
}

func init() {
	interceptCmd.Flags().StringVar(&flagInterceptBlock, "block", "", "Comma-separated URL glob patterns to block")
	interceptCmd.Flags().StringVar(&flagInterceptFulfill, "fulfill", "", "URL glob pattern to fulfill with --body")
	interceptCmd.Flags().StringVar(&flagInterceptBody, "body", "", "Response body (inline string or @path)")
	interceptCmd.Flags().IntVar(&flagInterceptStatus, "status", 200, "HTTP status for --fulfill responses")
	interceptCmd.Flags().StringVar(&flagInterceptContentType, "content-type", "", "Content-Type for --fulfill (default auto-detected)")
	interceptCmd.Flags().StringVar(&flagInterceptRules, "rules", "", "Path to JSON rules file for multi-pattern fulfillment")
	rootCmd.AddCommand(interceptCmd)
}
