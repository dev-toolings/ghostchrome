package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/dev-toolings/ghostchrome/engine"
)

// OpenAIProvider hits the Chat Completions endpoint with function-style tool
// calls. Same HTTP-native rationale as AnthropicProvider.
type OpenAIProvider struct {
	APIKey      string
	Model       string
	Temperature float64
	Endpoint    string
	Timeout     time.Duration
}

func (p *OpenAIProvider) Name() string { return "openai" }

func (p *OpenAIProvider) Step(ctx context.Context, history []Message, tools []ToolSpec) (Step, error) {
	model := p.Model
	if model == "" {
		model = "gpt-4o-mini"
	}
	endpoint := p.Endpoint
	if endpoint == "" {
		endpoint = "https://api.openai.com/v1/chat/completions"
	}

	body := map[string]any{
		"model":    model,
		"messages": openaiMessages(history),
		"tools":    openaiTools(tools),
	}
	if p.Temperature > 0 {
		body["temperature"] = p.Temperature
	}

	raw, err := json.Marshal(body)
	if err != nil {
		return Step{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(raw))
	if err != nil {
		return Step{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.APIKey)

	client, err := engine.BuildHTTPClient(engine.HTTPClientOpts{Timeout: p.timeout()})
	if err != nil {
		return Step{}, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return Step{}, fmt.Errorf("openai: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		buf, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return Step{}, fmt.Errorf("openai: %d %s: %s", resp.StatusCode, resp.Status, strings.TrimSpace(string(buf)))
	}

	var parsed struct {
		Choices []struct {
			FinishReason string `json:"finish_reason"`
			Message      struct {
				Content   string `json:"content"`
				ToolCalls []struct {
					ID       string `json:"id"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return Step{}, err
	}
	if len(parsed.Choices) == 0 {
		return Step{}, fmt.Errorf("openai: empty choices")
	}
	c := parsed.Choices[0]
	step := Step{StopReason: c.FinishReason, Text: c.Message.Content}
	for _, tc := range c.Message.ToolCalls {
		step.ToolCalls = append(step.ToolCalls, ToolCallReq{
			ID:    tc.ID,
			Name:  tc.Function.Name,
			Input: json.RawMessage(tc.Function.Arguments),
		})
	}
	return step, nil
}

func (p *OpenAIProvider) timeout() time.Duration {
	if p.Timeout > 0 {
		return p.Timeout
	}
	return 60 * time.Second
}

func openaiTools(specs []ToolSpec) []map[string]any {
	out := make([]map[string]any, 0, len(specs))
	for _, t := range specs {
		out = append(out, map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        t.Name,
				"description": t.Description,
				"parameters":  t.InputSchema,
			},
		})
	}
	return out
}

func openaiMessages(history []Message) []map[string]any {
	out := []map[string]any{}
	for _, m := range history {
		switch m.Role {
		case "system", "user":
			out = append(out, map[string]any{"role": m.Role, "content": m.Content})
		case "assistant":
			msg := map[string]any{"role": "assistant", "content": m.Content}
			if len(m.Calls) > 0 {
				calls := make([]map[string]any, 0, len(m.Calls))
				for _, c := range m.Calls {
					args := string(c.Input)
					if args == "" {
						args = "{}"
					}
					calls = append(calls, map[string]any{
						"id":   c.ID,
						"type": "function",
						"function": map[string]any{
							"name":      c.Name,
							"arguments": args,
						},
					})
				}
				msg["tool_calls"] = calls
			}
			out = append(out, msg)
		case "tool":
			if m.Tool == nil {
				continue
			}
			out = append(out, map[string]any{
				"role":         "tool",
				"tool_call_id": m.Tool.CallID,
				"content":      m.Tool.Result,
			})
		}
	}
	return out
}
