package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dev-toolings/ghostchrome/internal/core/engine"
	"github.com/go-rod/rod"
	"github.com/spf13/cobra"
)

// includeSSR is variadic (not a plain bool) so the ~15 pre-existing
// call sites — internal ref-only skeleton snapshots for click/hover/etc. that
// never need SSR — keep compiling unchanged. Only the navigate --extract path
// passes an explicit value (navChallengeRecovered).
func snapshotPage(b *engine.Browser, page *rod.Page, level engine.ExtractLevel, includeSSR ...bool) *engine.ExtractionResult {
	ssr := len(includeSSR) > 0 && includeSSR[0]
	// An unchanged URL does not imply an unchanged DOM or extraction level.
	// Keep persisted refs, but refresh the tree requested by this command.
	result, err := engine.Extract(page, level, "", ssr)
	if err != nil {
		exitErr("extract", err)
	}
	if err := b.SaveSnapshot(page, result); err != nil {
		exitErr("snapshot", err)
	}
	return result
}

// snapshotPageAfterMutation drops any URL-keyed extract cache before
// snapshotting. Mutating commands (click/type/check/...) can change the DOM
// without navigating, so a cache hit would return stale refs.
func snapshotPageAfterMutation(b *engine.Browser, page *rod.Page, level engine.ExtractLevel, waitForDOM ...bool) *engine.ExtractionResult {
	if err := b.InvalidateCachedExtract(page); err != nil {
		exitErr("snapshot", err)
	}
	if len(waitForDOM) > 0 && waitForDOM[0] {
		if err := engine.WaitForImminentDOM(page, 0); err != nil {
			exitErr("snapshot", err)
		}
	}
	return snapshotPage(b, page, level)
}

var flagSnapshotMode string

func snapshotMode() engine.SnapshotMode {
	mode, err := engine.ParseSnapshotMode(flagSnapshotMode)
	if err != nil {
		exitErr("snapshot", err)
	}
	return mode
}

func registerSnapshotModeFlag(cmd *cobra.Command) {
	if cmd.Flags().Lookup("snapshot") != nil {
		return
	}
	cmd.Flags().StringVar(&flagSnapshotMode, "snapshot", "diff", "Post-action snapshot: none, diff (default), or full")
}

type mutationResult struct {
	Action string                   `json:"action"`
	Ref    string                   `json:"ref,omitempty"`
	Diff   engine.SnapshotDiff      `json:"diff"`
	Result *engine.ExtractionResult `json:"result,omitempty"`
}

func emitMutationOutput(action, ref string, b *engine.Browser, page *rod.Page, extra any) {
	mode := snapshotMode()
	prev := b.Snapshot(page)
	switch mode {
	case engine.SnapshotModeNone:
		if extra != nil {
			output(extra, action+" ok")
			return
		}
		output(&actionResult{Action: action, Ref: ref}, action+" ok")
		return
	case engine.SnapshotModeFull:
		result := snapshotPageAfterMutation(b, page, engine.LevelSkeleton, true)
		text := formatCurrentPlaywrightPageStateOutput(action, page, result)
		if extra != nil {
			output(extra, text)
			return
		}
		output(&actionResult{Action: action, Ref: ref, Result: result}, text)
		return
	default:
		diff, result, err := engine.CaptureMutation(b, page, prev)
		if err != nil {
			exitErr("snapshot", err)
		}
		text := engine.FormatDiff(diff)
		if extra != nil {
			output(extra, text)
			return
		}
		output(&mutationResult{Action: action, Ref: ref, Diff: diff, Result: result}, text)
	}
}

// trySnapshot attempts a best-effort extraction. On failure (e.g. timeout on
// a heavy page after a long navigation) it logs a warning to stderr and
// returns nil instead of killing the process.
func trySnapshot(b *engine.Browser, page *rod.Page, level engine.ExtractLevel) *engine.ExtractionResult {
	result, err := engine.Extract(page, level, "", false)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[navigate] snapshot skipped: %v\n", err)
		return nil
	}
	_ = b.SaveSnapshot(page, result)
	return result
}

