package ai

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/dev-toolings/ghostchrome/internal/core/feedback"
)

// FakeProvider replays a scripted sequence of Steps without ever calling
// a real LLM. Used by loop_test.go and as a way for downstream callers to
// integration-test pipelines without burning tokens.
//
// One Step is consumed per Provider.Step call. When the queue is empty,
// Step returns ErrFakeExhausted.
type FakeProvider struct {
	Steps []Step
	idx   int
	// CalledWith is appended on every Step invocation; useful for asserts.
	CalledWith []FakeCall
}

// FakeCall is a record of one Provider.Step invocation.
type FakeCall struct {
	History []Message
	Tools   []ToolSpec
}

// ErrFakeExhausted is returned when the script is consumed.
var ErrFakeExhausted = errors.New("fake provider: scripted steps exhausted")

func (p *FakeProvider) Name() string { return "fake" }

func (p *FakeProvider) Step(_ context.Context, history []Message, tools []ToolSpec) (Step, error) {
	p.CalledWith = append(p.CalledWith, FakeCall{History: history, Tools: tools})
	if p.idx >= len(p.Steps) {
		return Step{}, ErrFakeExhausted
	}
	s := p.Steps[p.idx]
	p.idx++
	return s, nil
}

// FakeRunner is a deterministic Runner that returns a canned result for
// each op. Use to drive Run end-to-end without a real browser.
type FakeRunner struct {
	URL     string
	Results map[string]any                        // op → result
	OnOp    func(op string, args json.RawMessage) // optional spy
}

func (r *FakeRunner) RunOp(op string, args json.RawMessage) (any, *feedback.Observation, error) {
	if r.OnOp != nil {
		r.OnOp(op, args)
	}
	if r.Results == nil {
		return nil, nil, nil
	}
	v, ok := r.Results[op]
	if !ok {
		return nil, nil, nil
	}
	return v, nil, nil
}

func (r *FakeRunner) CurrentURL() string { return r.URL }
