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

// AnthropicProvider talks directly to the Messages API. Stays HTTP-native to
// avoid a heavyweight SDK dependency and the version drift that comes with it.
type AnthropicProvider struct {
	APIKey      string
	Model       string
	MaxTokens   int
	Temperature float64
	Endpoint    string // override for self-hosted gateways
	Timeout     time.Duration
}

func (p *AnthropicProvider) Name() string { return "anthropic" }

func (p *AnthropicProvider) Step(ctx context.Context, history []Message, tools []ToolSpec) (Step, error) {
	model := p.Model
	if model == "" {
		model = "claude-haiku-4-5-20251001"
	}
	maxTokens := p.MaxTokens
	if maxTokens == 0 {
		maxTokens = 1024
	}
	endpoint := p.Endpoint
	if endpoint == "" {
		endpoint = "https://api.anthropic.com/v1/messages"
	}

	system, msgs := splitSystem(history)
	body := map[string]any{
		"model":      model,
		"max_tokens": maxTokens,
		"messages":   msgs,
		"tools":      anthropicTools(tools),
	}
	if system != "" {
		// Cache the static system prompt (1 cache breakpoint). Anthropic
		// returns cache_creation/read tokens in usage so the savings show up
		// transparently across steps within the 5-minute TTL.
		body["system"] = []map[string]any{{
			"type":          "text",
			"text":          system,
			"cache_control": map[string]any{"type": "ephemeral"},
		}}
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
	req.Header.Set("x-api-key", p.APIKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	client, err := engine.BuildHTTPClient(engine.HTTPClientOpts{Timeout: p.timeout()})
	if err != nil {
		return Step{}, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return Step{}, fmt.Errorf("anthropic: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		buf, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return Step{}, fmt.Errorf("anthropic: %d %s: %s", resp.StatusCode, resp.Status, strings.TrimSpace(string(buf)))
	}

	var parsed struct {
		StopReason string `json:"stop_reason"`
		Content    []struct {
			Type  string          `json:"type"`
			Text  string          `json:"text,omitempty"`
			ID    string          `json:"id,omitempty"`
			Name  string          `json:"name,omitempty"`
			Input json.RawMessage `json:"input,omitempty"`
		} `json:"content"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return Step{}, err
	}

	step := Step{StopReason: parsed.StopReason}
	for _, block := range parsed.Content {
		switch block.Type {
		case "text":
			if step.Text != "" {
				step.Text += "\n"
			}
			step.Text += block.Text
		case "tool_use":
			step.ToolCalls = append(step.ToolCalls, ToolCallReq{
				ID:    block.ID,
				Name:  block.Name,
				Input: block.Input,
			})
		}
	}
	return step, nil
}

func (p *AnthropicProvider) timeout() time.Duration {
	if p.Timeout > 0 {
		return p.Timeout
	}
	return 60 * time.Second
}

func anthropicTools(specs []ToolSpec) []map[string]any {
	out := make([]map[string]any, 0, len(specs))
	for i, t := range specs {
		entry := map[string]any{
			"name":         t.Name,
			"description":  t.Description,
			"input_schema": t.InputSchema,
		}
		// Cache the tools block (2nd cache breakpoint). Anthropic allows up
		// to 4; marking the LAST tool propagates the breakpoint to the whole
		// tools array. The static tool catalog never changes between steps.
		if i == len(specs)-1 {
			entry["cache_control"] = map[string]any{"type": "ephemeral"}
		}
		out = append(out, entry)
	}
	return out
}

// splitSystem extracts the leading system message and converts the rest to
// the Anthropic schema:
//
//	[{role:"user"|"assistant", content: [...blocks]}]
//
// Tool results become user messages with content=[{type:"tool_result", ...}].
// Assistant tool calls are reconstructed by inspecting Calls + Content.
func splitSystem(history []Message) (system string, msgs []map[string]any) {
	for _, m := range history {
		if m.Role == "system" {
			if system != "" {
				system += "\n"
			}
			system += m.Content
			continue
		}
		switch m.Role {
		case "user":
			msgs = append(msgs, map[string]any{"role": "user", "content": []map[string]any{{"type": "text", "text": m.Content}}})
		case "assistant":
			blocks := []map[string]any{}
			if m.Content != "" {
				blocks = append(blocks, map[string]any{"type": "text", "text": m.Content})
			}
			for _, c := range m.Calls {
				var input any
				_ = json.Unmarshal(c.Input, &input)
				if input == nil {
					input = map[string]any{}
				}
				blocks = append(blocks, map[string]any{
					"type":  "tool_use",
					"id":    c.ID,
					"name":  c.Name,
					"input": input,
				})
			}
			msgs = append(msgs, map[string]any{"role": "assistant", "content": blocks})
		case "tool":
			if m.Tool == nil {
				continue
			}
			msgs = append(msgs, map[string]any{
				"role": "user",
				"content": []map[string]any{{
					"type":        "tool_result",
					"tool_use_id": m.Tool.CallID,
					"content":     m.Tool.Result,
					"is_error":    m.Tool.IsErr,
				}},
			})
		}
	}
	return
}
