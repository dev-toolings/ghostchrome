package cli

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/dev-toolings/ghostchrome/internal/core/engine"
	"github.com/dev-toolings/ghostchrome/internal/core/overlay"
	"github.com/spf13/cobra"
)

var (
	flagConsoleLevel          string
	flagConsoleWait           int
	flagConsoleClear          bool
	flagNetworkWait           int
	flagNetworkMax            int
	flagNetworkFilter         string
	flagNetworkStatic         bool
	flagNetworkRequestBody    bool
	flagNetworkRequestHeaders bool
	flagNetworkClear          bool
	flagRequestFilename       string
)

var consoleCompatCmd = &cobra.Command{
	Use:   "console [level|url] [url]",
	Short: "Observe console messages",
	Long: `Observe console messages during an optional navigation or wait window.
With a persistent session or --connect target, captured messages are appended to
the session console buffer until --clear is used. Ephemeral launches only report
messages captured while the command is running.`,
	Args: cobra.MaximumNArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		level, targetURL, err := resolveConsoleArgs(args, flagConsoleLevel)
		if err != nil {
			exitErr("console", err)
		}
		b, page := openPage()
		defer b.Close()
		if flagConsoleClear {
			if err := b.ClearConsoleLog(); err != nil {
				exitErr("console --clear", err)
			}
			type consoleResult struct {
				Entries []engine.ObserverEvent `json:"entries"`
				Count   int                    `json:"count"`
				Cleared bool                   `json:"cleared"`
			}
			output(consoleResult{Entries: []engine.ObserverEvent{}, Count: 0, Cleared: true}, "console buffer cleared")
			return
		}

		if flagStealth {
			fmt.Fprintln(os.Stderr, "ghostchrome: console enables the Runtime CDP domain, weakening --stealth")
		}
		observer := engine.NewObserver(page, engine.ObserverOpts{
			Filters: engine.ObserverFilters{
				Kinds: []engine.ObserverKind{engine.KindConsole, engine.KindError},
			},
		})
		if err := observer.Start(context.Background()); err != nil {
			exitErr("console", err)
		}
		if targetURL != "" {
			navigateIfRequested(page, targetURL, "load")
		}
		if flagConsoleWait > 0 {
			time.Sleep(time.Duration(flagConsoleWait) * time.Second)
		}
		if err := observer.Stop(); err != nil {
			exitErr("console", err)
		}

		entries := observer.Drain(0)
		if entries == nil {
			entries = []engine.ObserverEvent{}
		}
		if err := b.AppendConsoleLog(entries); err != nil {
			exitErr("console", err)
		}
		if cumulative := b.ConsoleLog(); cumulative != nil {
			entries = cumulative
		}
		entries = filterConsoleLog(entries, level)
		type consoleResult struct {
			Entries []engine.ObserverEvent `json:"entries"`
			Count   int                    `json:"count"`
		}
		output(consoleResult{Entries: entries, Count: len(entries)}, formatConsoleEntries(entries))
	},
}

var networkCompatCmd = &cobra.Command{
	Use:   "network [url]",
	Short: "Observe network requests",
	Long: `Observe network requests during an optional navigation or wait window.
With a persistent session or --connect target, captured requests are appended to
the session network buffer until --clear is used. Ephemeral launches only report
requests captured while the command is running.`,
	Args: cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		targetURL := ""
		if len(args) > 0 {
			targetURL = args[0]
		}
		b, page := openPage()
		defer b.Close()
		if flagNetworkClear {
			if err := b.ClearNetworkLog(); err != nil {
				exitErr("network --clear", err)
			}
			type networkResult struct {
				Entries []*engine.CapturedEntry `json:"entries"`
				Count   int                     `json:"count"`
				Cleared bool                    `json:"cleared"`
			}
			output(networkResult{Entries: []*engine.CapturedEntry{}, Count: 0, Cleared: true}, "network log cleared")
			return
		}

		session, err := engine.StartCapture(page, engine.CaptureSpec{
			Max: flagNetworkMax,
		})
		if err != nil {
			exitErr("network", err)
		}
		if targetURL != "" {
			if _, err := engine.Navigate(page, targetURL, "load"); err != nil {
				fmt.Fprintf(os.Stderr, "navigate: %v\n", err)
			}
		}
		if flagNetworkWait > 0 {
			select {
			case <-session.ReachedMax():
			case <-time.After(time.Duration(flagNetworkWait) * time.Second):
			}
		}
		entries, err := session.Stop()
		if err != nil {
			exitErr("network", err)
		}
		if err := b.AppendNetworkLog(entries); err != nil {
			exitErr("network", err)
		}
		if cumulative := b.NetworkLog(); cumulative != nil {
			entries = cumulative
		}
		entries, err = filterNetworkLog(entries, flagNetworkFilter, flagNetworkStatic)
		if err != nil {
			exitErr("network", err)
		}
		entries = prepareNetworkEntries(entries, flagNetworkRequestBody, flagNetworkRequestHeaders)

		type networkResult struct {
			Entries []*engine.CapturedEntry `json:"entries"`
			Count   int                     `json:"count"`
		}
		output(networkResult{Entries: entries, Count: len(entries)}, formatNetworkEntries(entries))
	},
}

