package ai

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/dev-toolings/ghostchrome/internal/core/feedback"
)

// happy path: extract → click → done
func TestRun_HappyPath(t *testing.T) {
	provider := &FakeProvider{
		Steps: []Step{
			{ToolCalls: []ToolCallReq{{ID: "1", Name: "extract", Input: json.RawMessage(`{"level":"content"}`)}}},
			{ToolCalls: []ToolCallReq{{ID: "2", Name: "click", Input: json.RawMessage(`{"ref":"@5"}`)}}},
			{ToolCalls: []ToolCallReq{{ID: "3", Name: "done", Input: json.RawMessage(`{"answer":"clicked the button"}`)}}},
		},
	}

	var ops []string
	runner := &FakeRunner{
		URL: "https://example.com/after",
		Results: map[string]any{
			"extract": map[string]any{"refs": map[string]any{"@5": map[string]any{"role": "button"}}},
			"click":   map[string]any{},
		},
		OnOp: func(op string, _ json.RawMessage) { ops = append(ops, op) },
	}

	res, err := Run(context.Background(), runner, provider, LoopOpts{Goal: "click the button", MaxSteps: 5})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.Success {
		t.Fatalf("expected success, got %+v", res)
	}
	if res.FinalAnswer != "clicked the button" {
		t.Fatalf("answer mismatch: %q", res.FinalAnswer)
	}
	if got, want := strings.Join(ops, ","), "extract,click"; got != want {
		t.Fatalf("ops sequence: got %q want %q", got, want)
	}
	if res.FinalURL != "https://example.com/after" {
		t.Fatalf("final url: %q", res.FinalURL)
	}
	if res.StepsTaken != 3 {
		t.Fatalf("steps_taken: got %d want 3", res.StepsTaken)
	}
}

// hitting MaxSteps without a `done` tool call must fail with a clear error.
func TestRun_MaxStepsReached(t *testing.T) {
	provider := &FakeProvider{
		Steps: []Step{
			{ToolCalls: []ToolCallReq{{ID: "1", Name: "scroll_by", Input: json.RawMessage(`{"dy":200}`)}}},
			{ToolCalls: []ToolCallReq{{ID: "2", Name: "scroll_by", Input: json.RawMessage(`{"dy":200}`)}}},
		},
	}
	runner := &FakeRunner{}
	res, err := Run(context.Background(), runner, provider, LoopOpts{Goal: "scroll forever", MaxSteps: 2})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Success {
		t.Fatalf("expected failure on max-steps, got success")
	}
	if !strings.Contains(res.Error, "max-steps") {
		t.Fatalf("expected max-steps error, got %q", res.Error)
	}
	if res.StepsTaken != 2 {
		t.Fatalf("steps_taken: got %d want 2", res.StepsTaken)
	}
}

// a tool error should surface in the StepRecord but the loop continues
// (the LLM will see the error and choose the next move).
func TestRun_ToolErrorPropagated(t *testing.T) {
	provider := &FakeProvider{
		Steps: []Step{
			{ToolCalls: []ToolCallReq{{ID: "1", Name: "click", Input: json.RawMessage(`{"ref":"@missing"}`)}}},
			{ToolCalls: []ToolCallReq{{ID: "2", Name: "done", Input: json.RawMessage(`{"answer":"gave up"}`)}}},
		},
	}
	runner := &errRunner{op: "click", err: errors.New("stale ref @missing")}

	res, err := Run(context.Background(), runner, provider, LoopOpts{Goal: "x", MaxSteps: 5})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.Steps) < 1 {
		t.Fatalf("expected at least 1 step")
	}
	if res.Steps[0].OK {
		t.Fatalf("first step should be marked failed")
	}
	if !strings.Contains(res.Steps[0].Error, "stale ref") {
		t.Fatalf("error not propagated: %q", res.Steps[0].Error)
	}
	// Loop must have continued past the error: there should be a `done` step.
	last := res.Steps[len(res.Steps)-1]
	if last.Op != "done" {
		t.Fatalf("loop did not reach done; last op was %q", last.Op)
	}
}

// a `done` tool call with answer beginning with "blocked:" marks failure.
func TestRun_BlockedAnswerIsFailure(t *testing.T) {
	provider := &FakeProvider{
		Steps: []Step{
			{ToolCalls: []ToolCallReq{{ID: "1", Name: "done", Input: json.RawMessage(`{"answer":"blocked: datadome interstitial"}`)}}},
		},
	}
	res, err := Run(context.Background(), &FakeRunner{}, provider, LoopOpts{Goal: "x", MaxSteps: 3})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Success {
		t.Fatalf("blocked answer must NOT report success")
	}
	if res.FinalAnswer != "blocked: datadome interstitial" {
		t.Fatalf("answer not preserved: %q", res.FinalAnswer)
	}
}

// errRunner returns err on the named op, nothing on others.
type errRunner struct {
	op  string
	err error
}

func (r *errRunner) RunOp(op string, _ json.RawMessage) (any, *feedback.Observation, error) {
	if op == r.op {
		return nil, nil, r.err
	}
	return nil, nil, nil
}
func (r *errRunner) CurrentURL() string { return "" }
