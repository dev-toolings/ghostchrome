package engine

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestDirectTestLaunchersAreIsolated prevents integration tests from silently
// repopulating launcher.DefaultUserDataDirPrefix. Every direct Rod launcher in
// engine and CLI surface tests must select an explicit test-owned profile and
// clean it.
func TestDirectTestLaunchersAreIsolated(t *testing.T) {
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test source path")
	}
	// currentFile is <repo>/internal/core/engine/<this file>.
	repoRoot := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(currentFile))))

	var launchers int
	for _, relRoot := range []string{
		filepath.Join("internal", "core", "engine"),
		filepath.Join("internal", "surface", "cli"),
	} {
		err := filepath.WalkDir(filepath.Join(repoRoot, relRoot), func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), "_test.go") {
				return nil
			}
			if entry.Name() == "test_launcher_hygiene_test.go" {
				return nil
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			source := string(data)
			for offset := 0; ; {
				newAt := strings.Index(source[offset:], "launcher.New()")
				if newAt < 0 {
					break
				}
				newAt += offset
				nextAt := strings.Index(source[newAt+1:], "launcher.New()")
				if nextAt >= 0 {
					nextAt += newAt + 1
				} else {
					nextAt = len(source)
				}
				segment := source[newAt:nextAt]
				if strings.Contains(segment, ".Launch()") {
					launchers++
					if !strings.Contains(segment, ".UserDataDir(") && !strings.Contains(segment, "UserDataDir(") {
						t.Errorf("%s: direct launcher has no explicit isolated UserDataDir", path)
					}
					if !strings.Contains(segment, ".Cleanup()") {
						t.Errorf("%s: direct launcher has no Cleanup teardown", path)
					}
				}
				offset = nextAt
			}
			return nil
		})
		if err != nil {
			t.Fatalf("scan %s tests: %v", relRoot, err)
		}
	}
	if launchers != 7 {
		t.Fatalf("direct test launchers inventoried = %d, want 7; review every new launcher explicitly", launchers)
	}
}