var requestsCompatCmd = &cobra.Command{
	Use:   "requests",
	Short: "List network requests",
	Long: `List all network requests since loading the page.
Each request is numbered for use with request-level commands. Static resources
like images, scripts, CSS, fonts, and media are excluded unless --static is passed.`,
	Args: cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		if flagNetworkClear {
			b, _ := openPage()
			defer b.Close()
			if err := b.ClearNetworkLog(); err != nil {
				exitErr("requests", err)
			}
			type requestsResult struct {
				Action  string `json:"action"`
				Count   int    `json:"count"`
				Cleared bool   `json:"cleared"`
			}
			output(requestsResult{Action: "requests", Count: 0, Cleared: true}, "network log cleared")
			return
		}

		allEntries, err := currentPlaywrightRequestsWithStatic(true)
		if err != nil {
			exitErr("requests", err)
		}
		entries, err := filterNetworkLog(allEntries, flagNetworkFilter, flagNetworkStatic)
		if err != nil {
			exitErr("requests", err)
		}
		text := formatRequests(entries)
		if !flagNetworkStatic {
			all, err := filterNetworkLog(allEntries, flagNetworkFilter, true)
			if err != nil {
				exitErr("requests", err)
			}
			if len(all) > len(entries) {
				text = fmt.Sprintf("%s\nNote: %d static request(s) omitted; pass --static to include them", strings.TrimSpace(text), len(all)-len(entries))
			}
		}

		type requestsResult struct {
			Action  string                  `json:"action"`
			Count   int                     `json:"count"`
			Entries []*engine.CapturedEntry `json:"entries"`
		}
		output(requestsResult{Action: "requests", Count: len(entries), Entries: entries}, text)
	},
}

var requestCompatCmd = &cobra.Command{
	Use:   "request <index>",
	Short: "Show full details of a single request",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		index, err := parseRequestIndex(args[0])
		if err != nil {
			exitErr("request", err)
		}

		entries, err := currentPlaywrightRequests()
		if err != nil {
			exitErr("request", err)
		}
		entry, err := pickRequestEntry(entries, index)
		if err != nil {
			exitErr("request", err)
		}

		if flagRequestFilename != "" {
			if err := writeJSONRequestPayload(flagRequestFilename, entry); err != nil {
				exitErr("request", err)
			}
			output(map[string]any{"action": "request", "index": index, "filename": flagRequestFilename}, fmt.Sprintf("request %d written to %s", index, flagRequestFilename))
			return
		}
		type requestResult struct {
			Index int `json:"index"`
			*engine.CapturedEntry
		}
		output(&requestResult{Index: index, CapturedEntry: entry}, formatPlaywrightRequestEntry(index, entry))
	},
}

var requestHeadersCompatCmd = &cobra.Command{
	Use:   "request-headers <index>",
	Short: "Print request headers for a single request",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		index, err := parseRequestIndex(args[0])
		if err != nil {
			exitErr("request-headers", err)
		}
		entries, err := currentPlaywrightRequests()
		if err != nil {
			exitErr("request-headers", err)
		}
		entry, err := pickRequestEntry(entries, index)
		if err != nil {
			exitErr("request-headers", err)
		}

		if len(entry.ReqHeaders) == 0 {
			output(map[string]any{"index": index, "headers": map[string]string{}}, fmt.Sprintf("request %d headers: none", index))
			return
		}

		if flagRequestFilename != "" {
			if err := writeHeadersPayload(flagRequestFilename, entry.ReqHeaders); err != nil {
				exitErr("request-headers", err)
			}
			output(map[string]any{"index": index, "filename": flagRequestFilename}, fmt.Sprintf("request %d headers written to %s", index, flagRequestFilename))
			return
		}
		type headersResult struct {
			Index   int               `json:"index"`
			Headers map[string]string `json:"headers"`
		}
		output(headersResult{Index: index, Headers: entry.ReqHeaders}, formatPlaywrightHeaders(entry.ReqHeaders, "request"))
	},
}

