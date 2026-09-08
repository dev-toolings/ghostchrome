package cli

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/dev-toolings/ghostchrome/internal/core/artifact"
	"github.com/dev-toolings/ghostchrome/internal/setup"
)

// TestMCPTraceStdioSequential exercises the public stdio boundary: initialize,
// one successful tools/call, one failed tools/call, then trace-replay.
func TestMCPTraceStdioSequential(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "ghostchrome"+setup.BinarySuffix())
	build := exec.Command("go", "build", "-o", bin, "../../../cmd/ghostchrome")
	build.Dir = "."
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build ghostchrome: %v\n%s", err, output)
	}

	tracePath := filepath.Join(t.TempDir(), "mcp-trace.jsonl")
	server := exec.Command(bin, "mcp")
	server.Env = append(os.Environ(), "GHOSTCHROME_MCP_LAZY=1", "GHOSTCHROME_MCP_TRACE="+tracePath)
	stdin, err := server.StdinPipe()
	if err != nil {
		t.Fatalf("mcp stdin: %v", err)
	}
	stdout, err := server.StdoutPipe()
	if err != nil {
		t.Fatalf("mcp stdout: %v", err)
	}
	var stderr bytes.Buffer
	server.Stderr = &stderr
	if err := server.Start(); err != nil {
		t.Fatalf("start mcp server: %v", err)
	}
	scanner := bufio.NewScanner(stdout)
	write := func(request string) {
		t.Helper()
		if _, err := fmt.Fprintln(stdin, request); err != nil {
			t.Fatalf("write MCP request: %v", err)
		}
	}
	readResponse := func() {
		t.Helper()
		if !scanner.Scan() {
			t.Fatalf("read MCP response: %v\nstderr:\n%s", scanner.Err(), stderr.String())
		}
		var response map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &response); err != nil {
			t.Fatalf("stdout is not pure JSON-RPC: %v\nline: %s", err, scanner.Text())
		}
		if response["jsonrpc"] != "2.0" {
			t.Fatalf("stdout is not JSON-RPC 2.0: %#v", response)
		}
	}
	write(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"trace-test","version":"1"}}}`)
	readResponse()
	write(`{"jsonrpc":"2.0","method":"notifications/initialized","params":{}}`)
	write(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"wait_for","arguments":{"timeout_ms":1}}}`)
	readResponse()
	write(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"type","arguments":{"text":"correct horse battery staple","access_token":"token-value"}}}`)
	readResponse()
	if err := stdin.Close(); err != nil {
		t.Fatalf("close MCP stdin: %v", err)
	}
	if err := server.Wait(); err != nil {
		t.Fatalf("mcp server: %v\nstderr:\n%s", err, stderr.String())
	}

	entries, err := artifact.ReadTrace(tracePath, 0)
	if err != nil {
		t.Fatalf("read trace: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("trace entries = %d, want 2: %#v", len(entries), entries)
	}
	if got := entries[0]; got.Op != "wait_for" || !got.OK || got.Outcome != "success" || got.DurationMs < 0 {
		t.Fatalf("successful trace entry = %#v", got)
	}
	if got := entries[1]; got.Op != "type" || got.OK || got.Outcome != "error" || got.Error == "" || got.DurationMs < 0 {
		t.Fatalf("failing trace entry = %#v", got)
	}
	if got := entries[1].Args; got["text"] != "[REDACTED]" || got["access_token"] != "[REDACTED]" {
		t.Fatalf("persisted redaction = %#v", got)
	}
	traceBytes, err := os.ReadFile(tracePath)
	if err != nil {
		t.Fatalf("read raw trace: %v", err)
	}
	if bytes.Contains(traceBytes, []byte("correct horse battery staple")) || bytes.Contains(traceBytes, []byte("token-value")) {
		t.Fatalf("raw trace persisted sensitive input: %s", traceBytes)
	}

	replay := exec.Command(bin, "trace-replay", "--file", tracePath, "--format", "json")
	output, err := replay.CombinedOutput()
	if err != nil {
		t.Fatalf("trace-replay: %v\n%s", err, output)
	}
	var payload struct {
		Count   int                   `json:"count"`
		Entries []artifact.TraceEntry `json:"entries"`
	}
	if err := json.Unmarshal(output, &payload); err != nil {
		t.Fatalf("trace-replay JSON: %v\n%s", err, output)
	}
	if payload.Count != 2 || len(payload.Entries) != 2 {
		t.Fatalf("trace-replay payload = %#v", payload)
	}
}

func TestMCPTraceWriteFailureStdio(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "ghostchrome"+setup.BinarySuffix())
	build := exec.Command("go", "build", "-o", bin, "../../../cmd/ghostchrome")
	build.Dir = "."
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build ghostchrome: %v\n%s", err, output)
	}

	tracePath := t.TempDir() // A directory cannot be opened as a trace file.
	server := exec.Command(bin, "mcp")
	server.Env = append(os.Environ(), "GHOSTCHROME_MCP_LAZY=1", "GHOSTCHROME_MCP_TRACE="+tracePath)
	stdin, err := server.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := server.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	server.Stderr = &stderr
	if err := server.Start(); err != nil {
		t.Fatal(err)
	}
	scanner := bufio.NewScanner(stdout)
	write := func(request string) {
		t.Helper()
		if _, err := fmt.Fprintln(stdin, request); err != nil {
			t.Fatal(err)
		}
	}
	readResponse := func() map[string]any {
		t.Helper()
		if !scanner.Scan() {
			t.Fatalf("read MCP response: %v\nstderr:\n%s", scanner.Err(), stderr.String())
		}
		var response map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &response); err != nil {
			t.Fatalf("stdout is not JSON-RPC: %v\n%s", err, scanner.Text())
		}
		return response
	}
	write(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"trace-test","version":"1"}}}`)
	readResponse()
	write(`{"jsonrpc":"2.0","method":"notifications/initialized","params":{}}`)
	write(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"wait_for","arguments":{"timeout_ms":1}}}`)
	response := readResponse()
	if response["error"] != nil {
		t.Fatalf("trace write failure changed MCP response: %#v", response)
	}
	if err := stdin.Close(); err != nil {
		t.Fatal(err)
	}
	if err := server.Wait(); err != nil {
		t.Fatalf("mcp server: %v\nstderr:\n%s", err, stderr.String())
	}
	if !bytes.Contains(stderr.Bytes(), []byte("trace write:")) {
		t.Fatalf("trace write failure was not visible on stderr:\n%s", stderr.String())
	}
}
