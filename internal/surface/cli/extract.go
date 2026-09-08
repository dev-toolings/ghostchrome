package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/dev-toolings/ghostchrome/engine"
	"github.com/dev-toolings/ghostchrome/internal/core/overlay"
	"github.com/spf13/cobra"
)

var (
	flagLevel           string
	flagSelector        string
	flagDiff            string // "auto", "on", "off"
	flagContentBoundary bool   // wrap page-derived text with ⟦page⟧…⟦/page⟧ sentinel markers
	flagSnapshotFile    string
	flagSnapshotDepth   int
	flagSnapshotRaw     bool
	flagSnapshotBoxes   bool
)

var extractCmd = &cobra.Command{
	Use:     "extract [url]",
	Aliases: []string{"snapshot"},
	Short:   "Extract the DOM as a compact accessibility tree",
	Long: `Extracts the page's accessibility tree and outputs a compact representation.
Can auto-launch Chrome if a URL is provided, or attach to a running Chrome via --connect.

Examples:
  ghostchrome extract https://example.com
  ghostchrome extract https://example.com --level skeleton
  ghostchrome extract --connect ws://... --level full
  ghostchrome extract --connect ws://... --diff on        # diff vs last snapshot

Extraction levels:
  skeleton — interactive elements + landmarks only (minimal tokens)
  content  — skeleton + text, paragraphs, images, list items (default)
  full     — everything with a non-empty name

--diff:
  auto — diff in agent profile when a previous snapshot exists (default)
  on   — always diff (error if no previous snapshot)
  off  — always return the full extraction tree`,
	Args: cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		isSnapshot := cmd.CalledAs() == "snapshot"
		level := engine.ExtractLevel(flagLevel)
		if err := engine.ValidateExtractLevel(level); err != nil {
			exitErr("extract", err)
		}

		b, page := openPage()
		defer b.Close()

		// Snapshot the previous refs *before* re-extracting, so we can diff.
		// Skip when navigating to a new URL — the prior page's snapshot is
		// not a meaningful diff base for the page we're about to load. This
		// is the silent footgun that caused `extract URL --connect` on a
		// long-lived Chrome to return {"added":[...]} or {"unchanged":true}
		// instead of {"nodes":[...]} on the 2nd+ call.
		var prev *engine.PageSnapshot
		targetURL := ""
		targetRef := ""
		targetSelector := flagSelector
		if len(args) > 0 {
			if isSnapshot && isSnapshotRef(args[0]) {
				targetRef = engine.InternalRef(args[0])
			} else if isSnapshot && looksLikeSelector(args[0]) {
				targetSelector = args[0]
			} else if strings.HasPrefix(args[0], "@") {
				targetRef = engine.InternalRef(args[0])
			} else {
				targetURL = args[0]
			}
		}
		hasURL := targetURL != ""
		if targetSelector == "" && flagDiff != "off" && !hasURL {
			prev = b.Snapshot(page)
		}

		if hasURL {
			navigateIfRequested(page, targetURL, "load")
		}

		// includeSSR opts in on the recovery path: navChallengeRecovered is
		// set by navigateIfRequested above when this call's --stealth
		// navigate just detected+cleared a DataDome/Cloudflare challenge.
		result, err := engine.Extract(page, level, targetSelector, consumeNavChallengeRecovered())
		if err != nil {
			exitErr("extract", err)
		}

		var fresh *engine.PageSnapshot
		if targetSelector == "" {
			fresh, err = engine.BuildSnapshot(page, result)
			if err != nil {
				exitErr("snapshot", err)
			}
			if err := b.SaveSnapshot(page, result); err != nil {
				exitErr("snapshot", err)
			}
		}

		if targetRef != "" {
			scoped, ok := engine.ExtractionForRef(result, targetRef)
			if !ok {
				exitErr("snapshot", fmt.Errorf("ref %s not found in current snapshot", targetRef))
			}
			result = scoped
			fresh = nil
		}
		result = engine.LimitExtractionDepth(result, flagSnapshotDepth)
		if isSnapshot && flagSnapshotBoxes {
			overlay.AddExtractionBoxes(page, result)
		}

		profile := renderProfile()
		profile.ContentBoundary = flagContentBoundary
		if shouldDiff(flagDiff, profile, prev, fresh) {
			diff := engine.DiffRefs(prev.Refs, fresh.Refs)
			output(&diff, engine.FormatDiff(diff))
			return
		}
		if flagDiff == "on" && prev == nil {
			os.Stderr.WriteString("extract: --diff on requested but no previous snapshot; returning full tree\n")
		}

		text := renderExtractionOutput(result, profile, isSnapshot)
		writeSnapshotFileIfRequested(result, text, isSnapshot)
		if isSnapshot && !flagSnapshotRaw && flagFormat != "json" {
			pageInfo, err := page.Info()
			if err != nil {
				exitErr("snapshot", err)
			}
			info := &engine.PageInfo{URL: pageInfo.URL, Title: pageInfo.Title}
			snapshotPath := flagSnapshotFile
			if snapshotPath == "" {
				snapshotPath, err = writePlaywrightSnapshotArtifact(result)
				if err != nil {
					exitErr("snapshot", err)
				}
			}
			text = formatSnapshotPageOutput(info, snapshotPath, text)
		}
		output(result, text)
	},
}

