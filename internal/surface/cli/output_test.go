package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dev-toolings/ghostchrome/internal/compat/playwright"
	"github.com/dev-toolings/ghostchrome/internal/core/engine"
)

func TestRenderOutputBytesRawAndJSON(t *testing.T) {
	oldRaw, oldFormat := flagRaw, flagFormat
	t.Cleanup(func() { flagRaw, flagFormat = oldRaw, oldFormat })

	flagRaw = true
	flagFormat = "text"
	got, err := renderOutputBytes(map[string]string{"value": "ok"}, "ignored", engine.RenderProfile{})
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"value":"ok"}` {
		t.Fatalf("raw output = %q", got)
	}
	got, err = renderOutputBytes(map[string]any{"expression": "1+1", "result": "2"}, "ignored", engine.RenderProfile{})
	if err != nil || string(got) != "2" {
		t.Fatalf("raw wrapped result = %q, %v", got, err)
	}

	flagRaw = false
	flagFormat = "json"
	got, err = renderOutputBytes(map[string]string{"value": "ok"}, "ignored", engine.RenderProfile{Agent: true})
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(got) || strings.Contains(string(got), "\n") {
		t.Fatalf("compact JSON output = %q", got)
	}
}

func TestRedactOutputUsesLongestSecretFirst(t *testing.T) {
	secrets := uniqueSecrets([]string{"token", "token-long", "token"})
	got := string(redactOutput([]byte("token-long token"), secrets))
	if got != "<redacted> <redacted>" {
		t.Fatalf("redacted output = %q", got)
	}
}

func TestRedactOutputCoversJSONEscapedAndMultilineSecrets(t *testing.T) {
	secrets := []string{"<token&value>", "first line\nsecond line"}
	payload, err := json.Marshal(map[string]string{
		"escaped":   secrets[0],
		"multiline": secrets[1],
	})
	if err != nil {
		t.Fatal(err)
	}

	got := redactOutput(payload, secrets)
	if !json.Valid(got) {
		t.Fatalf("redaction made JSON invalid: %q", got)
	}
	for _, secret := range secrets {
		if strings.Contains(string(got), secret) || strings.Contains(string(got), jsonEscapedString(secret)) {
			t.Fatalf("redacted JSON leaked %q: %q", secret, got)
		}
	}
	if strings.Count(string(got), "<redacted>") != 2 {
		t.Fatalf("redacted JSON = %q", got)
	}
}

func TestLimitOutputSpillsRedactedPayloadToArtifact(t *testing.T) {
	oldMax, oldRaw, oldFormat, oldDir := flagOutputMaxSize, flagRaw, flagFormat, flagConfigOutputDir
	t.Cleanup(func() {
		flagOutputMaxSize, flagRaw, flagFormat, flagConfigOutputDir = oldMax, oldRaw, oldFormat, oldDir
	})

	flagOutputMaxSize = 4
	flagRaw = false
	flagFormat = "json"
	flagConfigOutputDir = t.TempDir()
	payload := []byte(`{"secret":"<redacted>"}`)
	got, err := limitOutput(payload)
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Artifact  string `json:"artifact"`
		Truncated bool   `json:"truncated"`
	}
	if err := json.Unmarshal(got, &result); err != nil {
		t.Fatalf("limit output JSON: %v (%s)", err, got)
	}
	if !result.Truncated || filepath.Dir(result.Artifact) != flagConfigOutputDir {
		t.Fatalf("limit result = %+v", result)
	}
	written, err := os.ReadFile(result.Artifact)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(written), "top-secret") || !strings.Contains(string(written), "<redacted>") {
		t.Fatalf("artifact contents = %q", written)
	}
}

func TestLimitOutputArtifactDoesNotLeakJSONEscapedOrMultilineSecrets(t *testing.T) {
	oldMax, oldRaw, oldFormat, oldDir := flagOutputMaxSize, flagRaw, flagFormat, flagConfigOutputDir
	t.Cleanup(func() {
		flagOutputMaxSize, flagRaw, flagFormat, flagConfigOutputDir = oldMax, oldRaw, oldFormat, oldDir
	})

	secrets := []string{"<token&value>", "first line\nsecond line"}
	payload, err := json.Marshal(map[string]string{"escaped": secrets[0], "multiline": secrets[1]})
	if err != nil {
		t.Fatal(err)
	}
	flagOutputMaxSize = 1
	flagRaw = false
	flagFormat = "json"
	flagConfigOutputDir = t.TempDir()
	limited, err := limitOutput(redactOutput(payload, secrets))
	if err != nil {
		t.Fatal(err)
	}
	var result struct{ Artifact string }
	if err := json.Unmarshal(limited, &result); err != nil {
		t.Fatalf("limit output JSON: %v (%s)", err, limited)
	}
	written, err := os.ReadFile(result.Artifact)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range secrets {
		if strings.Contains(string(written), secret) || strings.Contains(string(written), jsonEscapedString(secret)) {
			t.Fatalf("artifact leaked %q: %q", secret, written)
		}
	}
}

func TestLoadConfiguredOutputSecretsCombinesConfigAndDotenv(t *testing.T) {
	oldConfig, oldFile, oldSecrets := loadedPlaywrightConfig, flagSecretsFile, flagOutputSecrets
	t.Cleanup(func() {
		loadedPlaywrightConfig, flagSecretsFile, flagOutputSecrets = oldConfig, oldFile, oldSecrets
	})

	path := filepath.Join(t.TempDir(), "secrets.env")
	if err := os.WriteFile(path, []byte("API_TOKEN=from-file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	loadedPlaywrightConfig = &loadedConfigState{Config: playwright.Config{
		Secrets: map[string]string{"password": "from-config"},
	}}
	flagSecretsFile = path
	if err := loadConfiguredOutputSecrets(); err != nil {
		t.Fatal(err)
	}
	got := string(redactOutput([]byte("from-file from-config visible"), flagOutputSecrets))
	if got != "<redacted> <redacted> visible" {
		t.Fatalf("combined redaction = %q", got)
	}
}