var requestBodyCompatCmd = &cobra.Command{
	Use:   "request-body <index>",
	Short: "Print request body for a single request",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		index, err := parseRequestIndex(args[0])
		if err != nil {
			exitErr("request-body", err)
		}
		entries, err := currentPlaywrightRequests()
		if err != nil {
			exitErr("request-body", err)
		}
		entry, err := pickRequestEntry(entries, index)
		if err != nil {
			exitErr("request-body", err)
		}

		if flagRequestFilename != "" {
			if err := writeTextPayload(flagRequestFilename, entry.PostData); err != nil {
				exitErr("request-body", err)
			}
			output(map[string]any{"index": index, "filename": flagRequestFilename}, fmt.Sprintf("request %d body written to %s", index, flagRequestFilename))
			return
		}

		type bodyResult struct {
			Index int    `json:"index"`
			Body  string `json:"body"`
		}
		if entry.PostData == "" {
			output(bodyResult{Index: index, Body: ""}, fmt.Sprintf("request %d body: empty", index))
			return
		}
		output(bodyResult{Index: index, Body: entry.PostData}, fmt.Sprintf("request %d body:\n%s", index, entry.PostData))
	},
}

var responseHeadersCompatCmd = &cobra.Command{
	Use:   "response-headers <index>",
	Short: "Print response headers for a single request",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		index, err := parseRequestIndex(args[0])
		if err != nil {
			exitErr("response-headers", err)
		}
		entries, err := currentPlaywrightRequests()
		if err != nil {
			exitErr("response-headers", err)
		}
		entry, err := pickRequestEntry(entries, index)
		if err != nil {
			exitErr("response-headers", err)
		}

		if len(entry.ResHeaders) == 0 {
			output(map[string]any{"index": index, "headers": map[string]string{}}, fmt.Sprintf("request %d response headers: none", index))
			return
		}

		if flagRequestFilename != "" {
			if err := writeHeadersPayload(flagRequestFilename, entry.ResHeaders); err != nil {
				exitErr("response-headers", err)
			}
			output(map[string]any{"index": index, "filename": flagRequestFilename}, fmt.Sprintf("request %d response headers written to %s", index, flagRequestFilename))
			return
		}

		type headersResult struct {
			Index   int               `json:"index"`
			Headers map[string]string `json:"headers"`
		}
		output(headersResult{Index: index, Headers: entry.ResHeaders}, formatPlaywrightHeaders(entry.ResHeaders, "response"))
	},
}

var responseBodyCompatCmd = &cobra.Command{
	Use:   "response-body <index>",
	Short: "Print the response body for a single request",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		index, err := parseRequestIndex(args[0])
		if err != nil {
			exitErr("response-body", err)
		}
		b, page := openPage()
		defer b.Close()
		entries, err := currentPlaywrightRequestsFromBrowser(b)
		if err != nil {
			exitErr("response-body", err)
		}
		entry, err := pickRequestEntry(entries, index)
		if err != nil {
			exitErr("response-body", err)
		}
		if entry.Body == "" && entry.BodyError == "" && entry.RequestID != "" {
			body, base64, berr := engine.GetResponseBodyByRequestID(page, entry.RequestID)
			if berr != nil {
				entry.BodyError = berr.Error()
			} else {
				entry.Body = body
				entry.BodyBase64 = base64
			}
		}

		if entry.Body == "" {
			if entry.BodyError != "" {
				exitErr("response-body", fmt.Errorf("%s", entry.BodyError))
			}
			output(map[string]any{"index": index, "body": ""}, fmt.Sprintf("request %d body: empty", index))
			return
		}

		if entry.BodyBase64 {
			data, err := base64.StdEncoding.DecodeString(entry.Body)
			if err != nil {
				exitErr("response-body", fmt.Errorf("decode response body: %w", err))
			}
			path := flagRequestFilename
			if path == "" {
				path = resolveRequestFilename("response-body", index)
			}
			if err := os.WriteFile(path, data, 0o600); err != nil {
				exitErr("response-body", err)
			}
			output(map[string]any{"index": index, "filename": path}, fmt.Sprintf("request %d body written to %s", index, path))
			return
		}

		if flagRequestFilename != "" {
			if err := writeTextPayload(flagRequestFilename, entry.Body); err != nil {
				exitErr("response-body", err)
			}
			output(map[string]any{"index": index, "filename": flagRequestFilename}, fmt.Sprintf("request %d body written to %s", index, flagRequestFilename))
			return
		}
		type bodyResult struct {
			Index int    `json:"index"`
			Body  string `json:"body"`
		}
		output(bodyResult{Index: index, Body: entry.Body}, fmt.Sprintf("request %d response body:\n%s", index, entry.Body))
	},
}

