package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dev-toolings/ghostchrome/internal/core/artifact"
	mcpgo "github.com/mark3labs/mcp-go/mcp"
	mcpsrv "github.com/mark3labs/mcp-go/server"
)

func TestRedactTraceArgs(t *testing.T) {
	args := map[string]any{
		"ref":         "@4",
		"text":        "correct horse battery staple",
		"accessToken": "token-value",
		"url":         "https://example.test/callback?token=token-value&view=full",
	}
	got := redactTraceArgs("type", args)
	if got["ref"] != "@4" {
		t.Fatalf("safe ref = %#v", got["ref"])
	}
	if got["text"] != redactedTraceValue || got["accessToken"] != redactedTraceValue {
		t.Fatalf("sensitive args not redacted: %#v", got)
	}
	if got["url"] != "https://example.test/callback?token=%5BREDACTED%5D&view=full" {
		t.Fatalf("URL query was not redacted: %#v", got["url"])
	}

	for _, opAndKey := range [][2]string{{"fill_form", "fields"}, {"eval", "expression"}, {"upload", "paths"}} {
		args := redactTraceArgs(opAndKey[0], map[string]any{opAndKey[1]: "private"})
		if args[opAndKey[1]] != redactedTraceValue {
			t.Errorf("%s %s = %#v, want redacted", opAndKey[0], opAndKey[1], args[opAndKey[1]])
		}
	}

	nested := redactTraceArgs("navigate", map[string]any{
		"options": map[string]any{
			"oauth_code": "private",
			"session-id": "private",
		},
		"items": []any{map[string]any{"bearer": "private"}},
		"url":   "https://example.test/callback?authorization_code=private&view=full",
	})
	options := nested["options"].(map[string]any)
	if options["oauth_code"] != redactedTraceValue || options["session-id"] != redactedTraceValue {
		t.Fatalf("nested sensitive args not redacted: %#v", options)
	}
	items := nested["items"].([]any)
	if items[0].(map[string]any)["bearer"] != redactedTraceValue {
		t.Fatalf("array sensitive arg not redacted: %#v", items)
	}
	if nested["url"] != "https://example.test/callback?authorization_code=%5BREDACTED%5D&view=full" {
		t.Fatalf("URL query was not redacted: %#v", nested["url"])
	}
	if got := redactURL("https://example.test/%zz?token=private"); got != redactedTraceValue {
		t.Fatalf("malformed URL = %q, want redacted", got)
	}
}

func TestAppendTraceSecuresExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trace.jsonl")
	if err := os.WriteFile(path, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := appendTrace(path, artifact.TraceEntry{Op: "wait_for", OK: true, Outcome: "success"}); err != nil {
		t.Fatalf("append trace: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); runtime.GOOS != "windows" && got != 0o600 {
		t.Fatalf("trace permissions = %o, want 600", got)
	}
}

func TestAppendTraceTruncatesExpiredWindowAndKeepsTrigger(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "trace.jsonl")
	if err := appendTrace(path, artifact.TraceEntry{
		TS: time.Now().Add(-traceRetention).UnixMilli(), Op: "old", OK: true, Outcome: "success",
	}); err != nil {
		t.Fatal(err)
	}
	// mtime is deliberately fresh: the timestamp of the first JSONL entry,
	// not a continuously updated mtime, defines the current window.
	now := time.Now()
	if err := os.Chtimes(path, now, now); err != nil {
		t.Fatal(err)
	}
	trigger := artifact.TraceEntry{TS: now.UnixMilli(), Op: "trigger", OK: true, Outcome: "success"}
	if err := appendTrace(path, trigger); err != nil {
		t.Fatal(err)
	}
	entries, err := artifact.ReadTrace(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Op != "trigger" {
		t.Fatalf("expired window entries = %#v, want only trigger", entries)
	}
	files, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].Name() != "trace.jsonl" {
		t.Fatalf("retention created unexpected files: %#v", files)
	}
}

