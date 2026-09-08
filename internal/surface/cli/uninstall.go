package cli

import (
	"fmt"
	"os"

	"github.com/dev-toolings/ghostchrome/engine"
	"github.com/dev-toolings/ghostchrome/internal/setup"
	"github.com/spf13/cobra"
)

var (
	flagUninstallYes   bool
	flagUninstallPurge bool
)

var uninstallCmd = &cobra.Command{
	Use:   "uninstall",
	Short: "Remove the ghostchrome binary (and optionally its data)",
	Long: `Stop all sessions, then remove the ghostchrome binary. With --purge,
also delete the data directories (profiles, sessions, contexts, cache).

Use --yes to skip the confirmation prompt (dry-run plan is printed otherwise).

Examples:
  ghostchrome uninstall                # show what would be removed
  ghostchrome uninstall --yes          # remove the binary only
  ghostchrome uninstall --purge --yes  # remove the binary AND all data`,
	Args: cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		out := cmd.OutOrStdout()

		exe, err := os.Executable()
		if err != nil {
			exitErr("uninstall", fmt.Errorf("locate binary: %w", err))
		}
		dataDirs := engine.GhostchromeDataDirs()

		skillDirs := setup.InstalledSkillDirs()

		// Plan
		fmt.Fprintln(out, "uninstall plan:")
		fmt.Fprintf(out, "  - remove binary: %s\n", exe)
		for _, s := range skillDirs {
			fmt.Fprintf(out, "  - remove skill:  %s\n", s)
		}
		if flagUninstallPurge {
			for _, d := range dataDirs {
				fmt.Fprintf(out, "  - remove data:   %s\n", d)
			}
			if len(dataDirs) == 0 {
				fmt.Fprintln(out, "  - (no data directories present)")
			}
		} else {
			fmt.Fprintln(out, "  - keeping data (pass --purge to remove profiles/sessions/cache)")
		}

		if !flagUninstallYes {
			fmt.Fprintln(out, "\nDry run. Re-run with --yes to proceed.")
			return
		}

		// Stop any running sessions so we don't orphan browsers. Surface a
		// failure loudly — silently proceeding could leave detached serves.
		if n, err := engine.KillAllSessions(); err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(),
				"warning: could not stop all sessions: %v\n"+
					"  some 'ghostchrome serve' processes may still be running — check: pgrep -af 'ghostchrome serve'\n", err)
		} else if n > 0 {
			fmt.Fprintf(out, "stopped %d session(s)\n", n)
		}

		// Remove the bundled skill (installed alongside the binary, so it goes
		// with it regardless of --purge).
		if n := setup.RemoveInstalledSkills(); n > 0 {
			fmt.Fprintf(out, "removed %d bundled skill(s)\n", n)
		}

		if flagUninstallPurge {
			for _, d := range dataDirs {
				if err := os.RemoveAll(d); err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "warning: could not remove %s: %v\n", d, err)
				} else {
					fmt.Fprintf(out, "removed %s\n", d)
				}
			}
		}

		// Remove the binary last (a running process can unlink its own file on
		// Unix; the inode lives until exit). If it's not writable (e.g. under
		// /usr/local/bin), tell the user to remove it manually.
		if err := os.Remove(exe); err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "could not remove %s: %v\nremove it manually (e.g. sudo rm %s)\n", exe, err, exe)
			os.Exit(1)
		}
		fmt.Fprintf(out, "removed %s\nghostchrome uninstalled.\n", exe)
	},
}

func init() {
	uninstallCmd.Flags().BoolVar(&flagUninstallYes, "yes", false, "Actually perform the uninstall (otherwise dry-run)")
	uninstallCmd.Flags().BoolVar(&flagUninstallPurge, "purge", false, "Also remove data directories (profiles, sessions, cache)")
	rootCmd.AddCommand(uninstallCmd)
	commandGroups["uninstall"] = "util"
}
