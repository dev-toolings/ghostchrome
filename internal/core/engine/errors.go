package engine

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

// ErrorEntry represents a single console or network error.
type ErrorEntry struct {
	Type    string `json:"type"`             // "console" or "network"
	Level   string `json:"level"`            // "error", "warning", "4xx", "5xx"
	Message string `json:"message"`          // error message or URL
	Source  string `json:"source"`           // file:line for console, URL for network
	Status  int    `json:"status,omitempty"` // HTTP status for network errors
	Method  string `json:"method,omitempty"` // HTTP method for network
	TimeMs  int64  `json:"time_ms"`          // timestamp relative to collector start
}

// ErrorsFromEvents returns the retained console and network errors in an
// observer history. Reading errors does not consume the session history.
func ErrorsFromEvents(events []ObserverEvent) []ErrorEntry {
	out := make([]ErrorEntry, 0)
	for _, e := range events {
		switch e.Kind {
		case KindConsole, KindError:
			if e.Level == "error" || e.Level == "warning" {
				out = append(out, ErrorEntry{Type: "console", Level: e.Level, Message: e.Text, Source: e.Source, TimeMs: e.TS})
			}
		case KindNet:
			if e.Status >= 400 || e.Failed != "" {
				level := "error"
				if e.Status >= 500 {
					level = "5xx"
				} else if e.Status >= 400 {
					level = "4xx"
				}
				out = append(out, ErrorEntry{Type: "network", Level: level, Message: e.URL, Source: e.URL, Status: e.Status, Method: e.Method, TimeMs: e.DurationMs})
			}
		}
	}
	return out
}

// ErrorCollector collects console-side errors from a page via CDP events.
type ErrorCollector struct {
	mu        sync.Mutex
	errors    []ErrorEntry
	startAt   time.Time
	cancel    func()
	done      chan struct{}
	closeOnce sync.Once
}

// NewErrorCollector creates a collector and starts listening on the page.
// It hooks into RuntimeConsoleAPICalled, RuntimeExceptionThrown, and
// LogEntryAdded. The Log domain carries browser-side messages (CORS,
// mixed content, CSP violations, deprecation, network ERR_*, cookie
// warnings) that the Runtime domain does NOT surface — they show up
// in DevTools Console but go through a different CDP channel.
// The caller must call Close() to detach the listeners.
func NewErrorCollector(page *rod.Page) *ErrorCollector {
	c := &ErrorCollector{
		startAt: time.Now(),
	}

	// Log domain must be enabled explicitly — it carries the browser-side
	// channel (CORS, CSP, network ERR_*, deprecation) that Runtime does NOT
	// surface. Runtime is auto-enabled by Rod's EachEvent via reflection
	// (browser.go:367-371) when we subscribe to Runtime.* events.
	_ = (proto.LogEnable{}).Call(page)

	// CRITICAL: page.EachEvent returns the dispatch loop, not a detach
	// callback. The handlers fire only while that loop is actively draining
	// the messages channel — without `go wait()`, events buffer up but no
	// callback ever runs and the snapshot returns empty. Bug found 2026-05.
	scoped, cancel := page.WithCancel()
	c.cancel = cancel
	c.done = make(chan struct{})

	// Runtime.* subscriptions make go-rod auto-send `Runtime.enable` — a signal
	// DataDome & co. read as an automation tell. Under evasion we omit them
	// (the Log-domain handler below still captures network/CSP/deprecation).
	handlers := make([]interface{}, 0, 3)
	if !EvadeRuntimeEnable() {
		handlers = append(handlers,
			func(e *proto.RuntimeConsoleAPICalled) {
				typ := string(e.Type)
				if typ != "error" && typ != "warning" {
					return
				}

				// Build message from args
				var parts []string
				for _, arg := range e.Args {
					if !arg.Value.Nil() {
						parts = append(parts, arg.Value.String())
					} else if arg.Description != "" {
						parts = append(parts, arg.Description)
					} else if arg.UnserializableValue != "" {
						parts = append(parts, string(arg.UnserializableValue))
					}
				}
				msg := strings.Join(parts, " ")
				if msg == "" {
					msg = "(empty)"
				}

				// Build source from stack trace
				source := ""
				if e.StackTrace != nil && len(e.StackTrace.CallFrames) > 0 {
					f := e.StackTrace.CallFrames[0]
					source = fmt.Sprintf("%s:%d", f.URL, f.LineNumber)
				}

				c.mu.Lock()
				c.errors = append(c.errors, ErrorEntry{
					Type:    "console",
					Level:   typ,
					Message: msg,
					Source:  source,
					TimeMs:  time.Since(c.startAt).Milliseconds(),
				})
				c.mu.Unlock()
			},
			func(e *proto.RuntimeExceptionThrown) {
				msg := ""
				source := ""
				if e.ExceptionDetails.Exception != nil {
					if e.ExceptionDetails.Exception.Description != "" {
						msg = e.ExceptionDetails.Exception.Description
					} else if !e.ExceptionDetails.Exception.Value.Nil() {
						msg = e.ExceptionDetails.Exception.Value.String()
					}
				}
				if msg == "" && e.ExceptionDetails.Text != "" {
					msg = e.ExceptionDetails.Text
				}
				if e.ExceptionDetails.URL != "" {
					source = fmt.Sprintf("%s:%d", e.ExceptionDetails.URL, e.ExceptionDetails.LineNumber)
				} else if e.ExceptionDetails.StackTrace != nil && len(e.ExceptionDetails.StackTrace.CallFrames) > 0 {
					f := e.ExceptionDetails.StackTrace.CallFrames[0]
					source = fmt.Sprintf("%s:%d", f.URL, f.LineNumber)
				}

				c.mu.Lock()
				c.errors = append(c.errors, ErrorEntry{
					Type:    "console",
					Level:   "error",
					Message: msg,
					Source:  source,
					TimeMs:  time.Since(c.startAt).Milliseconds(),
				})
				c.mu.Unlock()
			},
		)
	}
	handlers = append(handlers,
		func(e *proto.LogEntryAdded) {
			if e.Entry == nil {
				return
			}
			level := string(e.Entry.Level)
			// Map CDP Log levels to the collector's two-level taxonomy.
			// "error"/"warning" are kept as-is; "info"/"verbose" are
			// dropped (they would drown the signal in noise).
			if level != "error" && level != "warning" {
				return
			}
			msg := e.Entry.Text
			if msg == "" {
				return
			}
			// Prefix with the CDP source category so callers can tell
			// a CORS error apart from a JS deprecation warning at a
			// glance: "[network] Access to fetch ...".
			if src := string(e.Entry.Source); src != "" {
				msg = "[" + src + "] " + msg
			}
			source := e.Entry.URL
			if source != "" && e.Entry.LineNumber != nil {
				source = fmt.Sprintf("%s:%d", source, *e.Entry.LineNumber)
			}
			c.mu.Lock()
			c.errors = append(c.errors, ErrorEntry{
				Type:    "console",
				Level:   level,
				Message: msg,
				Source:  source,
				TimeMs:  time.Since(c.startAt).Milliseconds(),
			})
			c.mu.Unlock()
		},
	)

	wait := scoped.EachEvent(handlers...)

	go func() {
		defer close(c.done)
		wait()
	}()

	return c
}

