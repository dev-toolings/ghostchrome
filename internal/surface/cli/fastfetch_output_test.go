package cli

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestWriteFastfetchOutputRedactsExplicitFile(t *testing.T) {
	oldOutput, oldSecrets := fastfetchOutput, flagOutputSecrets
	t.Cleanup(func() {
		fastfetchOutput, flagOutputSecrets = oldOutput, oldSecrets
	})

	fastfetchOutput = filepath.Join(t.TempDir(), "page.html")
	flagOutputSecrets = []string{"private-value"}
	if err := writeFastfetchOutput([]byte("<p>private-value visible</p>")); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(fastfetchOutput)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "private-value") || !strings.Contains(string(data), "<redacted>") {
		t.Fatalf("fastfetch output = %q", data)
	}
	info, err := os.Stat(fastfetchOutput)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("fastfetch output mode = %o", info.Mode().Perm())
	}
}
