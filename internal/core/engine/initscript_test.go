package engine

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/dev-toolings/ghostchrome/internal/core/policy"
)

func TestRemoveInitScriptRejectsTraversal(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir, err := InitScriptsDir()
	if err != nil {
		t.Fatal(err)
	}
	victim := filepath.Join(filepath.Dir(dir), "victim.js")
	if err := os.WriteFile(victim, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"../victim.js", `..\victim.js`, victim, "", ".", ".."} {
		if err := RemoveInitScript(name); err == nil {
			t.Fatalf("accepted %q", name)
		}
	}
	if _, err := os.Stat(victim); err != nil {
		t.Fatal("deleted file outside scripts directory")
	}
}

func TestInstalledScriptsRunBeforeEachDocument(t *testing.T) {
	if testing.Short() {
		t.Skip("requires Chrome")
	}
	t.Setenv("HOME", t.TempDir())
	source := filepath.Join(t.TempDir(), "installed.js")
	if err := os.WriteFile(source, []byte(`window.installedCount = (window.installedCount || 0) + 1;`), 0600); err != nil {
		t.Fatal(err)
	}
	name, err := AddInitScript(source)
	if err != nil {
		t.Fatal(err)
	}
	b, page := newIsolatedPage(t)
	doc := dataURL(`<script>document.title = String(window.installedCount)</script>`)
	for i := 0; i < 2; i++ {
		if _, err := Navigate(page, doc, "load"); err != nil {
			t.Fatal(err)
		}
		info, err := page.Info()
		if err != nil || info.Title != "1" {
			t.Fatalf("script missing or duplicated: %+v, %v", info, err)
		}
	}
	other, err := NewTab(b.RodBrowser(), doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := other.WaitLoad(); err != nil {
		t.Fatal(err)
	}
	info, err := other.Info()
	if err != nil || info.Title != "1" {
		t.Fatalf("new tab script: %+v, %v", info, err)
	}
	if err := RemoveInitScript(name); err != nil {
		t.Fatal(err)
	}
	if _, err := Navigate(page, doc, "load"); err != nil {
		t.Fatal(err)
	}
	info, err = page.Info()
	if err != nil || info.Title != "undefined" {
		t.Fatalf("removed script still active: %+v, %v", info, err)
	}
}

func TestNewTabChecksPolicyBeforeBrowserAccess(t *testing.T) {
	old := ActivePolicy
	ActivePolicy = policy.FromDomains([]string{"allowed.test"})
	t.Cleanup(func() { ActivePolicy = old })
	if _, err := NewTab(nil, "https://blocked.test"); !errors.Is(err, policy.ErrPolicyDenied) {
		t.Fatalf("expected policy denial before creating a target, got %v", err)
	}
}
