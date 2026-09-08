package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/dev-toolings/ghostchrome/engine/ai"
	"github.com/dev-toolings/ghostchrome/internal/core/feedback"
	"github.com/dev-toolings/ghostchrome/internal/runtime"
	"github.com/spf13/cobra"
)

var (
	aiURL         string
	aiProvider    string
	aiModel       string
	aiMaxSteps    int
	aiAPIKey      string
	aiTemperature float64
	aiOutput      string
	aiTrace       bool
)

var aiCmd = &cobra.Command{
	Use:   "ai <goal>",
	Short: "Autonomous LLM-driven browser agent (Claude / OpenAI)",
	Long: `ai launches an autonomous loop where an LLM picks the next action
(navigate, extract, click, type, …) until the GOAL is met.

Providers:
  --provider claude   (default; uses ANTHROPIC_API_KEY, default model claude-haiku-4-5-20251001)
  --provider openai   (uses OPENAI_API_KEY, default model gpt-4o-mini)

API keys are read from env (.env loaded automatically), or via --api-key.
Use --trace to stream a one-line summary per step on stderr.

Examples:
  ghostchrome ai "find the price of the Tesla Model 3" \
    --url https://www.tesla.com/fr_FR/model3 --max-steps 8

  ghostchrome --stealth ai "log in with user@x and click 'Sign in'" \
    --url https://app.example.com/login --provider openai --model gpt-4o`,
	Args: cobra.MinimumNArgs(1),
	Run:  runAI,
}

func init() {
	aiCmd.Flags().StringVar(&aiURL, "url", "", "Initial URL to navigate to before the loop starts")
	aiCmd.Flags().StringVar(&aiProvider, "provider", "claude", "LLM provider: claude | openai")
	aiCmd.Flags().StringVar(&aiModel, "model", "", "Model id (default: claude-haiku-4-5-20251001 or gpt-4o-mini)")
	aiCmd.Flags().IntVar(&aiMaxSteps, "max-steps", 15, "Hard cap on observe→act iterations")
	aiCmd.Flags().StringVar(&aiAPIKey, "api-key", "", "API key (overrides ANTHROPIC_API_KEY / OPENAI_API_KEY)")
	aiCmd.Flags().Float64Var(&aiTemperature, "temperature", 0, "Sampling temperature (0 = provider default)")
	aiCmd.Flags().StringVarP(&aiOutput, "output", "o", "", "Write the JSON result to this file (default: stdout)")
	aiCmd.Flags().BoolVar(&aiTrace, "trace", false, "Emit one-line step trace on stderr")
	rootCmd.AddCommand(aiCmd)
}

func runAI(_ *cobra.Command, args []string) {
	goal := strings.TrimSpace(strings.Join(args, " "))
	if goal == "" {
		exitErr("ai", fmt.Errorf("goal is empty"))
	}

	provider, err := buildAIProvider()
	if err != nil {
		exitErr("ai", err)
	}

	sess := newAgentSession()
	defer sess.Shutdown()

	// Open the browser (reuses --connect / --user-profile / --stealth).
	if _, _, err := sess.EnsurePage(); err != nil {
		exitErr("ai", err)
	}

	// Optional initial navigate. Doing it here (vs. asking the model to do it)
	// shaves a step and keeps the first observation grounded.
	if aiURL != "" {
		raw, _ := json.Marshal(map[string]string{"url": aiURL, "wait": "load"})
		if _, _, _, err := sess.RunOp("navigate", raw); err != nil {
			exitErr("ai navigate", err)
		}
	}

	runner := &agentRunner{sess: sess}
	ctx, cancel := context.WithTimeout(context.Background(), aiTimeout())
	defer cancel()

	result, err := ai.Run(ctx, runner, provider, ai.LoopOpts{
		Goal:     goal,
		MaxSteps: aiMaxSteps,
		Verbose:  aiTrace,
	})
	if result == nil {
		result = &ai.Result{Goal: goal, Provider: provider.Name()}
	}
	if result.Model == "" {
		result.Model = aiModel
	}
	if err != nil && result.Error == "" {
		result.Error = err.Error()
	}

	emitAIResult(result)
}

// agentRunner is the bridge between engine/ai and the shared internal/runtime
// session: the `ai` loop drives exactly the same ops as the JSONL loop.
type agentRunner struct{ sess *runtime.Session }

func (r *agentRunner) RunOp(op string, args json.RawMessage) (any, *feedback.Observation, error) {
	result, obs, _, err := r.sess.RunOp(op, args)
	return result, obs, err
}

func (r *agentRunner) CurrentURL() string {
	page := r.sess.Page()
	if page == nil {
		return ""
	}
	info, _ := page.Info()
	if info == nil {
		return ""
	}
	return info.URL
}

func buildAIProvider() (ai.Provider, error) {
	switch strings.ToLower(aiProvider) {
	case "", "claude", "anthropic":
		key := aiAPIKey
		if key == "" {
			key = os.Getenv("ANTHROPIC_API_KEY")
		}
		if key == "" {
			return nil, fmt.Errorf("anthropic provider requires --api-key or ANTHROPIC_API_KEY")
		}
		return &ai.AnthropicProvider{
			APIKey:      key,
			Model:       aiModel,
			Temperature: aiTemperature,
		}, nil
	case "openai":
		key := aiAPIKey
		if key == "" {
			key = os.Getenv("OPENAI_API_KEY")
		}
		if key == "" {
			return nil, fmt.Errorf("openai provider requires --api-key or OPENAI_API_KEY")
		}
		return &ai.OpenAIProvider{
			APIKey:      key,
			Model:       aiModel,
			Temperature: aiTemperature,
		}, nil
	default:
		return nil, fmt.Errorf("unknown provider %q", aiProvider)
	}
}

func aiTimeout() time.Duration {
	// Generous overall cap: each step has its own LLM + browser timeouts.
	if flagTimeout > 0 {
		return time.Duration(flagTimeout) * time.Second * time.Duration(maxOr(aiMaxSteps, 1))
	}
	return 10 * time.Minute
}

func maxOr(v, fallback int) int {
	if v > 0 {
		return v
	}
	return fallback
}

func emitAIResult(res *ai.Result) {
	w := os.Stdout
	if aiOutput != "" {
		safe, err := validateOutputPath(aiOutput)
		if err != nil {
			exitErr("ai output", err)
		}
		f, err := os.Create(safe)
		if err != nil {
			exitErr("ai output", err)
		}
		defer f.Close()
		w = f
	}
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(res); err != nil {
		exitErr("ai encode", err)
	}
	if !res.Success {
		os.Exit(1)
	}
}