func TestAppendTraceTruncatesAtSizeCapAndKeepsTrigger(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trace.jsonl")
	if err := appendTrace(path, artifact.TraceEntry{
		TS: time.Now().UnixMilli(), Op: "large", OK: true, Outcome: "success",
		Args: map[string]any{"padding": strings.Repeat("x", traceMaxBytes-150)},
	}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() >= traceMaxBytes {
		t.Fatalf("fixture size = %d, want less than cap %d", info.Size(), traceMaxBytes)
	}
	if err := appendTrace(path, artifact.TraceEntry{TS: time.Now().UnixMilli(), Op: "trigger", OK: true, Outcome: "success"}); err != nil {
		t.Fatal(err)
	}
	entries, err := artifact.ReadTrace(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Op != "trigger" {
		t.Fatalf("size cap entries = %#v, want only trigger", entries)
	}
	info, err = os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() >= traceMaxBytes {
		t.Fatalf("retained trace size = %d, want below cap %d", info.Size(), traceMaxBytes)
	}
}

func TestAppendTraceRepairsCorruptJSONL(t *testing.T) {
	valid, err := json.Marshal(artifact.TraceEntry{TS: time.Now().UnixMilli(), Op: "valid", OK: true, Outcome: "success"})
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name     string
		existing []byte
	}{
		{name: "empty", existing: nil},
		{name: "valid entry without trailing newline", existing: valid},
		{name: "corrupt before valid", existing: append([]byte("{partial\n"), append(valid, '\n')...)},
		{name: "corrupt after valid", existing: append(append(valid, '\n'), []byte("{partial")...)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "trace.jsonl")
			if err := os.WriteFile(path, tc.existing, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := appendTrace(path, artifact.TraceEntry{TS: time.Now().UnixMilli(), Op: "trigger", OK: true, Outcome: "success"}); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			for _, line := range bytes.Split(data, []byte{'\n'}) {
				if len(line) == 0 {
					continue
				}
				var entry artifact.TraceEntry
				if err := json.Unmarshal(line, &entry); err != nil {
					t.Fatalf("final JSONL contains invalid line %q: %v", line, err)
				}
			}
			entries, err := artifact.ReadTrace(path, 0)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 1 || entries[0].Op != "trigger" {
				t.Fatalf("repaired entries = %#v, want only trigger", entries)
			}
		})
	}
}

func TestAppendTraceTruncatesOversizedEntry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trace.jsonl")
	private := strings.Repeat("private-value", traceMaxBytes/4)
	if err := appendTrace(path, artifact.TraceEntry{
		TS: time.Now().UnixMilli(), Op: "navigate", OK: true, Outcome: "success",
		Args:    map[string]any{"large_safe_value": private},
		Summary: private,
		Error:   private,
	}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() > traceMaxBytes {
		t.Fatalf("oversized entry left file at %d bytes, cap %d", info.Size(), traceMaxBytes)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte("private-value")) {
		t.Fatal("oversized value was persisted")
	}
	entries, err := artifact.ReadTrace(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Op != "navigate" || entries[0].Summary != "trace entry truncated" || entries[0].Args != nil || entries[0].Error != "" {
		t.Fatalf("oversized entry fallback = %#v", entries)
	}
}

func TestTraceMiddlewareConcurrentJSONL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trace.jsonl")
	t.Setenv("GHOSTCHROME_MCP_TRACE", path)
	s := New(Options{})
	handler := s.traceMiddleware()(mcpsrv.ToolHandlerFunc(func(context.Context, mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		return mcpgo.NewToolResultText("ok"), nil
	}))
	const calls = 32
	var wg sync.WaitGroup
	errs := make(chan error, calls)
	for i := 0; i < calls; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := handler(context.Background(), mcpgo.CallToolRequest{Params: mcpgo.CallToolParams{
				Name:      "wait_for",
				Arguments: map[string]any{"timeout_ms": 1},
			}})
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent append: %v", err)
		}
	}
	entries, err := artifact.ReadTrace(path, 0)
	if err != nil {
		t.Fatalf("read trace: %v", err)
	}
	if len(entries) != calls {
		t.Fatalf("valid JSONL entries = %d, want %d", len(entries), calls)
	}
}
