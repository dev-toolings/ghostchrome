package cli

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/dev-toolings/ghostchrome/engine"
	"github.com/go-rod/rod"
	"github.com/spf13/cobra"
)

var flagBatchStopOnError bool

// BatchStep captures one line of execution in the script.
type BatchStep struct {
	Index  int    `json:"step"`
	Cmd    string `json:"cmd"`
	Args   string `json:"args,omitempty"`
	OK     bool   `json:"ok"`
	TimeMs int64  `json:"ms"`
	Error  string `json:"error,omitempty"`
}

// BatchResult is the final JSON returned by `ghostchrome batch`.
type BatchResult struct {
	Steps   []BatchStep  `json:"steps"`
	Final   any          `json:"final,omitempty"`
	FinalOp string       `json:"final_op,omitempty"`
	Summary BatchSummary `json:"summary"`
}

// BatchSummary gives the agent a glanceable overview.
type BatchSummary struct {
	Total  int   `json:"total"`
	Ok     int   `json:"ok"`
	Failed int   `json:"failed"`
	TimeMs int64 `json:"total_ms"`
}

var batchCmd = &cobra.Command{
	Use:   "batch [script]",
	Short: "Run a sequence of commands in-process and return a single JSON result",
	Long: `Batch executes a script of ghostchrome commands in a single process, keeping
the same browser / page context between steps. Only the FINAL command's payload
is returned in full; intermediate steps are summarised as {ok, ms}. This saves
tokens on multi-step agent flows.

Script syntax:
  # comments allowed
  navigate <url> [wait=load|stable|idle|none]
  click @ref
  type @ref "<text>"
  press <key> [@ref]
  hover @ref
  select @ref "val1" "val2"
  wait-selector "<css>" [timeout=30]
  wait-ms <n>
  extract [level=skeleton|content|full] [diff=on|off]
  preview <url> [level=skeleton|content|full]

Examples:
  ghostchrome batch script.gcb --connect ws://...
  echo -e "navigate https://example.com\nextract level=skeleton" | ghostchrome batch -`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		source := args[0]

		lines, err := readBatchScript(source)
		if err != nil {
			exitErr("batch", err)
		}

		b, page := openPage()
		defer b.Close()

		runner := &batchRunner{
			browser: b,
			page:    page,
			profile: renderProfile(),
		}
		result := runner.run(lines)

		text := formatBatchText(result)
		output(result, text)
	},
}

func readBatchScript(source string) ([]string, error) {
	var r *bufio.Scanner
	switch source {
	case "-":
		r = bufio.NewScanner(os.Stdin)
	default:
		f, err := os.Open(source)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		r = bufio.NewScanner(f)
	}
	r.Buffer(make([]byte, 1<<20), 1<<20)

	var lines []string
	for r.Scan() {
		lines = append(lines, r.Text())
	}
	return lines, r.Err()
}

type batchRunner struct {
	browser *engine.Browser
	page    *rod.Page
	profile engine.RenderProfile
	// lastSnapshot holds the most recent extracted snapshot, used to compute
	// diffs across batch steps even when not connected to a persistent Chrome.
	lastSnapshot *engine.PageSnapshot
}

func (r *batchRunner) run(lines []string) *BatchResult {
	result := &BatchResult{}
	start := time.Now()

	for i, raw := range lines {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		verb, rest := splitFirstWord(trimmed)
		step := BatchStep{Index: len(result.Steps) + 1, Cmd: verb, Args: rest}
		stepStart := time.Now()

		payload, err := r.dispatch(verb, rest)
		step.TimeMs = time.Since(stepStart).Milliseconds()
		if err != nil {
			step.Error = err.Error()
			result.Steps = append(result.Steps, step)
			result.Summary.Failed++
			if flagBatchStopOnError {
				break
			}
			continue
		}
		step.OK = true
		result.Steps = append(result.Steps, step)
		result.Summary.Ok++

		// Capture the *last* step's payload as the final result.
		if payload != nil {
			result.Final = payload
			result.FinalOp = verb
		}

		_ = i
	}

	result.Summary.Total = len(result.Steps)
	result.Summary.TimeMs = time.Since(start).Milliseconds()
	return result
}

func (r *batchRunner) dispatch(verb, rest string) (any, error) {
	switch verb {
	case "navigate":
		return r.cmdNavigate(rest)
	case "click":
		return nil, r.cmdClick(rest)
	case "type":
		return nil, r.cmdType(rest)
	case "press":
		return nil, r.cmdPress(rest)
	case "hover":
		return nil, r.cmdHover(rest)
	case "select":
		return nil, r.cmdSelect(rest)
	case "wait-selector":
		return nil, r.cmdWaitSelector(rest)
	case "wait-ms":
		return nil, r.cmdWaitMs(rest)
	case "extract":
		return r.cmdExtract(rest)
	case "preview":
		return r.cmdPreview(rest)
	case "emulate":
		return nil, r.cmdEmulate(rest)
	case "scroll-to":
		return nil, r.cmdScrollTo(rest)
	case "scroll":
		return nil, r.cmdScroll(rest)
	default:
		return nil, fmt.Errorf("unknown verb %q", verb)
	}
}

