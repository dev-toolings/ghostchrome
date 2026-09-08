//go:build ignore

// gen writes every artefact the ops catalog owns.
// Invoked via: go generate ./internal/ops/...
// or directly:  go run ./internal/ops/cmd/gen/main.go
//
// The rendering lives in internal/ops/gen so a test can regenerate in memory
// and check that what is committed is current; this file is only the writer.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/dev-toolings/ghostchrome/internal/ops"
	"github.com/dev-toolings/ghostchrome/internal/ops/gen"
)

func main() {
	if errs := ops.Validate(); len(errs) > 0 {
		for _, err := range errs {
			fmt.Fprintf(os.Stderr, "gen: catalog: %v\n", err)
		}
		fmt.Fprintf(os.Stderr, "gen: %d catalog error(s), nothing written\n", len(errs))
		os.Exit(1)
	}

	files, err := gen.Files()
	if err != nil {
		fmt.Fprintf(os.Stderr, "gen: %v\n", err)
		os.Exit(1)
	}

	root := repoRoot()
	for _, f := range files {
		out := filepath.Join(root, filepath.FromSlash(f.Path))
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
			fmt.Fprintf(os.Stderr, "gen: mkdir: %v\n", err)
			os.Exit(1)
		}
		if err := os.WriteFile(out, f.Content, 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "gen: write %s: %v\n", out, err)
			os.Exit(1)
		}
		fmt.Printf("gen: wrote %s\n", f.Path)
	}
	fmt.Printf("gen: %d ops, %d files\n", len(ops.Catalog()), len(files))
}

// repoRoot resolves the repository root relative to this source file so the
// generator works regardless of the caller's working directory.
func repoRoot() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		fmt.Fprintf(os.Stderr, "gen: cannot resolve source path\n")
		os.Exit(1)
	}
	// file = .../internal/ops/cmd/gen/main.go
	// repo root is 4 levels up (gen/ -> cmd/ -> ops/ -> internal/ -> root)
	return filepath.Join(filepath.Dir(file), "..", "..", "..", "..")
}
