package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/dev-toolings/ghostchrome/internal/core/engine"
	"github.com/spf13/cobra"
)

var (
	flagCaptureURLMatch  string
	flagCaptureMimeMatch string
	flagCaptureMax       int
	flagCaptureBodies    bool
	flagCaptureOutput    string
	flagCaptureNDJSON    string
	flagCaptureWait      int
	flagCaptureScroll    int
	flagCaptureScrollMs  int
)

var captureCmd = &cobra.Command{
	Use:   "capture [url]",
	Short: "Passively record network requests (DevTools Network tab → JSON)",
	Long: `Capture enables the Chrome Network domain and records every request made
by the page. Matching entries are written as a JSON array (--output) and/or
streamed as NDJSON (--ndjson). No requests are modified, blocked, or replayed.

Stops automatically once --max matching entries have been collected, when
--wait seconds elapse, or on SIGINT.

Examples:
  ghostchrome capture https://www.tiktok.com/@tiktok \
    --url-match '/api/' --bodies --max 10 --output out.json

  ghostchrome capture https://site.com \
    --url-match 'graphql' --mime-match 'json' --max 20 --ndjson trace.ndjson`,
	Args: cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		b, page := openPage()
		defer b.Close()

		applyStealthIfNeeded(page)

		spec := engine.CaptureSpec{
			URLMatch:    flagCaptureURLMatch,
			MimeMatch:   flagCaptureMimeMatch,
			Max:         flagCaptureMax,
			IncludeBody: flagCaptureBodies,
			OutputPath:  flagCaptureNDJSON,
		}
		session, err := engine.StartCapture(page, spec)
		if err != nil {
			exitErr("capture", err)
		}

		if len(args) == 1 {
			if _, err := engine.Navigate(page, args[0], "load"); err != nil {
				fmt.Fprintf(os.Stderr, "navigate: %v\n", err)
			}
		}

		if flagCaptureScroll > 0 {
			go func() {
				defer func() {
					if r := recover(); r != nil {
						fmt.Fprintf(os.Stderr, "[capture] scroll error: %v\n", r)
					}
				}()
				for i := 0; i < flagCaptureScroll; i++ {
					time.Sleep(time.Duration(flagCaptureScrollMs) * time.Millisecond)
					_, _ = page.Eval(`() => window.scrollBy(0, window.innerHeight * 2)`)
				}
			}()
		}

		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
		defer signal.Stop(sig)

		var timeoutCh <-chan time.Time
		if flagCaptureWait > 0 {
			timeoutCh = time.After(time.Duration(flagCaptureWait) * time.Second)
		}

		fmt.Fprintf(os.Stderr, "[capture] running (url=%q mime=%q max=%d bodies=%v). Ctrl-C to stop.\n",
			spec.URLMatch, spec.MimeMatch, spec.Max, spec.IncludeBody)

		select {
		case <-session.ReachedMax():
			fmt.Fprintf(os.Stderr, "[capture] reached max=%d\n", spec.Max)
		case <-sig:
			fmt.Fprintln(os.Stderr, "[capture] interrupted")
		case <-timeoutCh:
			fmt.Fprintf(os.Stderr, "[capture] wait timeout (%ds)\n", flagCaptureWait)
		}

		entries, writeErr := session.Stop()
		if writeErr != nil {
			exitErr("ndjson write", writeErr)
		}

		if flagCaptureOutput != "" {
			data, err := json.MarshalIndent(entries, "", "  ")
			if err != nil {
				exitErr("marshal", err)
			}
			if err := os.WriteFile(flagCaptureOutput, data, 0o600); err != nil {
				exitErr("write output", err)
			}
			fmt.Fprintf(os.Stderr, "[capture] wrote %d entries to %s\n", len(entries), flagCaptureOutput)
		}

		type summary struct {
			Action string `json:"action"`
			Count  int    `json:"count"`
			Output string `json:"output,omitempty"`
			NDJSON string `json:"ndjson,omitempty"`
		}
		text := fmt.Sprintf("[capture] stopped — %d entries", len(entries))
		output(&summary{Action: "capture", Count: len(entries), Output: flagCaptureOutput, NDJSON: flagCaptureNDJSON}, text)
	},
}

func init() {
	captureCmd.Flags().StringVar(&flagCaptureURLMatch, "url-match", "", "Regex applied to request URL")
	captureCmd.Flags().StringVar(&flagCaptureMimeMatch, "mime-match", "", "Regex applied to response MIME type")
	captureCmd.Flags().IntVar(&flagCaptureMax, "max", 0, "Stop after N matching entries (0 = unlimited)")
	captureCmd.Flags().BoolVar(&flagCaptureBodies, "bodies", false, "Fetch response bodies via Network.getResponseBody")
	captureCmd.Flags().StringVar(&flagCaptureOutput, "output", "", "Write final JSON array to this path")
	captureCmd.Flags().StringVar(&flagCaptureNDJSON, "ndjson", "", "Stream each entry as NDJSON to this path")
	captureCmd.Flags().IntVar(&flagCaptureWait, "wait", 0, "Max seconds to wait (0 = until max/SIGINT)")
	captureCmd.Flags().IntVar(&flagCaptureScroll, "scroll", 0, "Auto-scroll N times to trigger lazy-loaded API calls")
	captureCmd.Flags().IntVar(&flagCaptureScrollMs, "scroll-ms", 1500, "Delay between scrolls (ms)")
	rootCmd.AddCommand(captureCmd)
}
