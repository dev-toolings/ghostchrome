package cli

// The skills command tree only renders results; the embedded bundle and the
// on-disk install live in internal/setup.

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/dev-toolings/ghostchrome/internal/setup"
	"github.com/spf13/cobra"
)

var skillsCmd = &cobra.Command{
	Use:   "skills",
	Short: "Install or remove the bundled agent skill (~/.claude/skills)",
	Long: `ghostchrome ships an agent skill that teaches a coding agent (Claude Code)
how to drive it. It is installed globally on 'ghostchrome skills install'
(the install script does this for you) and removed on uninstall.`,
}

var skillsInstallCmd = &cobra.Command{
	Use:   "install",
	Short: "Install the bundled skill globally for Claude Code",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		paths, err := setup.InstallEmbeddedSkills()
		if err != nil {
			exitErr("skills install", err)
		}
		if len(paths) == 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "no bundled skills in this build")
			return
		}
		for _, p := range paths {
			fmt.Fprintf(cmd.OutOrStdout(), "installed skill → %s\n", p)
		}
	},
}

var skillsRemoveCmd = &cobra.Command{
	Use:   "remove",
	Short: "Remove the installed bundled skill",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		n := setup.RemoveInstalledSkills()
		fmt.Fprintf(cmd.OutOrStdout(), "removed %d skill(s)\n", n)
	},
}

var skillsStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show whether the bundled skill is installed",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		dir, err := setup.UserSkillsDir()
		if err != nil {
			exitErr("skills status", err)
		}
		for _, name := range setup.BundledSkillNames {
			p := filepath.Join(dir, name, "SKILL.md")
			state := "not installed"
			if _, err := os.Stat(p); err == nil {
				state = "installed: " + p
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%-16s %s\n", name, state)
		}
	},
}

func init() {
	skillsCmd.AddCommand(skillsInstallCmd)
	skillsCmd.AddCommand(skillsRemoveCmd)
	skillsCmd.AddCommand(skillsStatusCmd)
	rootCmd.AddCommand(skillsCmd)
	commandGroups["skills"] = "util"
}
