package cli

import (
	"fmt"
	"strings"

	"github.com/dev-toolings/ghostchrome/internal/core/engine"
	"github.com/spf13/cobra"
)

var initScriptCmd = &cobra.Command{
	Use:     "init-script",
	Aliases: []string{"initscript"},
	Short:   "Manage user init scripts (JS executed on every page load)",
}

var initScriptAddCmd = &cobra.Command{
	Use:   "add <path-to-script.js>",
	Short: "Install an init script",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		name, err := engine.AddInitScript(args[0])
		if err != nil {
			exitErr("init-script add", err)
		}
		type addResult struct {
			Action string `json:"action"`
			Name   string `json:"name"`
		}
		output(&addResult{Action: "added", Name: name},
			fmt.Sprintf("[init-script] added %s", name))
	},
}

var initScriptRemoveCmd = &cobra.Command{
	Use:   "remove <name>",
	Short: "Remove an init script by name",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		if err := engine.RemoveInitScript(args[0]); err != nil {
			exitErr("init-script remove", err)
		}
		type removeResult struct {
			Action string `json:"action"`
			Name   string `json:"name"`
		}
		output(&removeResult{Action: "removed", Name: args[0]},
			fmt.Sprintf("[init-script] removed %s", args[0]))
	},
}

var initScriptListCmd = &cobra.Command{
	Use:   "list",
	Short: "List installed init scripts",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		scripts, err := engine.ListInitScripts()
		if err != nil {
			exitErr("init-script list", err)
		}
		type listResult struct {
			Scripts []string `json:"scripts"`
			Count   int      `json:"count"`
		}
		var text string
		if len(scripts) == 0 {
			text = "[init-script] no scripts installed"
		} else {
			text = fmt.Sprintf("[init-script] %d installed:\n  %s", len(scripts), strings.Join(scripts, "\n  "))
		}
		output(&listResult{Scripts: scripts, Count: len(scripts)}, text)
	},
}

func init() {
	initScriptCmd.AddCommand(initScriptAddCmd, initScriptRemoveCmd, initScriptListCmd)
	rootCmd.AddCommand(initScriptCmd)
}