func writePlaywrightSnapshotArtifact(result *engine.ExtractionResult) (string, error) {
	if result == nil {
		return "", nil
	}
	dir := playwrightArtifactDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, playwrightSnapshotFilename(time.Now().UTC()))
	data := []byte(engine.FormatPlaywrightSnapshot(result) + "\n")
	data = redactOutput(data, flagOutputSecrets)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return "", err
	}
	return path, nil
}

func playwrightArtifactDir() string {
	if strings.TrimSpace(flagConfigOutputDir) != "" {
		return flagConfigOutputDir
	}
	return ".playwright-cli"
}

func playwrightArtifactPath(name string) string {
	return filepath.Join(playwrightArtifactDir(), name)
}

func playwrightSnapshotFilename(t time.Time) string {
	t = t.UTC()
	return fmt.Sprintf("page-%s-%03dZ.yml", t.Format("2006-01-02T15-04-05"), t.Nanosecond()/int(time.Millisecond))
}

func appendPlaywrightSnapshotLink(text, path string) string {
	if path == "" {
		return text
	}
	text = strings.TrimRight(text, "\n")
	if text != "" {
		text += "\n\n"
	}
	return text + "### Snapshot\n\n[Snapshot](" + path + ")"
}

func appendAutoPlaywrightSnapshot(text string, result *engine.ExtractionResult) string {
	if flagFormat == "json" || result == nil {
		return text
	}
	path, err := writePlaywrightSnapshotArtifact(result)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[snapshot] artifact skipped: %v\n", err)
		return text
	}
	return appendPlaywrightSnapshotLink(text, path)
}

func formatPlaywrightPageStateOutput(info *engine.PageInfo, result *engine.ExtractionResult) string {
	if flagFormat == "json" {
		return fmt.Sprintf("[%d] %s — %s (%dms)", info.Status, info.Title, info.URL, info.TimeMs)
	}
	path, err := writePlaywrightSnapshotArtifact(result)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[snapshot] artifact skipped: %v\n", err)
		return fmt.Sprintf("[%d] %s — %s (%dms)", info.Status, info.Title, info.URL, info.TimeMs)
	}
	return formatSnapshotPageOutput(info, path, "")
}

func formatCurrentPlaywrightPageStateOutput(action string, page *rod.Page, result *engine.ExtractionResult) string {
	if flagFormat == "json" {
		return engine.FormatTextProfile(result, renderProfile())
	}
	pageInfo, err := page.Info()
	if err != nil {
		exitErr(action, err)
	}
	return formatPlaywrightPageStateOutput(&engine.PageInfo{
		URL:   pageInfo.URL,
		Title: pageInfo.Title,
	}, result)
}

func ensureSnapshot(b *engine.Browser, page *rod.Page, targetURL string, waitStrategy string, level engine.ExtractLevel) *engine.PageSnapshot {
	if targetURL != "" {
		navigateIfRequested(page, targetURL, waitStrategy)
		result := snapshotPage(b, page, level)
		if !b.Connected() {
			snapshot, err := engine.BuildSnapshot(page, result)
			if err != nil {
				exitErr("snapshot", err)
			}
			return snapshot
		}
		snapshot := b.Snapshot(page)
		if snapshot == nil {
			exitErr("snapshot", errors.New("failed to persist page snapshot"))
		}
		return snapshot
	}

	snapshot := b.Snapshot(page)
	if snapshot == nil {
		exitErr("snapshot", errors.New("no snapshot for current page: run preview, extract, or navigate --extract first"))
	}
	return snapshot
}

func exitIfStaleRef(err error, action string) {
	if err == nil {
		return
	}
	if errors.Is(err, engine.ErrStaleRef) {
		exitErr(action, err)
	}
}
