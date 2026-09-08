package cli

import (
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/dev-toolings/ghostchrome/internal/core/engine"
	"github.com/spf13/cobra"
)

func humanBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}

var profilesCmd = &cobra.Command{
	Use:   "profiles",
	Short: "Manage persistent Chrome profiles (~/.ghostchrome/profiles)",
	Long: `Persistent profiles store cookies/cache so logins and sessions survive.
They accumulate disk over time — list them and remove the ones you no longer
need. Tearing a session down with --purge also removes its profile.`,
}

var profilesListCmd = &cobra.Command{
	Use:   "list",
	Short: "List profiles with their on-disk size",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		profiles, err := engine.ListProfiles()
		if err != nil {
			exitErr("list profiles", err)
		}
		sort.Slice(profiles, func(i, j int) bool { return profiles[i].Bytes > profiles[j].Bytes })

		if flagFormat == "json" {
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			_ = enc.Encode(profiles)
			return
		}
		if len(profiles) == 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "no profiles")
			return
		}
		var total int64
		fmt.Fprintf(cmd.OutOrStdout(), "%-24s  %s\n", "NAME", "SIZE")
		for _, p := range profiles {
			total += p.Bytes
			fmt.Fprintf(cmd.OutOrStdout(), "%-24s  %s\n", p.Name, humanBytes(p.Bytes))
		}
		fmt.Fprintf(cmd.OutOrStdout(), "%-24s  %s\n", "(total)", humanBytes(total))
	},
}

var profilesRmCmd = &cobra.Command{
	Use:   "rm <name> [name...]",
	Short: "Remove one or more profiles and reclaim their disk",
	Args:  cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		removed := 0
		for _, name := range args {
			if err := engine.RemoveProfile(name); err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "skip %q: %v\n", name, err)
				continue
			}
			removed++
			if flagFormat != "json" {
				fmt.Fprintf(cmd.OutOrStdout(), "removed profile %q\n", name)
			}
		}
		if flagFormat == "json" {
			fmt.Fprintf(cmd.OutOrStdout(), `{"removed":%d}`+"\n", removed)
		}
	},
}

var (
	flagGCOlderThan time.Duration
	flagGCYes       bool
)

var profilesGCCmd = &cobra.Command{
	Use:   "gc",
	Short: "Reclaim orphan profiles idle beyond --older-than (dry-run unless --yes)",
	Long: `Garbage-collect stale Chrome profiles that accumulate disk over time.

A profile is a candidate only when ALL hold:
  - it is not the implicit "default" daemon profile;
  - it is not backing a currently-live session;
  - no file in it has changed within --older-than (it is genuinely idle).

By default this only PRINTS the candidates (dry run). Review the list, then
re-run with --yes to delete. Persistent login profiles (LinkedIn, Google, ...)
that you still use appear only once they have been idle past the window — check
the dry run before confirming so none is reclaimed by surprise.`,
	Args: cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		candidates, err := engine.GCProfiles(flagGCOlderThan, !flagGCYes)
		if err != nil {
			exitErr("gc profiles", err)
		}
		sort.Slice(candidates, func(i, j int) bool { return candidates[i].Bytes > candidates[j].Bytes })

		var freed int64
		for _, c := range candidates {
			freed += c.Bytes
		}

		if flagFormat == "json" {
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			_ = enc.Encode(map[string]any{
				"dry_run":    !flagGCYes,
				"candidates": candidates,
				"freed":      freed,
			})
			return
		}

		if len(candidates) == 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "no idle orphan profiles to reclaim")
			return
		}

		fmt.Fprintf(cmd.OutOrStdout(), "%-24s  %10s  %s\n", "NAME", "SIZE", "IDLE SINCE")
		for _, c := range candidates {
			fmt.Fprintf(cmd.OutOrStdout(), "%-24s  %10s  %s\n", c.Name, humanBytes(c.Bytes), c.Modified.Format("2006-01-02"))
		}
		if flagGCYes {
			fmt.Fprintf(cmd.OutOrStdout(), "removed %d profile(s), reclaimed %s\n", len(candidates), humanBytes(freed))
		} else {
			fmt.Fprintf(cmd.OutOrStdout(), "(dry run) %d profile(s), %s reclaimable — re-run with --yes to delete\n", len(candidates), humanBytes(freed))
		}
	},
}

func init() {
	profilesCmd.AddCommand(profilesListCmd)
	profilesCmd.AddCommand(profilesRmCmd)
	profilesGCCmd.Flags().DurationVar(&flagGCOlderThan, "older-than", 168*time.Hour, "only reclaim profiles idle at least this long (e.g. 24h, 168h)")
	profilesGCCmd.Flags().BoolVar(&flagGCYes, "yes", false, "actually delete (default is a dry run)")
	profilesCmd.AddCommand(profilesGCCmd)
	rootCmd.AddCommand(profilesCmd)
	commandGroups["profiles"] = "session"
}
