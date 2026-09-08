// Package ai implements an autonomous LLM-driven browser agent that drives
// the existing ghostchrome agent ops. It is provider-agnostic (Anthropic,
// OpenAI) and goes through a Runner adapter so cmd/ai.go can inject the
// real session without exposing internal types.
package ai

import (
	"context"
	"encoding/json"

	"github.com/dev-toolings/ghostchrome/internal/core/feedback"
)

// Message is the cross-provider chat message shape used by the loop.
type Message struct {
	Role    string        `json:"role"` // "system" | "user" | "assistant" | "tool"
	Content string        `json:"content,omitempty"`
	Tool    *ToolMessage  `json:"-"` // present when Role=="tool"
	Calls   []ToolCallReq `json:"-"` // present on assistant messages with tool calls
}

// ToolMessage carries the result of a tool execution back to the LLM.
type ToolMessage struct {
	CallID string
	Name   string
	Result string // serialised JSON result (or error)
	IsErr  bool
}

// ToolCallReq is a tool call requested by the assistant.
type ToolCallReq struct {
	ID    string
	Name  string
	Input json.RawMessage
}

// Step is the LLM's decision for one iteration.
type Step struct {
	Text        string        // assistant prose (kept for trace)
	ToolCalls   []ToolCallReq // 0+ tool calls to execute (often 1)
	StopReason  string        // "end_turn" | "tool_use" | "max_tokens" | …
	FinalAnswer string        // populated when the model emits the `done` tool
}

// Provider is the LLM backend abstraction. Implementations are stateless;
// the loop accumulates the conversation and replays it on every Step call.
type Provider interface {
	Name() string
	Step(ctx context.Context, history []Message, tools []ToolSpec) (Step, error)
}

// ToolSpec is the JSON-Schema-shaped tool definition shared with the LLM.
type ToolSpec struct {
	Name        string
	Description string
	InputSchema map[string]any // JSON Schema object
}

// Runner is the adapter the loop uses to actually execute tools against the
// browser. cmd/ai.go provides an implementation that wraps a runtime.Session.
type Runner interface {
	RunOp(op string, args json.RawMessage) (result any, obs *feedback.Observation, err error)
	CurrentURL() string
}

// LoopOpts configures one Run call.
type LoopOpts struct {
	Goal     string
	MaxSteps int  // hard cap (default 15)
	Verbose  bool // emit one stderr trace line per tool call
	OnStep   func(StepRecord)
}

// StepRecord is appended to Result.Steps for each iteration. Compact by
// design — never embed raw DOM/HTML payloads here.
type StepRecord struct {
	Index       int                   `json:"index"`
	Op          string                `json:"op,omitempty"`
	Args        json.RawMessage       `json:"args,omitempty"`
	OK          bool                  `json:"ok"`
	Error       string                `json:"error,omitempty"`
	Observation *feedback.Observation `json:"observation,omitempty"`
	Text        string                `json:"text,omitempty"` // assistant prose for this step
}

// Result is the JSON envelope emitted by `ghostchrome ai`.
type Result struct {
	Success     bool         `json:"success"`
	Goal        string       `json:"goal"`
	Provider    string       `json:"provider"`
	Model       string       `json:"model"`
	Steps       []StepRecord `json:"steps"`
	StepsTaken  int          `json:"steps_taken"`
	FinalURL    string       `json:"final_url,omitempty"`
	FinalAnswer string       `json:"final_answer,omitempty"`
	Error       string       `json:"error,omitempty"`
}
