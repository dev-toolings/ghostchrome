package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/dev-toolings/ghostchrome/internal/core/engine"
)

func TestAgentWriteRedactsPayloadAndPreservesProtocol(t *testing.T) {
	old := flagOutputSecrets
	flagOutputSecrets = []string{"secret", "42", "a\"b\n"}
	t.Cleanup(func() { flagOutputSecrets = old })
	var buf bytes.Buffer
	s := agentWriter{enc: json.NewEncoder(&buf)}
	s.write(agentResponse{ID: "secret", OK: true, Result: map[string]any{"value": "a\"b\n secret 42", "number": 42}, Error: "secret", Events: []engine.ObserverEvent{{Text: "secret"}}})
	var got agentResponse
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	result := got.Result.(map[string]any)
	if got.ID != "secret" || !got.OK || got.Protocol != engine.ProtocolVersion || result["number"] != float64(42) {
		t.Fatalf("protocol or types changed: %+v", got)
	}
	if strings.Contains(result["value"].(string), "secret") || got.Error != "<redacted>" || got.Events[0].Text != "<redacted>" {
		t.Fatalf("payload not redacted: %+v", got)
	}
}

func TestSnapshotArtifactRedactsSecrets(t *testing.T) {
	oldDir, oldSecrets := flagConfigOutputDir, flagOutputSecrets
	flagConfigOutputDir, flagOutputSecrets = t.TempDir(), []string{"secret-value"}
	t.Cleanup(func() { flagConfigOutputDir, flagOutputSecrets = oldDir, oldSecrets })
	path, err := writePlaywrightSnapshotArtifact(&engine.ExtractionResult{Nodes: []engine.ExtractedNode{{Role: "button", Name: "secret-value"}}})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "secret-value") || !strings.Contains(string(data), "<redacted>") {
		t.Fatalf("unsafe artifact: %s", data)
	}
}