var highlightCompatCmd = &cobra.Command{
	Use:   "highlight [target]",
	Short: "Draw a persistent overlay around a target element",
	Long: `Draw a pointer-transparent highlight overlay around a target.

TARGET accepts @ref/eN, a unique CSS selector, or a supported Playwright
locator. The overlay is kept in the current page document, so it remains
visible across later CLI commands attached to the same session and page.

Use --hide TARGET to remove one overlay, or --hide without a target to remove
all ghostchrome overlays from the current page. --style accepts CSS declarations
applied to the overlay, for example 'border-color: #22c55e; border-width: 5px'.`,
	Args: cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		if len(args) == 0 && !flagHighlightHide {
			exitErr("highlight", fmt.Errorf("provide a target or use --hide to remove all highlights"))
		}

		b, page := openPage()
		defer b.Close()
		target := ""
		if len(args) == 1 {
			target = args[0]
		}
		var snapshot *engine.PageSnapshot
		if target != "" && isSnapshotRef(target) {
			snapshot = ensureSnapshot(b, page, "", "none", engine.LevelSkeleton)
		}

		if flagHighlightHide {
			if err := overlay.HideHighlights(page, target, snapshot); err != nil {
				exitIfStaleRef(err, "highlight")
				exitErr("highlight", err)
			}
			output(map[string]any{"action": "hide", "target": target}, "highlight hidden")
			return
		}
		if err := overlay.HighlightTarget(page, target, flagHighlightStyle, snapshot); err != nil {
			exitIfStaleRef(err, "highlight")
			exitErr("highlight", err)
		}
		output(map[string]any{"action": "highlight", "target": target, "style": flagHighlightStyle}, "highlighted "+target)
	},
}

var (
	flagHighlightHide  bool
	flagHighlightStyle string
)

func resolveConsoleArgs(args []string, flagLevel string) (level string, targetURL string, err error) {
	level = flagLevel
	if !isConsoleLevel(level) {
		return "", "", errInvalidArg("level", level, "all, error, warning, info, debug")
	}
	if len(args) == 0 {
		return level, "", nil
	}
	if isConsoleLevel(args[0]) {
		level = args[0]
		if len(args) > 1 {
			targetURL = args[1]
		}
		return level, targetURL, nil
	}
	if len(args) > 1 {
		return "", "", fmt.Errorf("unexpected console argument %q; first argument must be a level when two arguments are provided", args[0])
	}
	return level, args[0], nil
}

func isConsoleLevel(level string) bool {
	switch level {
	case "all", "error", "warning", "info", "debug":
		return true
	default:
		return false
	}
}

func consoleFilterLevels(level string) []string {
	switch level {
	case "error":
		return []string{"error"}
	case "warning":
		return []string{"warning", "error"}
	case "all", "info":
		return []string{"log", "info", "warning", "error"}
	case "debug":
		return nil
	default:
		return []string{"log", "info", "warning", "error"}
	}
}

func filterConsoleLog(entries []engine.ObserverEvent, level string) []engine.ObserverEvent {
	allowed := consoleFilterLevels(level)
	if allowed == nil {
		return entries
	}
	allowedSet := make(map[string]struct{}, len(allowed))
	for _, item := range allowed {
		allowedSet[item] = struct{}{}
	}
	out := make([]engine.ObserverEvent, 0, len(entries))
	for _, entry := range entries {
		if _, ok := allowedSet[entry.Level]; ok {
			out = append(out, entry)
		}
	}
	return out
}

