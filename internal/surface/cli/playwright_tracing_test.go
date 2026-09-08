package cli

import (
	"archive/zip"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteTraceJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trace.json")
	events := []map[string]any{{"name": "EvaluateScript", "ts": float64(123)}}
	if err := writeTraceJSON(path, events, "2026-06-13T12:00:00Z", false); err != nil {
		t.Fatalf("writeTraceJSON: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read trace: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("parse trace: %v", err)
	}
	if payload["playwright_compatible"] != false {
		t.Fatalf("expected playwright_compatible=false, got %#v", payload["playwright_compatible"])
	}
	traceEvents, ok := payload["traceEvents"].([]any)
	if !ok || len(traceEvents) != 1 {
		t.Fatalf("unexpected traceEvents: %#v", payload["traceEvents"])
	}
}

func TestWriteTraceZip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trace.zip")
	events := []map[string]any{{"name": "EvaluateScript", "ts": float64(123)}}
	if err := writeTraceZip(path, events, "2026-06-13T12:00:00Z", false); err != nil {
		t.Fatalf("writeTraceZip: %v", err)
	}
	reader, err := zip.OpenReader(path)
	if err != nil {
		t.Fatalf("open zip: %v", err)
	}
	defer reader.Close()

	files := map[string]bool{}
	for _, file := range reader.File {
		files[file.Name] = true
		if file.Name != "metadata.json" {
			continue
		}
		rc, err := file.Open()
		if err != nil {
			t.Fatalf("open metadata: %v", err)
		}
		var metadata map[string]any
		if err := json.NewDecoder(rc).Decode(&metadata); err != nil {
			_ = rc.Close()
			t.Fatalf("parse metadata: %v", err)
		}
		_ = rc.Close()
		if metadata["playwright_compatible"] != false {
			t.Fatalf("expected playwright_compatible=false, got %#v", metadata["playwright_compatible"])
		}
	}
	for _, name := range []string{"cdp-trace.json", "metadata.json", "README.md"} {
		if !files[name] {
			t.Fatalf("missing zip entry %s in %#v", name, files)
		}
	}
}

func TestWriteTraceOutputSelectsFormat(t *testing.T) {
	dir := t.TempDir()
	events := []map[string]any{{"name": "EvaluateScript"}}
	format, err := writeTraceOutput(filepath.Join(dir, "trace.zip"), events, "2026-06-13T12:00:00Z", false)
	if err != nil {
		t.Fatalf("write zip output: %v", err)
	}
	if format != "ghostchrome-cdp-zip" {
		t.Fatalf("zip format = %q", format)
	}
	format, err = writeTraceOutput(filepath.Join(dir, "trace.json"), events, "2026-06-13T12:00:00Z", false)
	if err != nil {
		t.Fatalf("write json output: %v", err)
	}
	if format != "cdp-json" {
		t.Fatalf("json format = %q", format)
	}
}

func TestDefaultTraceOutputUsesConfiguredOutputDir(t *testing.T) {
	restore := snapshotConfigGlobals()
	defer restore()
	flagConfigOutputDir = filepath.Join("tmp", "pw-output")

	got := defaultTraceOutput("")
	want := filepath.Join("tmp", "pw-output", "trace.zip")
	if got != want {
		t.Fatalf("defaultTraceOutput = %q, want %q", got, want)
	}
	if got := defaultTraceOutput("custom.json"); got != "custom.json" {
		t.Fatalf("defaultTraceOutput explicit = %q", got)
	}
}
