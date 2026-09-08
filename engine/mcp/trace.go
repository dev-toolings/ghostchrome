package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/dev-toolings/ghostchrome/internal/core/artifact"
	mcpgo "github.com/mark3labs/mcp-go/mcp"
	mcpsrv "github.com/mark3labs/mcp-go/server"
)

const redactedTraceValue = "[REDACTED]"

const (
	traceRetention = 24 * time.Hour
	traceMaxBytes  = 1 << 20
)

// traceWriteMu keeps concurrent MCP calls from interleaving their JSONL lines.
var traceWriteMu sync.Mutex

// traceMiddleware records completed tool calls when GHOSTCHROME_MCP_TRACE names
// a file. It never changes a tool result: trace failures are diagnostics only.
func (s *Server) traceMiddleware() mcpsrv.ToolHandlerMiddleware {
	path := strings.TrimSpace(os.Getenv("GHOSTCHROME_MCP_TRACE"))
	return func(next mcpsrv.ToolHandlerFunc) mcpsrv.ToolHandlerFunc {
		return func(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
			started := time.Now()
			result, err := next(ctx, req)
			if path != "" {
				ok := err == nil && result != nil && !result.IsError
				outcome := "error"
				if ok {
					outcome = "success"
				}
				entry := artifact.TraceEntry{
					TS:         started.UnixMilli(),
					Op:         req.Params.Name,
					Args:       redactTraceArgs(req.Params.Name, req.GetArguments()),
					OK:         ok,
					Outcome:    outcome,
					DurationMs: time.Since(started).Milliseconds(),
				}
				if !ok {
					// Errors may repeat a user-supplied value. Keep the trace useful
					// without turning it into a record of tool input or page content.
					entry.Error = "tool call failed"
				}
				if writeErr := appendTrace(path, entry); writeErr != nil {
					fmt.Fprintf(os.Stderr, "[ghostchrome mcp] trace write: %v\n", writeErr)
				}
			}
			return result, err
		}
	}
}

func appendTrace(path string, entry artifact.TraceEntry) error {
	traceWriteMu.Lock()
	defer traceWriteMu.Unlock()

	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	// OpenFile's mode only applies on creation. Always tighten a pre-existing
	// trace as well: these files can contain credentials before redaction rules
	// evolve, and are intended for incident sharing rather than public logs.
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		return err
	}
	encoded, err := encodeTraceEntry(entry)
	if err != nil {
		_ = f.Close()
		return err
	}

	existing, err := os.ReadFile(path)
	if err != nil {
		_ = f.Close()
		return err
	}
	firstTS, hasEntry, corrupt := traceFileState(existing)
	// A partial write must never survive a restart. Discard the existing window
	// at the first invalid/blank JSONL line, then retain the current call.
	reset := corrupt || len(existing)+len(encoded) > traceMaxBytes
	if hasEntry && time.Since(time.UnixMilli(firstTS)) >= traceRetention {
		reset = true
	}
	if reset {
		// Keep the same file and preserve the call that triggered retention;
		// no archive is created, so the trace stays bounded across restarts.
		if err := f.Truncate(0); err != nil {
			_ = f.Close()
			return err
		}
		if _, err := f.Seek(0, 0); err != nil {
			_ = f.Close()
			return err
		}
	} else if _, err := f.Seek(0, 2); err != nil {
		_ = f.Close()
		return err
	}
	if _, err := f.Write(encoded); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// encodeTraceEntry guarantees the hard cap even if an otherwise-safe argument
// is unexpectedly huge. The fallback retains timing, operation and outcome,
// but deliberately drops args, summary and error text.
func encodeTraceEntry(entry artifact.TraceEntry) ([]byte, error) {
	encoded, err := json.Marshal(entry)
	if err != nil {
		return nil, err
	}
	encoded = append(encoded, '\n')
	if len(encoded) <= traceMaxBytes {
		return encoded, nil
	}
	minimal := artifact.TraceEntry{
		TS:         entry.TS,
		Op:         truncateTraceString(entry.Op, 256),
		OK:         entry.OK,
		Outcome:    entry.Outcome,
		DurationMs: entry.DurationMs,
		Summary:    "trace entry truncated",
	}
	encoded, err = json.Marshal(minimal)
	if err != nil {
		return nil, err
	}
	return append(encoded, '\n'), nil
}

func truncateTraceString(value string, max int) string {
	if len(value) <= max {
		return value
	}
	return value[:max]
}

// traceFileState treats every non-final blank or malformed line as corruption.
// On the next append appendTrace resets the same file, making the persisted
// JSONL fully parseable again rather than preserving a broken prefix/suffix.
func traceFileState(data []byte) (firstTS int64, hasEntry, corrupt bool) {
	// JSONL records must be newline-delimited. Even complete JSON without its
	// delimiter is treated as an interrupted record, so the next append cannot
	// concatenate two objects into one invalid line.
	if len(data) > 0 && data[len(data)-1] != '\n' {
		return 0, false, true
	}
	lines := bytes.Split(data, []byte{'\n'})
	for i, line := range lines {
		if len(line) == 0 {
			if i == len(lines)-1 { // normal trailing newline, or an empty file
				continue
			}
			return 0, false, true
		}
		var entry artifact.TraceEntry
		if err := json.Unmarshal(line, &entry); err != nil {
			return 0, false, true
		}
		if !hasEntry {
			firstTS, hasEntry = entry.TS, true
		}
	}
	return firstTS, hasEntry, false
}

func redactTraceArgs(op string, args map[string]any) map[string]any {
	if len(args) == 0 {
		return nil
	}
	redacted := make(map[string]any, len(args))
	for key, value := range args {
		if traceArgIsSensitive(op, key) {
			redacted[key] = redactedTraceValue
			continue
		}
		redacted[key] = redactTraceValue(key, value)
	}
	return redacted
}

func traceArgIsSensitive(op, key string) bool {
	if (op == "type" && key == "text") ||
		(op == "fill_form" && key == "fields") ||
		(op == "eval" && key == "expression") ||
		(op == "upload" && key == "paths") {
		return true
	}
	key = strings.NewReplacer("_", "", "-", "", ".", "", " ", "").Replace(strings.ToLower(key))
	return strings.Contains(key, "password") || strings.Contains(key, "secret") ||
		strings.Contains(key, "token") || strings.Contains(key, "auth") ||
		strings.Contains(key, "credential") || strings.Contains(key, "session") ||
		strings.Contains(key, "jwt") || strings.Contains(key, "bearer") ||
		strings.Contains(key, "oauth") || strings.Contains(key, "cookie") ||
		strings.Contains(key, "apikey") || strings.Contains(key, "code")
}

func redactTraceValue(key string, value any) any {
	if key == "url" {
		if raw, ok := value.(string); ok {
			return redactURL(raw)
		}
	}
	switch v := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for nestedKey, nestedValue := range v {
			if traceArgIsSensitive("", nestedKey) {
				out[nestedKey] = redactedTraceValue
			} else {
				out[nestedKey] = redactTraceValue(nestedKey, nestedValue)
			}
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, nestedValue := range v {
			out[i] = redactTraceValue(key, nestedValue)
		}
		return out
	default:
		return value
	}
}

func redactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		// An unparsable value might still contain a query secret. Do not retain
		// it just because it is malformed.
		return redactedTraceValue
	}
	q := u.Query()
	changed := false
	for key := range q {
		if traceArgIsSensitive("", key) {
			q.Set(key, redactedTraceValue)
			changed = true
		}
	}
	if changed {
		u.RawQuery = q.Encode()
	}
	return u.String()
}
