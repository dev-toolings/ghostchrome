package cli

import (
	"fmt"
	"strings"

	"github.com/dev-toolings/ghostchrome/internal/setup"
	"github.com/spf13/cobra"
)

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Diagnose ghostchrome setup (Chrome, profiles, extensions, connectivity)",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		checks := setup.Diagnose()

		var sb strings.Builder
		sb.WriteString("[doctor] ghostchrome diagnostics\n")
		allOK := true
		for _, c := range checks {
			icon := "✓"
			switch c.Status {
			case "warn":
				icon = "⚠"
				allOK = false
			case "fail":
				icon = "✗"
				allOK = false
			case "info":
				icon = "ℹ"
			}
			line := fmt.Sprintf("  %s %-15s %s", icon, c.Name, c.Detail)
			sb.WriteString(line + "\n")
		}
		if allOK {
			sb.WriteString("\n  All checks passed.")
		}
		output(checks, strings.TrimRight(sb.String(), "\n"))
	},
}

func init() {
	rootCmd.AddCommand(doctorCmd)
}
