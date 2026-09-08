package setup

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// embeddedSkills maps skill name → SKILL.md content, registered by the binary
// entrypoint via SetEmbeddedSkill (kept for stripped/minimal builds).
var embeddedSkills = map[string]string{}

// BundledSkillNames lists every skill ghostchrome ships, used for removal even
// when the embed is empty (e.g. a stripped build).
var BundledSkillNames = []string{"ghostchrome"}

// SetEmbeddedSkill registers an embedded skill's content (called from the
// binary entrypoint that owns the embed).
func SetEmbeddedSkill(name, content string) {
	if content != "" {
		embeddedSkills[name] = content
	}
}

func UserSkillsDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude", "skills"), nil
}

// InstallEmbeddedSkills writes each bundled skill to ~/.claude/skills/<name>/.
// Returns the installed paths. Best-effort: failures are reported, not fatal.
func InstallEmbeddedSkills() ([]string, error) {
	dir, err := UserSkillsDir()
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

// RemoveInstalledSkills deletes every bundled skill dir from ~/.claude/skills.
// Returns the count removed.
func RemoveInstalledSkills() int {
	dir, err := UserSkillsDir()
	if err != nil {
		return 0
	}
	n := 0
	for _, name := range BundledSkillNames {
		sdir := filepath.Join(dir, name)
		if _, err := os.Stat(sdir); err == nil {
			if err := os.RemoveAll(sdir); err == nil {
				n++
			}
		}
	}
	return n
}

// InstalledSkillDirs returns the on-disk dirs of bundled skills that exist.
func InstalledSkillDirs() []string {
	dir, err := UserSkillsDir()
	if err != nil {
		return nil
	}
	var out []string
	for _, name := range BundledSkillNames {
		sdir := filepath.Join(dir, name)
		if _, err := os.Stat(sdir); err == nil {
			out = append(out, sdir)
		}
	}
	return out
}
