// Package ghostchrome holds the assets that can only be embedded from the
// repository root.
//
// A //go:embed pattern is resolved against the directory of the file that
// declares it and cannot escape that subtree. The canonical agent skill lives
// at .claude/skills/ghostchrome (it is copied unchanged to Claude, Codex and
// Grok, so it cannot move), which leaves the root package as the only place
// able to embed it once the CLI main moved to cmd/ghostchrome.
//
// This package carries no logic: cmd/ghostchrome reads the bundle from here
// and hands it to the CLI surface at startup.
package ghostchrome

import (
	"embed"
	"io/fs"
	"strings"
)

// skillRoot is the in-repo path of the bundled skill, and the prefix trimmed
// from every embedded path to produce a skill-relative name.
const skillRoot = ".claude/skills/ghostchrome"

// SkillMarkdown is the skill entrypoint, installed globally by
// `ghostchrome skills install` and removed on uninstall.
//
//go:embed all:.claude/skills/ghostchrome/SKILL.md
var SkillMarkdown string

// skillTree carries the complete client-neutral skill bundle in the CLI
// artifact. The standalone MCP artifact is runtime-only; the release installer
// downloads the same tree as a separate asset.
//
//go:embed all:.claude/skills/ghostchrome
var skillTree embed.FS

// SkillFiles returns every file of the bundled skill, keyed by its path
// relative to the skill root.
func SkillFiles() map[string]string {
	files := map[string]string{}
	_ = fs.WalkDir(skillTree, skillRoot, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		data, readErr := skillTree.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		files[strings.TrimPrefix(path, skillRoot+"/")] = string(data)
		return nil
	})
	return files
}
