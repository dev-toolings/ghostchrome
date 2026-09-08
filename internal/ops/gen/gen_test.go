package gen_test

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/dev-toolings/ghostchrome/internal/ops/gen"
)

// TestGeneratedFilesAreCurrent is the successor to the old surface parity test.
//
// Parity itself is no longer testable: the JSONL dispatch table, the MCP tool
// table and the AI tool specs are all emitted from ops.Catalog(), so a surface
// cannot expose an op the catalog does not declare. What structure cannot rule
// out is a catalog edit committed without running `go generate`, which is the
// single manual step left. This test closes exactly that gap, and it compares
// full content rather than only op names, so a changed description, enum or
// default is caught too.
func TestGeneratedFilesAreCurrent(t *testing.T) {
	files, err := gen.Files()
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if len(files) != 4 {
		t.Fatalf("expected 4 generated artefacts, got %d", len(files))
	}

	root := repoRoot(t)
	for _, f := range files {
		path := filepath.Join(root, filepath.FromSlash(f.Path))
		committed, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("%s: %v (run: go generate ./internal/ops/...)", f.Path, err)
			continue
		}
		if !bytes.Equal(committed, f.Content) {
			t.Errorf("%s is stale: it does not match what the current catalog generates. Run: go generate ./internal/ops/...", f.Path)
		}
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot resolve the test source path")
	}
	// file = <root>/internal/ops/gen/gen_test.go
	return filepath.Join(filepath.Dir(file), "..", "..", "..")
}