// ---- individual verbs -----------------------------------------------------

func (r *batchRunner) cmdNavigate(args string) (any, error) {
	positional, kv := parseArgs(args)
	if len(positional) == 0 {
		return nil, errors.New("navigate: missing URL")
	}
	wait := kv["wait"]
	if wait == "" {
		wait = "load"
	}
	info, err := engine.Navigate(r.page, positional[0], wait)
	if err != nil {
		return nil, err
	}
	return info, nil
}

func (r *batchRunner) cmdClick(args string) error {
	ref := strings.TrimSpace(args)
	if ref == "" {
		return errors.New("click: missing @ref")
	}
	snap := r.browser.Snapshot(r.page)
	return engine.ClickRef(r.page, ref, snap)
}

func (r *batchRunner) cmdType(args string) error {
	ref, text, ok := cutQuoted(args)
	if !ok {
		return errors.New(`type: expected @ref "text"`)
	}
	snap := r.browser.Snapshot(r.page)
	return engine.TypeRef(r.page, ref, text, snap)
}

func (r *batchRunner) cmdPress(args string) error {
	positional, _ := parseArgs(args)
	if len(positional) == 0 {
		return errors.New("press: missing key")
	}
	ref := ""
	if len(positional) > 1 {
		ref = positional[1]
	}
	snap := r.browser.Snapshot(r.page)
	return engine.PressKey(r.page, positional[0], ref, snap)
}

func (r *batchRunner) cmdHover(args string) error {
	ref := strings.TrimSpace(args)
	if ref == "" {
		return errors.New("hover: missing @ref")
	}
	snap := r.browser.Snapshot(r.page)
	return engine.HoverRef(r.page, ref, snap)
}

func (r *batchRunner) cmdSelect(args string) error {
	ref, rest, ok := cutFirstWord(args)
	if !ok {
		return errors.New(`select: expected @ref "val1" "val2"`)
	}
	values := parseQuotedList(rest)
	if len(values) == 0 {
		return errors.New("select: need at least one value")
	}
	snap := r.browser.Snapshot(r.page)
	return engine.SelectOption(r.page, ref, values, snap)
}

func (r *batchRunner) cmdWaitSelector(args string) error {
	sel, _, ok := cutQuotedFirst(args)
	if !ok {
		return errors.New(`wait-selector: expected "css"`)
	}
	_, kv := parseArgs(args)
	timeoutSec := 30
	if v, ok := kv["timeout"]; ok {
		if n, err := strconv.Atoi(v); err == nil {
			timeoutSec = n
		}
	}
	return engine.WaitForSelector(r.page, sel, timeoutSec)
}

func (r *batchRunner) cmdEmulate(args string) error {
	_, kv := parseArgs(args)
	if name := kv["device"]; name != "" {
		d, ok := engine.DeviceByName(name)
		if !ok {
			return fmt.Errorf("emulate: unknown device %q", name)
		}
		if err := engine.ApplyDevice(r.page, d); err != nil {
			return err
		}
	}
	if ua := kv["user-agent"]; ua != "" {
		if err := engine.ApplyUserAgent(r.page, ua); err != nil {
			return err
		}
	}
	if cs := kv["color-scheme"]; cs != "" {
		if err := engine.ApplyColorScheme(r.page, cs); err != nil {
			return err
		}
	}
	if tz := kv["timezone"]; tz != "" {
		if err := engine.ApplyTimezone(r.page, tz); err != nil {
			return err
		}
	}
	return nil
}

func (r *batchRunner) cmdScroll(args string) error {
	ref := strings.TrimSpace(args)
	if ref == "" {
		return errors.New("scroll: missing @ref")
	}
	snap := r.browser.Snapshot(r.page)
	return engine.ScrollToRef(r.page, ref, snap)
}

func (r *batchRunner) cmdScrollTo(args string) error {
	target := strings.TrimSpace(args)
	if target == "" {
		return errors.New("scroll-to: missing target (top|bottom|<y>)")
	}
	y, err := scrollTargetToY(target)
	if err != nil {
		return err
	}
	_, err = engine.ScrollToY(r.page, y, target == "bottom")
	return err
}

func (r *batchRunner) cmdWaitMs(args string) error {
	n, err := strconv.Atoi(strings.TrimSpace(args))
	if err != nil {
		return fmt.Errorf("wait-ms: %w", err)
	}
	time.Sleep(time.Duration(n) * time.Millisecond)
	return nil
}

