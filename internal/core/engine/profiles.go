package engine

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// ProfileInfo describes a persistent Chrome profile on disk
// (~/.ghostchrome/profiles/<name>).
type ProfileInfo struct {
	Name  string `json:"name"`
	Path  string `json:"path"`
	Bytes int64  `json:"bytes"`
}

func profilesDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".ghostchrome", "profiles"), nil
}

// dirSize returns the total byte size of a directory tree (best-effort:
// unreadable entries are skipped).
func dirSize(path string) int64 {
	var total int64
	_ = filepath.WalkDir(path, func(_ string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if info, ierr := d.Info(); ierr == nil {
			total += info.Size()
		}
		return nil
	})
	return total
}

// ListProfiles returns every persistent profile with its on-disk size.
func ListProfiles() ([]ProfileInfo, error) {
	dir, err := profilesDir()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	out := make([]ProfileInfo, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		p := filepath.Join(dir, e.Name())
		out = append(out, ProfileInfo{Name: e.Name(), Path: p, Bytes: dirSize(p)})
	}
	return out, nil
}

// ProfileGCCandidate is an orphan profile eligible for reclamation.
type ProfileGCCandidate struct {
	Name     string    `json:"name"`
	Bytes    int64     `json:"bytes"`
	Modified time.Time `json:"modified"`
}

// dirModTime returns the most recent file modification time in a directory
// tree (best-effort). It is the "last active" signal for a profile: Chrome
// rewrites files under the profile on every use, so an old max-mtime means the
// profile has genuinely been idle.
func dirModTime(path string) time.Time {
	var newest time.Time
	_ = filepath.WalkDir(path, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if info, ierr := d.Info(); ierr == nil {
			if m := info.ModTime(); m.After(newest) {
				newest = m
			}
		}
		return nil
	})
	return newest
}

// GCProfiles finds — and, unless dryRun, deletes — orphan profile directories
// that are safe to reclaim: never the implicit "default" daemon profile, never
// a profile backing a currently-live session, and only those idle for at least
// ttl (no file modified within the window). The idle gate protects persistent
// login profiles that are still in active rotation even when no session is up
// right now. Callers should surface the returned candidates before deleting
// (the CLI defaults to a dry run) so a login profile is never removed silently.
func GCProfiles(ttl time.Duration, dryRun bool) ([]ProfileGCCandidate, error) {
	profiles, err := ListProfiles()
	if err != nil {
		return nil, err
	}

	keep := map[string]bool{DefaultSessionName: true}
	if sessions, serr := ListSessions(); serr == nil {
		for _, s := range sessions {
			if s.Alive && s.Profile != "" {
				keep[s.Profile] = true
			}
		}
	}

	cutoff := time.Now().Add(-ttl)
	var candidates []ProfileGCCandidate
	for _, p := range profiles {
		if keep[p.Name] {
			continue
		}
		mod := dirModTime(p.Path)
		if mod.After(cutoff) {
			continue // recently used — keep
		}
		candidates = append(candidates, ProfileGCCandidate{Name: p.Name, Bytes: p.Bytes, Modified: mod})
	}

	if !dryRun {
		for _, c := range candidates {
			_ = RemoveProfile(c.Name)
		}
	}
	return candidates, nil
}

// RemoveProfile deletes a profile directory. The name is validated and the
// resolved path is confirmed to live under the profiles dir before any
// removal, so it can never escape via traversal.
func RemoveProfile(name string) error {
	if err := validateSessionName(name); err != nil {
		return err
	}
	dir, err := profilesDir()
	if err != nil {
		return err
	}
	target := filepath.Join(dir, name)
	// Defense in depth: the cleaned target must be a direct child of dir.
	if filepath.Dir(target) != filepath.Clean(dir) {
		return fmt.Errorf("refusing to remove %q: outside the profiles directory", target)
	}
	if _, err := os.Stat(target); os.IsNotExist(err) {
		return fmt.Errorf("profile %q not found", name)
	}
	return os.RemoveAll(target)
}

// GhostchromeDataDirs returns the data directories a full uninstall removes:
// ~/.ghostchrome (profiles, contexts, sessions registry) and the
// ~/.cache/ghostchrome session-snapshot cache. Missing dirs are omitted.
func GhostchromeDataDirs() []string {
	var dirs []string
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(home, ".ghostchrome"))
	}
	if cache, err := os.UserCacheDir(); err == nil {
		dirs = append(dirs, filepath.Join(cache, "ghostchrome"))
	}
	existing := make([]string, 0, len(dirs))
	for _, d := range dirs {
		if _, err := os.Stat(d); err == nil {
			existing = append(existing, d)
		}
	}
	return existing
}
