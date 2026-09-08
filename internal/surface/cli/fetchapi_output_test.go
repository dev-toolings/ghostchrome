package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteFetchapiOutputRedactsExplicitFile(t *testing.T) {
	oldOutput, oldSecrets := fetchapiOutput, flagOutputSecrets
	t.Cleanup(func() {
		fetchapiOutput, flagOutputSecrets = oldOutput, oldSecrets
	})

	fetchapiOutput = filepath.Join(t.TempDir(), "response.json")
	flagOutputSecrets = []string{"private-value"}
	if err := writeFetchapiOutput([]byte(`{"token":"private-value","ok":true}`)); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(fetchapiOutput)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "private-value") || !strings.Contains(string(data), "<redacted>") {
		t.Fatalf("fetchapi output = %q", data)
	}
}
