package cmd

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/dev-toolings/ghostchrome/internal/core/dashboard"
	"github.com/spf13/cobra"
)

var flagDashboardPort int
var flagDashboardAnnotate bool

var dashboardCmd = &cobra.Command{
	Use:     "dashboard [url]",
	Aliases: []string{"show"},
	Short:   "Open a live browser viewport dashboard",
	Long: `Start a local web dashboard that streams the browser viewport
via WebSocket screencast. Navigate to the dashboard URL in any browser
to see real-time page activity.

When --annotate is set, draw a rectangle in the dashboard and add a note. The
command result includes an annotations artifact JSON path. Each annotation has
its screenshot filename, screenshot-pixel region, note, and timestamp.

Examples:
  ghostchrome dashboard https://example.com --port 8080
  ghostchrome show --annotate --connect auto --port 3000`,
	Args: cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		b, page := openPage()
		defer b.Close()

		if len(args) > 0 {
			navigateIfRequested(page, args[0], "load")
		}

		dash, addr, err := dashboard.StartWithOptions(page, flagDashboardPort, dashboard.Options{Annotate: flagDashboardAnnotate})
		if err != nil {
			exitErr("dashboard", err)
		}
		defer dash.Stop()

		fmt.Fprintf(os.Stderr, "[dashboard] live at %s\n", addr)

		type dashResult struct {
			URL                 string `json:"url"`
			AnnotationsArtifact string `json:"annotations_artifact,omitempty"`
		}
		artifact := ""
		if flagDashboardAnnotate {
			artifact = dash.AnnotationArtifactPath()
		}
		output(&dashResult{URL: addr, AnnotationsArtifact: artifact}, fmt.Sprintf("[dashboard] %s", addr))

		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
		<-sig
		fmt.Fprintln(os.Stderr, "\n[dashboard] shutting down")
	},
}

func init() {
	dashboardCmd.Flags().IntVar(&flagDashboardPort, "port", 0, "Dashboard HTTP port (0 = random)")
	dashboardCmd.Flags().BoolVar(&flagDashboardAnnotate, "annotate", false, "Enable rectangle-and-note annotations and write a JSON artifact")
	rootCmd.AddCommand(dashboardCmd)
}
