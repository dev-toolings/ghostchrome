package cli

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/dev-toolings/ghostchrome/engine"
	"github.com/spf13/cobra"
)

var sessionsCmd = &cobra.Command{
	Use:   "sessions",
	Short: "Manage named auto-managed Chrome sessions",
	Long: `A session is a persistent Chrome (bound to a disk profile of the same name)
that ghostchrome spawns on first use of -s/--session and reuses across calls.

  ghostchrome -s work goto https://example.com   # spawns + reuses session "work"
  ghostchrome sessions list
  ghostchrome sessions stop work`,
}

var sessionsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List known sessions with port, PID, and liveness",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		entries, err := engine.ListSessions()
		if err != nil {
			exitErr("list sessions", err)
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })

		if flagFormat == "json" {
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			_ = enc.Encode(entries)
			return
		}
		if len(entries) == 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "no sessions in registry")
			return
		}
		fmt.Fprintf(cmd.OutOrStdout(), "%-20s  %-6s  %-8s  %s\n", "NAME", "PORT", "PID", "ALIVE")
		for _, e := range entries {
			fmt.Fprintf(cmd.OutOrStdout(), "%-20s  %-6d  %-8d  %s\n", e.Name, e.Port, e.PID, e.AliveStr())
		}
	},
}

var flagSessionsPurge bool

var sessionsStopCmd = &cobra.Command{
	Use:   "stop <name>",
	Short: "Stop a named session (terminates its Chrome) and remove it",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		name := args[0]
		if err := engine.StopSession(name); err != nil {
			exitErr("stop session", err)
		}
		purged := false
		if flagSessionsPurge {
			if err := engine.RemoveProfile(name); err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "warning: could not remove profile %q: %v\n", name, err)
			} else {
				purged = true
			}
		}
		if flagFormat == "json" {
			fmt.Fprintf(cmd.OutOrStdout(), `{"stopped":%q,"purged":%t}`+"\n", name, purged)
		} else {
			suffix := ""
			if purged {
				suffix = " (profile purged)"
			}
			fmt.Fprintf(cmd.OutOrStdout(), "session %q stopped%s\n", name, suffix)
		}
	},
}

var sessionsPruneCmd = &cobra.Command{
	Use:   "prune",
	Short: "Remove dead sessions (Chrome no longer reachable) from the registry",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		n, err := engine.PruneSessions()
		if err != nil {
			exitErr("prune sessions", err)
		}
		if flagFormat == "json" {
			fmt.Fprintf(cmd.OutOrStdout(), `{"pruned":%d}`+"\n", n)
		} else {
			fmt.Fprintf(cmd.OutOrStdout(), "pruned %d dead session(s)\n", n)
		}
	},
}

var sessionsKillAllCmd = &cobra.Command{
	Use:   "kill-all",
	Short: "Stop every registered session",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		// Capture names before kill-all clears the registry, so --purge can
		// remove their profiles too.
		var names []string
		if flagSessionsPurge {
			if entries, lerr := engine.ListSessions(); lerr == nil {
				for _, e := range entries {
					names = append(names, e.Name)
				}
			}
		}
		n, err := engine.KillAllSessions()
		if err != nil {
			exitErr("kill-all sessions", err)
		}
		purged := 0
		for _, name := range names {
			if rerr := engine.RemoveProfile(name); rerr == nil {
				purged++
			}
		}
		if flagFormat == "json" {
			fmt.Fprintf(cmd.OutOrStdout(), `{"stopped":%d,"purged":%d}`+"\n", n, purged)
		} else {
			msg := fmt.Sprintf("stopped %d session(s)", n)
			if flagSessionsPurge {
				msg += fmt.Sprintf(", purged %d profile(s)", purged)
			}
			fmt.Fprintln(cmd.OutOrStdout(), msg)
		}
	},
}

func init() {
	sessionsStopCmd.Flags().BoolVar(&flagSessionsPurge, "purge", false, "Also delete the session's on-disk profile")
	sessionsKillAllCmd.Flags().BoolVar(&flagSessionsPurge, "purge", false, "Also delete each session's on-disk profile")
	sessionsCmd.AddCommand(sessionsListCmd)
	sessionsCmd.AddCommand(sessionsStopCmd)
	sessionsCmd.AddCommand(sessionsPruneCmd)
	sessionsCmd.AddCommand(sessionsKillAllCmd)
	rootCmd.AddCommand(sessionsCmd)
	commandGroups["sessions"] = "session"
}