// Close detaches the error listeners and waits for the dispatch goroutine
// to drain any in-flight events before returning, so callers that snapshot
// just before Close see a consistent view.
func (c *ErrorCollector) Close() {
	c.closeOnce.Do(func() {
		if c.cancel != nil {
			c.cancel()
		}
		if c.done != nil {
			<-c.done
		}
	})
}

// CollectErrors navigates if needed and returns console plus network errors.
func CollectErrors(page *rod.Page, url string, waitStrategy string, afterNavigate func(*rod.Page) error) ([]ErrorEntry, error) {
	consoleCollector := NewErrorCollector(page)
	defer consoleCollector.Close()

	requestTracker := newRequestTracker(page)
	requestTracker.listen(page)
	defer requestTracker.close()

	if url != "" {
		if _, err := Navigate(page, url, waitStrategy); err != nil {
			return nil, err
		}
		if afterNavigate != nil {
			if err := afterNavigate(page); err != nil {
				return nil, err
			}
		}
	}

	// Drain the dispatch goroutine before snapshotting: Close cancels the
	// scoped context and waits for the goroutine to finish processing all
	// buffered events. Without this, events that fired during navigation
	// but were not yet drained get dropped on return.
	consoleCollector.Close()

	errors := append(consoleCollector.Errors(), requestTracker.ErrorEntries()...)
	return errors, nil
}

// Errors returns all collected errors (snapshot).
func (c *ErrorCollector) Errors() []ErrorEntry {
	c.mu.Lock()
	defer c.mu.Unlock()
	result := make([]ErrorEntry, len(c.errors))
	copy(result, c.errors)
	return result
}

// FormatErrors formats errors as compact text lines.
func FormatErrors(errors []ErrorEntry) string {
	if len(errors) == 0 {
		return "No errors found"
	}
	var lines []string
	for _, e := range errors {
		switch e.Type {
		case "console":
			src := ""
			if e.Source != "" {
				src = fmt.Sprintf(" (%s)", e.Source)
			}
			lines = append(lines, fmt.Sprintf("[console:%s] %s%s", e.Level, e.Message, src))
		case "network":
			method := e.Method
			if method == "" {
				method = "GET"
			}
			lines = append(lines, fmt.Sprintf("[network:%d] %s %s (%dms)", e.Status, method, e.Message, e.TimeMs))
		}
	}
	return strings.Join(lines, "\n")
}
