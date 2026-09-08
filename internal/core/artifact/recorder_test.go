package artifact

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"
)

// syntheticRecorder creates a Recorder wired to a buffer without a real page.
// It only tests the coalescer logic (no CDP, no browser).
func newTestRecorder(buf *bytes.Buffer) *Recorder {
	r := &Recorder{
		w:           buf,
		enc:         json.NewEncoder(buf),
		done:        make(chan struct{}),
		typeTimers:  map[string]*time.Timer{},
		typePending: map[string]string{},
	}
	return r
}

func decodeOps(t *testing.T, buf *bytes.Buffer) []RecorderOp {
	t.Helper()
	var ops []RecorderOp
	dec := json.NewDecoder(buf)
	for dec.More() {
		var op RecorderOp
		if err := dec.Decode(&op); err != nil {
			t.Fatalf("decode op: %v", err)
		}
		ops = append(ops, op)
	}
	return ops
}

func TestTypingCoalesce_SingleField(t *testing.T) {
	buf := &bytes.Buffer{}
	r := newTestRecorder(buf)

	// Simulate rapid keystrokes: "h", "he", "hel", "hell", "hello"
	for _, partial := range []string{"h", "he", "hel", "hell", "hello"} {
		r.coalesceType("#search", partial)
	}

	// Wait for debounce then flush to avoid racing the timer callback.
	time.Sleep(350 * time.Millisecond)
	r.flushAllTyping()

	ops := decodeOps(t, buf)
	if len(ops) != 1 {
		t.Fatalf("expected 1 op, got %d: %v", len(ops), ops)
	}
	if ops[0].Op != "type" {
		t.Fatalf("expected op=type, got %q", ops[0].Op)
	}
	if ops[0].Args["text"] != "hello" {
		t.Fatalf("expected text=hello, got %q", ops[0].Args["text"])
	}
	if ops[0].Args["target"] != "#search" {
		t.Fatalf("expected target=#search, got %q", ops[0].Args["target"])
	}
}

func TestTypingCoalesce_TwoFields(t *testing.T) {
	buf := &bytes.Buffer{}
	r := newTestRecorder(buf)

	r.coalesceType("#email", "user@example.com")
	r.coalesceType("#password", "hunter2")

	time.Sleep(350 * time.Millisecond)
	r.flushAllTyping()

	ops := decodeOps(t, buf)
	if len(ops) != 2 {
		t.Fatalf("expected 2 ops, got %d", len(ops))
	}

	opsByTarget := map[string]RecorderOp{}
	for _, op := range ops {
		opsByTarget[op.Args["target"].(string)] = op
	}

	if opsByTarget["#email"].Args["text"] != "user@example.com" {
		t.Errorf("bad text for #email: %v", opsByTarget["#email"])
	}
	if opsByTarget["#password"].Args["text"] != "hunter2" {
		t.Errorf("bad text for #password: %v", opsByTarget["#password"])
	}
}

func TestTypingCoalesce_FlushOnStop(t *testing.T) {
	buf := &bytes.Buffer{}
	r := newTestRecorder(buf)
	r.done = make(chan struct{})

	r.coalesceType("#q", "foo")
	// flushAllTyping before debounce fires
	r.flushAllTyping()

	// Debounce timer should not fire a second time; buf must have exactly 1 op.
	time.Sleep(450 * time.Millisecond)

	ops := decodeOps(t, buf)
	if len(ops) != 1 {
		t.Fatalf("expected 1 op after flush, got %d", len(ops))
	}
	if ops[0].Args["text"] != "foo" {
		t.Fatalf("expected text=foo, got %q", ops[0].Args["text"])
	}
}

func TestTypingCoalesce_ResetOnNewInput(t *testing.T) {
	buf := &bytes.Buffer{}
	r := newTestRecorder(buf)

	r.coalesceType("#field", "ab")
	// immediately update before debounce fires
	time.Sleep(100 * time.Millisecond)
	r.coalesceType("#field", "abc")

	// Flush deterministically instead of racing the timer.
	time.Sleep(350 * time.Millisecond)
	r.flushAllTyping()

	ops := decodeOps(t, buf)
	if len(ops) != 1 {
		t.Fatalf("expected 1 op (debounce reset), got %d", len(ops))
	}
	if ops[0].Args["text"] != "abc" {
		t.Fatalf("expected text=abc (latest value), got %q", ops[0].Args["text"])
	}
}
