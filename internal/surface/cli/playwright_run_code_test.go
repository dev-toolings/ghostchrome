package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadRunCodeArgument(t *testing.T) {
	got, err := readRunCode([]string{"async (page) => page.url()"}, "")
	if err != nil {
		t.Fatalf("readRunCode: %v", err)
	}
	if got != "async (page) => page.url()" {
		t.Fatalf("unexpected code %q", got)
	}
}

func TestReadRunCodeFilename(t *testing.T) {
	path := filepath.Join(t.TempDir(), "script.js")
	if err := os.WriteFile(path, []byte("async (page) => page.title()"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := readRunCode(nil, path)
	if err != nil {
		t.Fatalf("readRunCode: %v", err)
	}
	if got != "async (page) => page.title()" {
		t.Fatalf("unexpected code %q", got)
	}
}

func TestReadRunCodeRejectsBothSources(t *testing.T) {
	if _, err := readRunCode([]string{"code"}, "script.js"); err == nil {
		t.Fatal("expected error")
	}
}