func renderExtractionOutput(result *engine.ExtractionResult, profile engine.RenderProfile, isSnapshot bool) string {
	if isSnapshot && flagFormat != "json" {
		return engine.FormatPlaywrightSnapshot(result)
	}
	return engine.FormatTextProfile(result, profile)
}

func writeSnapshotFileIfRequested(result *engine.ExtractionResult, text string, isSnapshot bool) {
	if flagSnapshotFile == "" {
		return
	}
	data := []byte(text + "\n")
	if flagFormat == "json" && !isSnapshot {
		var err error
		data, err = json.MarshalIndent(result, "", "  ")
		if err != nil {
			exitErr("snapshot --filename", err)
		}
		data = append(data, '\n')
	}
	if err := os.WriteFile(flagSnapshotFile, data, 0o600); err != nil {
		exitErr("snapshot --filename", err)
	}
}

func formatSnapshotPageOutput(info *engine.PageInfo, filename, snapshotText string) string {
	var b strings.Builder
	b.WriteString("### Page\n\n")
	fmt.Fprintf(&b, "- Page URL: %s\n\n", info.URL)
	fmt.Fprintf(&b, "- Page Title: %s\n\n", info.Title)
	b.WriteString("### Snapshot\n\n")
	if filename != "" {
		fmt.Fprintf(&b, "[Snapshot](%s)\n", filename)
	} else {
		b.WriteString(snapshotText)
		if snapshotText != "" {
			b.WriteString("\n")
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func isSnapshotRef(value string) bool {
	if strings.HasPrefix(value, "@") {
		return true
	}
	if len(value) < 2 || value[0] != 'e' {
		return false
	}
	for _, r := range value[1:] {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func looksLikeSelector(value string) bool {
	if value == "" {
		return false
	}
	if looksLikeURL(value) {
		return false
	}
	if strings.HasPrefix(value, "#") || strings.HasPrefix(value, ".") || strings.HasPrefix(value, "[") {
		return true
	}
	return strings.ContainsAny(value, " >:[")
}

func looksLikeURL(value string) bool {
	lower := strings.ToLower(value)
	return strings.Contains(lower, "://") ||
		strings.HasPrefix(lower, "about:") ||
		strings.HasPrefix(lower, "data:") ||
		strings.HasPrefix(lower, "file:") ||
		strings.HasPrefix(lower, "http:") ||
		strings.HasPrefix(lower, "https:")
}

func shouldDiff(mode string, profile engine.RenderProfile, prev, curr *engine.PageSnapshot) bool {
	if prev == nil || curr == nil {
		return false
	}
	switch mode {
	case "on":
		return true
	case "off":
		return false
	default:
		// auto: diff when in agent profile and we actually have a prior snapshot
		return profile.Agent
	}
}

func init() {
	extractCmd.Flags().StringVar(&flagLevel, "level", "content", "Extraction level: skeleton, content, or full")
	extractCmd.Flags().StringVar(&flagSelector, "selector", "", "CSS selector to scope extraction to a subtree")
	extractCmd.Flags().StringVar(&flagDiff, "diff", "auto", "Return diff vs last snapshot: auto, on, off")
	extractCmd.Flags().StringVar(&flagSnapshotFile, "filename", "", "Write snapshot output to this file")
	extractCmd.Flags().IntVar(&flagSnapshotDepth, "depth", -1, "Limit snapshot tree depth (-1 = unlimited)")
	extractCmd.Flags().BoolVar(&flagSnapshotRaw, "raw", false, "Return only snapshot tree output")
	extractCmd.Flags().BoolVar(&flagSnapshotBoxes, "boxes", false, "Include viewport-relative element boxes in CSS pixels")
	extractCmd.Flags().BoolVar(&flagContentBoundary, "content-boundary", false, "Wrap page-derived text with sentinel markers (⟦page⟧…⟦/page⟧) for prompt-injection defense")
	rootCmd.AddCommand(extractCmd)
}
