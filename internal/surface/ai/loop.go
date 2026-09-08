package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
)

// Run drives the observe→think→act loop until the model emits the `done`
// tool, MaxSteps is hit, or the runner returns a fatal error.
func Run(ctx context.Context, runner Runner, provider Provider, opts LoopOpts) (*Result, error) {
	if runner == nil {
		return nil, errors.New("ai.Run: runner required")
	}
	if provider == nil {
		return nil, errors.New("ai.Run: provider required")
	}
	if opts.Goal == "" {
		return nil, errors.New("ai.Run: goal required")
	}
	maxSteps := opts.MaxSteps
	if maxSteps <= 0 {
		maxSteps = 15
	}

	tools := ToolSpecs()
	res := &Result{
		Goal:     opts.Goal,
		Provider: provider.Name(),
	}

	history := []Message{
		{Role: "system", Content: SystemPrompt},
		{Role: "user", Content: "GOAL: " + opts.Goal},
	}

	for step := 1; step <= maxSteps; step++ {
		select {
		case <-ctx.Done():
			res.Error = ctx.Err().Error()
			return res, ctx.Err()
		default:
		}

		decision, err := provider.Step(ctx, history, tools)
		if err != nil {
			res.Error = "provider: " + err.Error()
			return res, err
		}

		// Persist assistant turn (text + tool calls) for the next round.
		history = append(history, Message{
			Role:    "assistant",
			Content: decision.Text,
			Calls:   decision.ToolCalls,
		})

		if len(decision.ToolCalls) == 0 {
			// No tool call and no done — model gave up. Capture text and stop.
			res.Steps = append(res.Steps, StepRecord{Index: step, OK: true, Text: decision.Text})
			res.StepsTaken = step
			res.FinalAnswer = decision.Text
			res.FinalURL = runner.CurrentURL()
			res.Success = decision.StopReason == "end_turn" || decision.StopReason == "stop"
			return res, nil
		}

		for _, call := range decision.ToolCalls {
			rec := StepRecord{Index: step, Op: call.Name, Args: call.Input, Text: decision.Text}

			if call.Name == "done" {
				answer := extractAnswer(call.Input)
				rec.OK = true
				res.Steps = append(res.Steps, rec)
				res.StepsTaken = step
				res.FinalAnswer = answer
				res.FinalURL = runner.CurrentURL()
				res.Success = !strings.HasPrefix(answer, "blocked:")
				return res, nil
			}

			result, obs, runErr := runner.RunOp(call.Name, call.Input)
			rec.Observation = obs
			if runErr != nil {
				rec.OK = false
				rec.Error = runErr.Error()
			} else {
				rec.OK = true
			}
			res.Steps = append(res.Steps, rec)

			if opts.Verbose {
				fmt.Fprintf(os.Stderr, "[ai] step=%d op=%s ok=%v\n", step, call.Name, runErr == nil)
			}
			if opts.OnStep != nil {
				opts.OnStep(rec)
			}

			toolMsg := buildToolMessage(call, result, obs, runErr)
			history = append(history, Message{Role: "tool", Tool: toolMsg})
		}
	}

	// Hit max steps.
	res.StepsTaken = maxSteps
	res.FinalURL = runner.CurrentURL()
	res.Error = fmt.Sprintf("max-steps reached (%d)", maxSteps)
	return res, nil
}

func extractAnswer(raw json.RawMessage) string {
	var a struct {
		Answer string `json:"answer"`
	}
	_ = json.Unmarshal(raw, &a)
	return a.Answer
}

// buildToolMessage compacts the result + observation into a single string the
// LLM sees as the tool's response. We trim the heaviest fields (full DOM,
// base64 screenshots) to keep token usage sane.
func buildToolMessage(call ToolCallReq, result any, obs interface{}, runErr error) *ToolMessage {
	tm := &ToolMessage{CallID: call.ID, Name: call.Name}
	if runErr != nil {
		tm.IsErr = true
		tm.Result = fmt.Sprintf(`{"error":%q}`, runErr.Error())
		return tm
	}
	payload := map[string]any{}
	if result != nil {
		payload["result"] = compactResult(call.Name, result)
	}
	if obs != nil {
		payload["observation"] = obs
	}
	b, err := json.Marshal(payload)
	if err != nil {
		tm.IsErr = true
		tm.Result = fmt.Sprintf(`{"error":"marshal: %s"}`, err.Error())
		return tm
	}
	tm.Result = string(b)
	return tm
}

// compactResult strips heavy fields per op: screenshots → just mime, extract
// is left intact (the agent needs the refs), eval is left intact.
func compactResult(op string, raw any) any {
	switch op {
	case "screenshot":
		if m, ok := raw.(map[string]string); ok {
			return map[string]any{"mime": m["mime"], "size_b64": len(m["base64"])}
		}
	}
	return raw
}