func (r *batchRunner) cmdExtract(args string) (any, error) {
	_, kv := parseArgs(args)
	level := engine.ExtractLevel(kv["level"])
	if level == "" {
		level = engine.LevelSkeleton
	}
	if err := engine.ValidateExtractLevel(level); err != nil {
		return nil, err
	}

	prev := r.lastSnapshot
	if prev == nil {
		prev = r.browser.Snapshot(r.page)
	}

	result, err := engine.Extract(r.page, level, "", false)
	if err != nil {
		return nil, err
	}
	fresh, err := engine.BuildSnapshot(r.page, result)
	if err != nil {
		return nil, err
	}
	if err := r.browser.SaveSnapshot(r.page, result); err != nil {
		return nil, err
	}
	r.lastSnapshot = fresh

	diffMode := kv["diff"]
	if diffMode == "" {
		diffMode = "auto"
	}
	if shouldDiff(diffMode, r.profile, prev, fresh) {
		d := engine.DiffRefs(prev.Refs, fresh.Refs)
		return d, nil
	}
	return result, nil
}

func (r *batchRunner) cmdPreview(args string) (any, error) {
	positional, kv := parseArgs(args)
	if len(positional) == 0 {
		return nil, errors.New("preview: missing URL")
	}
	level := engine.ExtractLevel(kv["level"])
	if level == "" {
		level = engine.LevelSkeleton
	}
	result, err := engine.Preview(r.page, positional[0], "stable", level, nil, flagStealth)
	if err != nil {
		return nil, err
	}
	if err := r.browser.SaveSnapshot(r.page, result.DOM); err != nil {
		return nil, err
	}
	return result, nil
}

// ---- tiny parsing helpers -------------------------------------------------

func splitFirstWord(s string) (first, rest string) {
	idx := strings.IndexAny(s, " \t")
	if idx < 0 {
		return s, ""
	}
	return s[:idx], strings.TrimSpace(s[idx+1:])
}

// parseArgs splits "foo bar key=value baz=qux" into positional ["foo","bar"]
// and map {"key":"value","baz":"qux"}.
func parseArgs(s string) ([]string, map[string]string) {
	kv := map[string]string{}
	var positional []string
	for _, tok := range tokenize(s) {
		if eq := strings.IndexByte(tok, '='); eq > 0 {
			kv[tok[:eq]] = strings.Trim(tok[eq+1:], `"`)
			continue
		}
		positional = append(positional, tok)
	}
	return positional, kv
}

// tokenize performs a very small shell-like split that respects double quotes.
func tokenize(s string) []string {
	var out []string
	var cur strings.Builder
	inQuote := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '"':
			inQuote = !inQuote
			cur.WriteByte(c)
		case (c == ' ' || c == '\t') && !inQuote:
			if cur.Len() > 0 {
				out = append(out, cur.String())
				cur.Reset()
			}
		default:
			cur.WriteByte(c)
		}
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}

// cutQuoted expects `<word> "<quoted>"` and returns (word, quoted, ok).
func cutQuoted(s string) (string, string, bool) {
	word, rest := splitFirstWord(s)
	if word == "" {
		return "", "", false
	}
	quoted, _, ok := cutQuotedFirst(rest)
	return word, quoted, ok
}

// cutQuotedFirst extracts the first `"..."` and returns (content, rest, ok).
func cutQuotedFirst(s string) (string, string, bool) {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, `"`) {
		return "", "", false
	}
	end := strings.IndexByte(s[1:], '"')
	if end < 0 {
		return "", "", false
	}
	return s[1 : 1+end], strings.TrimSpace(s[2+end:]), true
}

// cutFirstWord = first whitespace-separated token, remainder.
func cutFirstWord(s string) (string, string, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", "", false
	}
	first, rest := splitFirstWord(s)
	return first, rest, true
}

// parseQuotedList returns every `"..."` token in order.
func parseQuotedList(s string) []string {
	var out []string
	cursor := s
	for {
		content, rest, ok := cutQuotedFirst(cursor)
		if !ok {
			break
		}
		out = append(out, content)
		cursor = rest
	}
	return out
}

func formatBatchText(r *BatchResult) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "[batch] %d steps, %d ok, %d failed (%dms)\n",
		r.Summary.Total, r.Summary.Ok, r.Summary.Failed, r.Summary.TimeMs)
	for _, s := range r.Steps {
		marker := "✓"
		if !s.OK {
			marker = "✗"
		}
		fmt.Fprintf(&sb, "  %s #%d %s %s (%dms)", marker, s.Index, s.Cmd, truncateBatchArg(s.Args), s.TimeMs)
		if s.Error != "" {
			fmt.Fprintf(&sb, " — %s", s.Error)
		}
		sb.WriteString("\n")
	}
	return strings.TrimRight(sb.String(), "\n")
}

func truncateBatchArg(s string) string {
	if len(s) > 60 {
		return s[:57] + "..."
	}
	return s
}

func init() {
	batchCmd.Flags().BoolVar(&flagBatchStopOnError, "stop-on-error", true, "Abort after the first failing step")
	rootCmd.AddCommand(batchCmd)
}
