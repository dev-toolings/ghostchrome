package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

// embeddedSkills maps skill name → SKILL.md content, registered from main via
// SetEmbeddedSkill (kept for compatibility with stripped/minimal builds).
var embeddedSkills = map[string]string{}

// bundledSkillNames lists every skill ghostchrome ships, used for removal even
// when the embed is empty (e.g. a stripped build).
var bundledSkillNames = []string{"ghostchrome"}

// SetEmbeddedSkill registers an embedded skill's content (called from main).
func SetEmbeddedSkill(name, content string) {
	if content != "" {
		embeddedSkills[name] = content
	}
}

func userSkillsDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude", "skills"), nil
}

// installEmbeddedSkills writes each bundled skill to ~/.claude/skills/<name>/.
// Returns the installed paths. Best-effort: failures are reported, not fatal.
func installEmbeddedSkills() ([]string, error) {
	dir, err := userSkillsDir()
	if err != nil {
		return nil, err
	}
	var done []string
	for name, content := range embeddedSkills {
		sdir := filepath.Join(dir, name)
		if err := os.MkdirAll(sdir, 0o755); err != nil {
			return done, fmt.Errorf("%s: %w", name, err)
		}
		files := map[string]string{"SKILL.md": content}
		if name == "ghostchrome" {
			for relative, fileContent := range embeddedSkillFiles {
				files[relative] = fileContent
			}
		}
		for relative, fileContent := range files {
			if relative == "" || filepath.IsAbs(relative) || relative == "." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || relative == ".." {
				return done, fmt.Errorf("%s: invalid embedded path %q", name, relative)
			}
			path := filepath.Join(sdir, filepath.FromSlash(relative))
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return done, fmt.Errorf("%s: %w", name, err)
			}
			if err := os.WriteFile(path, []byte(fileContent), 0o644); err != nil {
				return done, fmt.Errorf("%s: %w", name, err)
			}
			done = append(done, path)
		}
	}
	return done, nil
}

// removeInstalledSkills deletes every bundled skill dir from ~/.claude/skills.
// Returns the count removed.
func removeInstalledSkills() int {
	dir, err := userSkillsDir()
	if err != nil {
		return 0
	}
	n := 0
	for _, name := range bundledSkillNames {
		sdir := filepath.Join(dir, name)
		if _, err := os.Stat(sdir); err == nil {
			if err := os.RemoveAll(sdir); err == nil {
				n++
			}
		}
	}
	return n
}

// installedSkillDirs returns the on-disk dirs of bundled skills that exist.
func installedSkillDirs() []string {
	dir, err := userSkillsDir()
	if err != nil {
		return nil
	}
	var out []string
	for _, name := range bundledSkillNames {
		sdir := filepath.Join(dir, name)
		if _, err := os.Stat(sdir); err == nil {
			out = append(out, sdir)
		}
	}
	return out
}

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
		paths, err := installEmbeddedSkills()
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
		n := removeInstalledSkills()
		fmt.Fprintf(cmd.OutOrStdout(), "removed %d skill(s)\n", n)
	},
}

var skillsStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show whether the bundled skill is installed",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		dir, err := userSkillsDir()
		if err != nil {
			exitErr("skills status", err)
		}
		for _, name := range bundledSkillNames {
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