func formatConsoleEntries(entries []engine.ObserverEvent) string {
	if len(entries) == 0 {
		return "No console messages"
	}
	lines := make([]string, 0, len(entries))
	for _, entry := range entries {
		source := ""
		if entry.Source != "" {
			source = " (" + entry.Source + ")"
		}
		lines = append(lines, fmt.Sprintf("[console:%s] %s%s", entry.Level, entry.Text, source))
	}
	return strings.Join(lines, "\n")
}

func filterNetworkLog(entries []*engine.CapturedEntry, filter string, includeStatic bool) ([]*engine.CapturedEntry, error) {
	var re *regexp.Regexp
	var err error
	if filter != "" {
		re, err = regexp.Compile(filter)
		if err != nil {
			return nil, err
		}
	}
	out := make([]*engine.CapturedEntry, 0, len(entries))
	for _, entry := range entries {
		if entry == nil {
			continue
		}
		if re != nil && !re.MatchString(entry.URL) {
			continue
		}
		if !includeStatic && engine.IsStaticNetworkEntry(entry.ResourceType, entry.MimeType) {
			continue
		}
		out = append(out, entry)
	}
	return out, nil
}

func formatNetworkEntries(entries []*engine.CapturedEntry) string {
	if len(entries) == 0 {
		return "No network entries"
	}
	lines := make([]string, 0, len(entries))
	for _, entry := range entries {
		lines = append(lines, fmt.Sprintf("[%d] %s %s", entry.Status, entry.Method, entry.URL))
	}
	return strings.Join(lines, "\n")
}

func prepareNetworkEntries(entries []*engine.CapturedEntry, includeRequestBody, includeRequestHeaders bool) []*engine.CapturedEntry {
	for _, entry := range entries {
		if !includeRequestBody {
			entry.PostData = ""
		}
		if !includeRequestHeaders {
			entry.ReqHeaders = nil
		}
	}
	return entries
}

func init() {
	consoleCompatCmd.Flags().StringVar(&flagConsoleLevel, "level", "all", "Filter level: all, error, warning, info, debug")
	consoleCompatCmd.Flags().IntVar(&flagConsoleWait, "wait", 0, "Seconds to keep observing after optional navigation")
	consoleCompatCmd.Flags().BoolVar(&flagConsoleClear, "clear", false, "Clear the current console buffer and return no entries")
	networkCompatCmd.Flags().IntVar(&flagNetworkWait, "wait", 1, "Seconds to keep observing after optional navigation")
	networkCompatCmd.Flags().IntVar(&flagNetworkMax, "max", 0, "Stop after N completed requests (0 = unlimited)")
	networkCompatCmd.Flags().StringVar(&flagNetworkFilter, "filter", "", "Filter requests by URL regex")
	networkCompatCmd.Flags().BoolVar(&flagNetworkStatic, "static", false, "Include static resources such as images, scripts, CSS, fonts, and media")
	networkCompatCmd.Flags().BoolVar(&flagNetworkRequestBody, "request-body", false, "Include request bodies")
	networkCompatCmd.Flags().BoolVar(&flagNetworkRequestHeaders, "request-headers", false, "Include request headers")
	networkCompatCmd.Flags().BoolVar(&flagNetworkClear, "clear", false, "Clear the current network log and return no entries")
	requestsCompatCmd.Flags().StringVar(&flagNetworkFilter, "filter", "", "Filter requests by URL regex")
	requestsCompatCmd.Flags().BoolVar(&flagNetworkStatic, "static", false, "Include static resources such as images, scripts, CSS, fonts, and media")
	requestsCompatCmd.Flags().BoolVar(&flagNetworkClear, "clear", false, "Clear the current network log and return no entries")

	requestCompatCmd.Flags().StringVar(&flagRequestFilename, "filename", "", "Write the request payload to this file")
	requestHeadersCompatCmd.Flags().StringVar(&flagRequestFilename, "filename", "", "Write request headers to this file")
	requestBodyCompatCmd.Flags().StringVar(&flagRequestFilename, "filename", "", "Write request body to this file")
	responseHeadersCompatCmd.Flags().StringVar(&flagRequestFilename, "filename", "", "Write response headers to this file")
	responseBodyCompatCmd.Flags().StringVar(&flagRequestFilename, "filename", "", "Write response body to this file")

	rootCmd.AddCommand(
		consoleCompatCmd,
		networkCompatCmd,
		requestsCompatCmd,
		requestCompatCmd,
		requestHeadersCompatCmd,
		requestBodyCompatCmd,
		responseHeadersCompatCmd,
		responseBodyCompatCmd,
		highlightCompatCmd,
	)
	commandGroups["requests"] = "observe"
	commandGroups["request"] = "observe"
	commandGroups["request-headers"] = "observe"
	commandGroups["request-body"] = "observe"
	commandGroups["response-headers"] = "observe"
	commandGroups["response-body"] = "observe"
	commandGroups["highlight"] = "observe"
	highlightCompatCmd.Flags().BoolVar(&flagHighlightHide, "hide", false, "Hide this target's highlight, or all highlights without a target")
	highlightCompatCmd.Flags().StringVar(&flagHighlightStyle, "style", "", "CSS declarations for the highlight overlay")
}

