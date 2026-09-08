package cmd

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/dev-toolings/ghostchrome/engine"
	"github.com/dev-toolings/ghostchrome/internal/core/feedback"
	"github.com/dev-toolings/ghostchrome/internal/runtime"
	"github.com/spf13/cobra"
)

// agentRequest is one line on stdin.
type agentRequest struct {
	ID   string          `json:"id"`
	Op   string          `json:"op"`
	Args json.RawMessage `json:"args,omitempty"`
}

// agentResponse is one line on stdout.
type agentResponse struct {
	ID          string                 `json:"id"`
	OK          bool                   `json:"ok"`
	Result      interface{}            `json:"result,omitempty"`
	Error       string                 `json:"error,omitempty"`
	Events      []engine.ObserverEvent `json:"events,omitempty"`
	Observation *feedback.Observation  `json:"observation,omitempty"`
	Protocol    int                    `json:"protocol,omitempty"`
	ErrorCode   string                 `json:"error_code,omitempty"`
	Retryable   bool                   `json:"retryable,omitempty"`
}

var agentCmd = &cobra.Command{
	Use:   "agent",
	Short: "Run an interactive JSONL loop on stdin/stdout for LLM agents",
	Long: `Reads one JSON request per line on stdin and writes one JSON response per
line on stdout. The browser stays alive across requests so refs from a prior
extract are valid for the next click/type.

Request:  {"id":"r1","op":"navigate","args":{"url":"https://example.com"}}
Response: {"id":"r1","ok":true,"result":{"status":200,"title":"...","url":"..."}}

Supported ops:
  init                                       (open browser, no-op if already open)
  navigate    {url, wait?}                   (wait: load|stable|networkidle)
  back / forward
  extract     {level?, selector?}            level: skeleton|content|full
  click       {ref}
  type        {ref, text}
  press       {key, ref?}
  hover       {ref}
  select      {ref, values[]}
  fill        {fields: {ref: value}}
  scroll_by   {dy}
  scroll_to   {y?, bottom?}
  eval        {expr, ref?}
  screenshot  {full_page?, ref?, quality?}   returns base64 PNG/JPEG
  wait        {selector?, ref?, ms?}
  tabs        {action?, index?, url?}        list|switch|close|new
  dialog      {action?, text?}               accept|dismiss (auto-handles JS dialogs)
  errors                                     console + network errors
  url
  close

JSONL embeds Chrome by default. Use -s <name> to share a named daemon.
Stale refs fail closed; re-extract then retry. Semantic retry remaps SPA rerenders.

With --stealth: after "navigate" detects+clears a DataDome/Cloudflare
challenge, the very next "extract" auto-includes SSR payloads
(__NEXT_DATA__ / RSC self.__next_f) until the next "navigate".`,
	Args: cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		runAgentLoop()
	},
}

func init() {
	rootCmd.AddCommand(agentCmd)
}

// agentRuntimeConfig snapshots the cobra globals the op layer needs. The CLI
// keeps owning the flags; internal/runtime never reads them.
func agentRuntimeConfig() runtime.Config {
	return runtime.Config{
		// Resolved lazily: BrowserOpts may assign the implicit daemon name
		// on the first launch, so the lease must be touched with whatever
		// flagSession holds at that point, not at construction time.
		SessionName:       func() string { return flagSession },
		BrowserOpts:       buildBrowserOpts,
		ResolveOutputPath: validateOutputPath,
		Stealth:           flagStealth,
		Observe:           flagObserve,
		ObserveOut:        flagObserveOut,
		TimeoutSec:        flagTimeout,
		Version:           rootCmd.Version,
	}
}

// newAgentSession constructs a session ready to be driven in-process (e.g.
// from cmd/ai.go). It does NOT bind stdout — JSON encoding is the caller's
// responsibility. Page is opened lazily on the first EnsurePage call.
func newAgentSession() *runtime.Session {
	return runtime.New(agentRuntimeConfig())
}

// agentWriter serialises one JSONL response per line, applying the
// --output-secrets redaction on the way out.
type agentWriter struct{ enc *json.Encoder }

func runAgentLoop() {
	if flagSession == "" {
		skipImplicitDaemon = true
	}
	sess := newAgentSession()
	defer sess.Shutdown()
	out := agentWriter{enc: json.NewEncoder(os.Stdout)}

	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 1<<16), 8<<20) // up to 8 MiB lines for big eval/fill payloads
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var req agentRequest
		if err := json.Unmarshal(line, &req); err != nil {
			out.write(agentResponse{ID: "", OK: false, Error: "parse: " + err.Error()})
			continue
		}

		result, obs, obsEvents, err := sess.RunOp(req.Op, req.Args)
		if flagSession != "" {
			engine.TouchSessionLease(flagSession)
		}
		// Mirror observer events to the --observe-out sidecar when configured.
		sess.MirrorEvents(obsEvents)

		if err != nil {
			code, retry := engine.ClassifyError(err)
			resp := agentResponse{ID: req.ID, OK: false, Error: err.Error(), ErrorCode: code, Retryable: retry, Observation: obs}
			if len(obsEvents) > 0 {
				resp.Events = obsEvents
			}
			out.write(resp)
			continue
		}
		resp := agentResponse{ID: req.ID, OK: true, Result: result, Observation: obs}
		if len(obsEvents) > 0 {
			resp.Events = obsEvents
		}
		out.write(resp)
		if req.Op == "close" {
			return
		}
	}
	if err := scanner.Err(); err != nil && !errors.Is(err, io.EOF) {
		fmt.Fprintf(os.Stderr, "agent: scanner: %v\n", err)
	}
}

func (w agentWriter) write(resp agentResponse) {
	resp.Protocol = engine.ProtocolVersion
	// Redact payloads structurally: replacing bytes in JSON could corrupt a
	// number or a key, and correlation IDs must remain untouched.
	if len(flagOutputSecrets) > 0 {
		payload, err := json.Marshal(resp)
		if err != nil {
			fmt.Fprintf(os.Stderr, "agent: encode response: %v\n", err)
			return
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(payload, &fields); err != nil {
			return
		}
		for _, key := range []string{"result", "error", "events", "observation"} {
			if value, ok := fields[key]; ok {
				redacted, err := redactJSONOutput(value, flagOutputSecrets)
				if err != nil {
					fmt.Fprintf(os.Stderr, "agent: redact response: %v\n", err)
					return
				}
				fields[key] = redacted
			}
		}
		if err := w.enc.Encode(fields); err != nil {
			fmt.Fprintf(os.Stderr, "agent: write response: %v\n", err)
		}
		return
	}
	if err := w.enc.Encode(resp); err != nil {
		fmt.Fprintf(os.Stderr, "agent: write response %s: %v\n", resp.ID, err)
	}
}
