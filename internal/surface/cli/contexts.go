package cli

import (
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/dev-toolings/ghostchrome/engine"
	"github.com/go-rod/rod"
	"github.com/spf13/cobra"
)

var contextsCmd = &cobra.Command{
	Use:   "contexts",
	Short: "Manage named isolated BrowserContexts (incognito sessions)",
}

var contextsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List known contexts with their CDP ID and liveness status",
	Run: func(cmd *cobra.Command, args []string) {
		rb, cleanup := rodBrowserForContexts()
		if cleanup != nil {
			defer cleanup()
		}

		entries, err := engine.ListContexts(rb)
		if err != nil {
			exitErr("list contexts", err)
		}

		sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })

		if flagFormat == "json" {
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			_ = enc.Encode(entries)
			return
		}

		if len(entries) == 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "no contexts in registry")
			return
		}
		fmt.Fprintf(cmd.OutOrStdout(), "%-20s  %-36s  %s\n", "NAME", "CONTEXT ID", "ALIVE")
		for _, e := range entries {
			alive := e.AliveStr()
			fmt.Fprintf(cmd.OutOrStdout(), "%-20s  %-36s  %s\n", e.Name, e.ID, alive)
		}
	},
}

var contextsCloseCmd = &cobra.Command{
	Use:   "close <name>",
	Short: "Dispose named context in Chrome and remove from registry",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		name := args[0]

		rb, cleanup := rodBrowserForContexts()
		if cleanup != nil {
			defer cleanup()
		}

		if err := engine.CloseContext(rb, name); err != nil {
			exitErr("close context", err)
		}
		if flagFormat == "json" {
			fmt.Fprintf(cmd.OutOrStdout(), `{"closed":%q}`+"\n", name)
		} else {
			fmt.Fprintf(cmd.OutOrStdout(), "context %q closed\n", name)
		}
	},
}

// rodBrowserForContexts returns a *rod.Browser so that liveness can be
// checked via CDP. When --connect is empty it tries auto-discovery on the
// default debug port (same as --connect=auto). Returns nil when no Chrome is
// reachable — in that case liveness is reported as unknown ("?").
func rodBrowserForContexts() (*rod.Browser, func()) {
	connectURL := flagConnect
	if connectURL == "" || connectURL == "auto" {
		ws, err := engine.DiscoverCDP(nil, 800*time.Millisecond)
		if err != nil {
			if connectURL == "auto" {
				fmt.Printf("warning: connect=auto failed (%v); reporting liveness as unknown\n", err)
			}
			return nil, nil
		}
		connectURL = ws
	}
	b, err := engine.NewBrowser(connectURL, flagHeadless, flagTimeout)
	if err != nil {
		fmt.Printf("warning: browser connect failed (%v); reporting liveness as unknown\n", err)
		return nil, nil
	}
	return b.RodBrowser(), func() { b.Close() }
}

func init() {
	contextsCmd.AddCommand(contextsListCmd)
	contextsCmd.AddCommand(contextsCloseCmd)
	rootCmd.AddCommand(contextsCmd)
	commandGroups["contexts"] = "session"
}