func resolveRequestFilename(cmd string, index int) string {
	if flagRequestFilename != "" {
		return flagRequestFilename
	}
	base := fmt.Sprintf("%s-%d.bin", strings.ReplaceAll(cmd, "-", "_"), index)
	return filepath.Join(playwrightArtifactPath("network"), base)
}

func currentPlaywrightRequests() ([]*engine.CapturedEntry, error) {
	return currentPlaywrightRequestsWithStatic(false)
}

func currentPlaywrightRequestsWithStatic(includeStatic bool) ([]*engine.CapturedEntry, error) {
	const requestListWarmup = 900 * time.Millisecond
	b, _ := openPage()
	time.Sleep(requestListWarmup)
	b.Close()
	return currentPlaywrightRequestsFromBrowserWithStatic(b, includeStatic)
}

func currentPlaywrightRequestsFromBrowser(b *engine.Browser) ([]*engine.CapturedEntry, error) {
	return currentPlaywrightRequestsFromBrowserWithStatic(b, false)
}

func currentPlaywrightRequestsFromBrowserWithStatic(b *engine.Browser, includeStatic bool) ([]*engine.CapturedEntry, error) {
	if b == nil {
		return nil, fmt.Errorf("browser is nil")
	}

	entries := b.NetworkLog()
	if entries == nil {
		entries = []*engine.CapturedEntry{}
	}
	filtered, err := filterNetworkLog(entries, "", includeStatic)
	if err != nil {
		return nil, err
	}
	return filtered, nil
}

func parseRequestIndex(raw string) (int, error) {
	idx, err := strconv.Atoi(raw)
	if err != nil || idx <= 0 {
		return 0, fmt.Errorf("index must be a positive integer, got %q", raw)
	}
	return idx, nil
}

func pickRequestEntry(entries []*engine.CapturedEntry, index int) (*engine.CapturedEntry, error) {
	if index <= 0 || index > len(entries) {
		return nil, fmt.Errorf("request %d not found (1..%d)", index, len(entries))
	}
	return entries[index-1], nil
}

func formatRequests(entries []*engine.CapturedEntry) string {
	if len(entries) == 0 {
		return "No network requests"
	}
	lines := make([]string, 0, len(entries))
	for i, e := range entries {
		status := e.Status
		if status == 0 {
			status = -1
		}
		lines = append(lines, fmt.Sprintf("%d. %s %d %s", i+1, e.Method, status, e.URL))
	}
	return strings.Join(lines, "\n")
}

func formatPlaywrightRequestEntry(index int, entry *engine.CapturedEntry) string {
	if entry == nil {
		return fmt.Sprintf("request %d not found", index)
	}
	status := entry.Status
	if status == 0 {
		status = -1
	}
	return fmt.Sprintf("request %d -> %s %d %s", index, entry.Method, status, entry.URL)
}

func formatPlaywrightHeaders(headers map[string]string, scope string) string {
	if len(headers) == 0 {
		return fmt.Sprintf("%s headers: none", strings.ToUpper(scope))
	}
	lines := make([]string, 0, len(headers))
	for k, v := range headers {
		lines = append(lines, fmt.Sprintf("%s: %s", k, v))
	}
	return strings.Join(lines, "\n")
}

func writeTextPayload(path, text string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(text), 0o600)
}

func writeHeadersPayload(path string, headers map[string]string) error {
	data, err := jsonMarshalIndent(headers)
	if err != nil {
		return err
	}
	return writeTextPayload(path, data)
}

func writeJSONRequestPayload(path string, payload any) error {
	data, err := jsonMarshalIndent(payload)
	if err != nil {
		return err
	}
	return writeTextPayload(path, data)
}

func jsonMarshalIndent(v any) (string, error) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data), nil
}
