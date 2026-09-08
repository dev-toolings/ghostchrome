package cli

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dev-toolings/ghostchrome/internal/core/engine"
)

func TestPlaywrightSnapshotFilename(t *testing.T) {
	ts := time.Date(2026, 2, 14, 19, 22, 42, 679*int(time.Millisecond), time.UTC)
	got := playwrightSnapshotFilename(ts)
	want := "page-2026-02-14T19-22-42-679Z.yml"
	if got != want {
		t.Fatalf("playwrightSnapshotFilename() = %q, want %q", got, want)
	}
}

func TestAppendPlaywrightSnapshotLink(t *testing.T) {
	got := appendPlaywrightSnapshotLink("body\n", ".playwright-cli/page.yml")
	for _, want := range []string{"body", "### Snapshot", "[Snapshot](.playwright-cli/page.yml)"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected %q in %q", want, got)
		}
	}
}

func TestPlaywrightArtifactPathUsesConfiguredOutputDir(t *testing.T) {
	restore := snapshotConfigGlobals()
	defer restore()
	flagConfigOutputDir = filepath.Join("tmp", "pw-output")

	got := playwrightArtifactPath("page.yml")
	want := filepath.Join("tmp", "pw-output", "page.yml")
	if got != want {
		t.Fatalf("playwrightArtifactPath = %q, want %q", got, want)
	}
}

func TestFormatPlaywrightPageStateOutputWritesPageAndSnapshot(t *testing.T) {
	restore := snapshotConfigGlobals()
	defer restore()

	flagConfigOutputDir = t.TempDir()
	flagFormat = "text"
	info := &engine.PageInfo{
		URL:   "https://example.com",
		Title: "Example Domain",
	}
	result := &engine.ExtractionResult{
		Nodes: []engine.ExtractedNode{{Role: "link", Name: "More", Ref: "e1"}},
		Refs:  map[string]engine.ExtractedNode{"e1": {Role: "link", Name: "More", Ref: "e1"}},
	}

	got := formatPlaywrightPageStateOutput(info, result)
	for _, want := range []string{
		"### Page",
		"- Page URL: https://example.com",
		"- Page Title: Example Domain",
		"### Snapshot",
		"[Snapshot](" + flagConfigOutputDir,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected %q in %q", want, got)
		}
	}
}

func TestFormatPlaywrightPageStateOutputKeepsJSONCompact(t *testing.T) {
	restore := snapshotConfigGlobals()
	defer restore()

	flagFormat = "json"
	info := &engine.PageInfo{URL: "https://example.com", Title: "Example Domain", Status: 200, TimeMs: 12}
	got := formatPlaywrightPageStateOutput(info, &engine.ExtractionResult{})
	if strings.Contains(got, "### Page") || strings.Contains(got, "### Snapshot") {
		t.Fatalf("json fallback should stay compact, got %q", got)
	}
	if !strings.Contains(got, "https://example.com") {
		t.Fatalf("expected page URL in %q", got)
	}
}

func TestFormatPlaywrightPageStateOutputForNavigationCommands(t *testing.T) {
	restore := snapshotConfigGlobals()
	defer restore()

	flagConfigOutputDir = t.TempDir()
	flagFormat = "text"
	result := &engine.ExtractionResult{
		Nodes: []engine.ExtractedNode{{Role: "heading", Name: "Welcome", Ref: "e1"}},
		Refs:  map[string]engine.ExtractedNode{"e1": {Role: "heading", Name: "Welcome", Ref: "e1"}},
	}

	commands := []struct {
		action string
		info   *engine.PageInfo
	}{
		{action: "back", info: &engine.PageInfo{URL: "https://example.com/prev", Title: "Previous"}},
		{action: "forward", info: &engine.PageInfo{URL: "https://example.com/next", Title: "Next"}},
		{action: "viewport", info: &engine.PageInfo{URL: "https://example.com", Title: "Test"}},
		{action: "tab-select", info: &engine.PageInfo{URL: "https://example.com/tab", Title: "Tab"}},
		{action: "tab-new", info: &engine.PageInfo{URL: "about:blank", Title: ""}},
		{action: "dialog-accept", info: &engine.PageInfo{URL: "https://example.com/form", Title: "Form"}},
		{action: "dialog-dismiss", info: &engine.PageInfo{URL: "https://example.com/form", Title: "Form"}},
	}

	for _, tc := range commands {
		t.Run(tc.action, func(t *testing.T) {
			got := formatPlaywrightPageStateOutput(tc.info, result)
			for _, want := range []string{"### Page", "- Page URL:", "### Snapshot", "[Snapshot]("} {
				if !strings.Contains(got, want) {
					t.Fatalf("%s output missing %q in:\n%s", tc.action, want, got)
				}
			}
			if !strings.Contains(got, tc.info.URL) {
				t.Fatalf("%s output missing URL %q", tc.action, tc.info.URL)
			}
		})
	}
}

func TestFormatPlaywrightPageStateOutputJSONSkipsSnapshotForAllCommands(t *testing.T) {
	restore := snapshotConfigGlobals()
	defer restore()

	flagFormat = "json"
	result := &engine.ExtractionResult{}

	actions := []string{"back", "forward", "viewport", "tab-select", "tab-new", "dialog-accept", "dialog-dismiss"}
	for _, action := range actions {
		t.Run(action, func(t *testing.T) {
			info := &engine.PageInfo{URL: "https://example.com", Title: "Test", Status: 200}
			got := formatPlaywrightPageStateOutput(info, result)
			if strings.Contains(got, "### Page") || strings.Contains(got, "### Snapshot") {
				t.Fatalf("json mode should not contain Playwright text headers for %s, got %q", action, got)
			}
		})
	}
}

func TestSnapshotModeDefaultsToDiff(t *testing.T) {
	old := flagSnapshotMode
	t.Cleanup(func() { flagSnapshotMode = old })
	flagSnapshotMode = ""
	if got := snapshotMode(); got != engine.SnapshotModeDiff {
		t.Fatalf("snapshotMode() = %q, want diff", got)
	}
}

func TestSnapshotModeRejectsUnknown(t *testing.T) {
	if _, err := engine.ParseSnapshotMode("banana"); err == nil {
		t.Fatal("expected error")
	}
}
