package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/dev-toolings/ghostchrome/internal/setup"
	"github.com/spf13/cobra"
)

var flagInstallSkills bool
var (
	flagInstallBrowserOnlyShell bool
	flagInstallBrowserNoShell   bool
	flagInstallBrowserWithDeps  bool
	flagInstallBrowserDryRun    bool
	flagInstallBrowserList      bool
	flagInstallBrowserForce     bool
)

var installCompatCmd = &cobra.Command{
	Use:   "install",
	Short: "Set up ghostchrome (skills, verify, print config)",
	Long: `Install sets up everything ghostchrome needs to run:

  1. Install the bundled Claude Code agent skill (~/.claude/skills/ghostchrome)
  2. Print the configuration summary

This is idempotent — safe to re-run. It is the counterpart of 'ghostchrome uninstall'.

Examples:
  ghostchrome install            # full setup
  ghostchrome install --skills   # skills only (Playwright CLI compatible)`,
	Args: cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		out := cmd.OutOrStdout()

		paths, err := setup.InstallEmbeddedSkills()
		if err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "warning: skill install failed: %v\n", err)
		}
		for _, p := range paths {
			fmt.Fprintf(out, "skill installed → %s\n", p)
		}

		if flagInstallSkills {
			return
		}

		exe, _ := os.Executable()
		fmt.Fprintf(out, "binary         → %s\n", exe)

		home, _ := os.UserHomeDir()
		fmt.Fprintf(out, "data           → %s\n", filepath.Join(home, ".ghostchrome"))
		fmt.Fprintf(out, "daemon         → enabled by default (set GHOSTCHROME_NO_DAEMON=1 to disable)\n")
		fmt.Fprintln(out, "\nghostchrome is ready. Try: ghostchrome preview https://example.com")
	},
}

var installBrowserCompatCmd = &cobra.Command{
	Use:   "install-browser [name]",
	Short: "Install a browser build for Playwright (unsupported in ghostchrome)",
	Args:  cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		target := "default browsers"
		if len(args) > 0 {
			target = args[0]
		}
		if flagInstallBrowserList {
			output(map[string]any{
				"supported": false,
				"command":   "install-browser",
				"action":    "list",
				"dry_run":   true,
				"reason":    "list mode requires Playwright's browser installation metadata",
			}, "list unsupported: ghostchrome does not manage Playwright browser binaries")
			os.Exit(2)
		}
		unsupportedPlaywrightCommand("install-browser", args, fmt.Sprintf("ghostchrome does not install Playwright browser binaries (requested %q)", target), "Use `bunx -y @playwright/cli install` (or `npm/yarn install` in your Playwright project) for browser runtimes.")
	},
}

func init() {
	installCompatCmd.Flags().BoolVar(&flagInstallSkills, "skills", false, "Install only the bundled agent skill (Playwright CLI compatible)")
	installBrowserCompatCmd.Flags().BoolVar(&flagInstallBrowserOnlyShell, "only-shell", false, "Compatibility passthrough flag (unsupported; kept for API parity)")
	installBrowserCompatCmd.Flags().BoolVar(&flagInstallBrowserNoShell, "no-shell", false, "Compatibility passthrough flag (unsupported; kept for API parity)")
	installBrowserCompatCmd.Flags().BoolVar(&flagInstallBrowserWithDeps, "with-deps", false, "Compatibility passthrough flag (unsupported; kept for API parity)")
	installBrowserCompatCmd.Flags().BoolVar(&flagInstallBrowserDryRun, "dry-run", false, "Compatibility passthrough flag (unsupported; kept for API parity)")
	installBrowserCompatCmd.Flags().BoolVar(&flagInstallBrowserList, "list", false, "Compatibility passthrough flag (unsupported; kept for API parity)")
	installBrowserCompatCmd.Flags().BoolVar(&flagInstallBrowserForce, "force", false, "Compatibility passthrough flag (unsupported; kept for API parity)")
	rootCmd.AddCommand(installCompatCmd, installBrowserCompatCmd)
	commandGroups["install"] = "util"
	commandGroups["install-browser"] = "util"
}
