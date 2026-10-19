package engine

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

// NetworkAuditSpec configures one network audit: every request a page makes
// while it loads, with the code that issued it.
//
// The audit answers "why does this page call that endpoint twice": it records
// from BEFORE the trigger (a reload or a navigation), so nothing the page
// fires during boot is lost, unlike the 250-entry Resource Timing buffer a
// dev server fills with modules before the first API call.
type NetworkAuditSpec struct {
	// Match is a regex applied to the request URL. Empty matches everything.
	Match string
	// IncludeStatic keeps scripts, styles, images, fonts and media.
	IncludeStatic bool
	// QuietWindow ends the audit once no request started or finished for this
	// long. A long-lived stream (SSE) stays open without blocking it.
	QuietWindow time.Duration
	// Timeout bounds the whole audit, trigger included.
	Timeout time.Duration
	// StackDepth is the number of application frames kept per initiator.
	StackDepth int
}

// NetworkAuditRequest is one request, in start order. It never carries
// headers, cookies or bodies, and sensitive query values are redacted.
type NetworkAuditRequest struct {
	StartMs    int64    `json:"start_ms"`
	Method     string   `json:"method"`
	URL        string   `json:"url"`
	Type       string   `json:"type"`
	Status     int      `json:"status,omitempty"`
	DurationMs int64    `json:"duration_ms,omitempty"`
	Pending    bool     `json:"pending,omitempty"`
	Failed     bool     `json:"failed,omitempty"`
	Error      string   `json:"error,omitempty"`
	Initiator  []string `json:"initiator,omitempty"`
}

// NetworkAuditDuplicate groups the requests that share a method and a URL.
type NetworkAuditDuplicate struct {
	Method string `json:"method"`
	URL    string `json:"url"`
	Count  int    `json:"count"`
	// StartsMs tells a burst (same instant, a real duplicate) from a poll
	// (regular spacing, usually by design).
	StartsMs []int64 `json:"starts_ms"`
	// Callers lists the distinct deepest application frames that issued it
	// (the query or component, past the shared transport helpers): two
	// callers means two places to fix, one caller means a re-run.
	Callers []string `json:"callers,omitempty"`
}

// NetworkAuditReport is the audit result.
type NetworkAuditReport struct {
	URL        string                  `json:"url"`
	Total      int                     `json:"total"`
	Duplicates []NetworkAuditDuplicate `json:"duplicates"`
	Requests   []NetworkAuditRequest   `json:"requests"`
	ElapsedMs  int64                   `json:"elapsed_ms"`
}

type auditedRequest struct {
	NetworkAuditRequest
	cdpStart float64
	mime     string
	order    int
	loader   proto.NetworkLoaderID
}

// Frames from these locations are library or tooling code: the audit skips
// them so that the first frame shown is the application line that asked.
var auditLibraryFrame = regexp.MustCompile(`/node_modules/|/\.vite/|/@vite/|/@react-refresh|/@id/|chrome-extension://`)

// AuditNetwork records every request the page makes while trigger runs and
// until the network has been quiet for spec.QuietWindow.
func AuditNetwork(page *rod.Page, spec NetworkAuditSpec, trigger func() error) (*NetworkAuditReport, error) {
	if spec.QuietWindow <= 0 {
		spec.QuietWindow = 1500 * time.Millisecond
	}
	if spec.Timeout <= 0 {
		spec.Timeout = 15 * time.Second
	}
	if spec.StackDepth <= 0 {
		spec.StackDepth = 4
	}
	var match *regexp.Regexp
	if spec.Match != "" {
		re, err := regexp.Compile(spec.Match)
		if err != nil {
			return nil, fmt.Errorf("match regex: %w", err)
		}
		match = re
	}
	if err := (proto.NetworkEnable{}).Call(page); err != nil {
		return nil, fmt.Errorf("network enable: %w", err)
	}
	// Async stacks reach past the `await` that separates a component or a
	// query from its fetch. Runtime only: enabling Debugger would also stop
	// on a `debugger;` statement in the page.
	_ = (proto.RuntimeSetAsyncCallStackDepth{MaxDepth: 32}).Call(page)
	defer func() { _ = (proto.RuntimeSetAsyncCallStackDepth{MaxDepth: 0}).Call(page) }()

	started := time.Now()
	var (
		mu       sync.Mutex
		requests = map[proto.NetworkRequestID]*auditedRequest{}
		order    int
		lastSeen = started
		// The document the trigger loads. Requests of the page being left
		// (cut off mid-flight, never finished) belong to another loader.
		docLoader proto.NetworkLoaderID
	)
	touch := func() { lastSeen = time.Now() }

	ctx, cancel := context.WithCancel(page.GetContext())
	defer cancel()
	wait := page.Context(ctx).EachEvent(
		func(e *proto.NetworkRequestWillBeSent) {
			mu.Lock()
			defer mu.Unlock()
			touch()
			if e.RedirectResponse != nil {
				if prev := requests[e.RequestID]; prev != nil {
					prev.Status = e.RedirectResponse.Status
					prev.DurationMs = cdpMs(float64(e.Timestamp) - prev.cdpStart)
					prev.Pending = false
					// A redirect reuses the request id: keep the hop under its own key.
					requests[proto.NetworkRequestID(fmt.Sprintf("%s#%d", e.RequestID, order))] = prev
				}
			}
			order++
			if docLoader == "" && e.Type == proto.NetworkResourceTypeDocument && e.FrameID == page.FrameID {
				docLoader = e.LoaderID
			}
			requests[e.RequestID] = &auditedRequest{
				NetworkAuditRequest: NetworkAuditRequest{
					StartMs:   time.Since(started).Milliseconds(),
					Method:    e.Request.Method,
					URL:       auditURL(e.Request.URL),
					Type:      string(e.Type),
					Pending:   true,
					Initiator: initiatorFrames(e.Initiator, spec.StackDepth),
				},
				cdpStart: float64(e.Timestamp),
				order:    order,
				loader:   e.LoaderID,
			}
		},
		func(e *proto.NetworkResponseReceived) {
			mu.Lock()
			defer mu.Unlock()
			touch()
			if req := requests[e.RequestID]; req != nil {
				req.Status = e.Response.Status
				req.mime = e.Response.MIMEType
				if req.Type == "" {
					req.Type = string(e.Type)
				}
			}
		},
		func(e *proto.NetworkLoadingFinished) {
			mu.Lock()
			defer mu.Unlock()
			touch()
			if req := requests[e.RequestID]; req != nil {
				req.Pending = false
				req.DurationMs = cdpMs(float64(e.Timestamp) - req.cdpStart)
			}
		},
		func(e *proto.NetworkLoadingFailed) {
			mu.Lock()
			defer mu.Unlock()
			touch()
			if req := requests[e.RequestID]; req != nil {
				req.Pending = false
				req.Failed = true
				req.Error = e.ErrorText
				req.DurationMs = cdpMs(float64(e.Timestamp) - req.cdpStart)
			}
		},
	)
	go wait()

	if err := trigger(); err != nil {
		return nil, err
	}
	deadline := started.Add(spec.Timeout)
	for {
		time.Sleep(100 * time.Millisecond)
		mu.Lock()
		quiet := time.Since(lastSeen) >= spec.QuietWindow && len(requests) > 0
		mu.Unlock()
		if quiet || time.Now().After(deadline) {
			break
		}
	}
	cancel()

	mu.Lock()
	defer mu.Unlock()
	kept := make([]*auditedRequest, 0, len(requests))
	for _, req := range requests {
		if req.URL == "" || strings.HasPrefix(req.URL, "data:") {
			continue
		}
		if docLoader != "" && req.loader != docLoader {
			continue
		}
		if !spec.IncludeStatic && IsStaticNetworkEntry(req.Type, req.mime) {
			continue
		}
		if match != nil && !match.MatchString(req.URL) {
			continue
		}
		kept = append(kept, req)
	}
	sort.Slice(kept, func(i, j int) bool { return kept[i].order < kept[j].order })

	report := &NetworkAuditReport{Total: len(kept), ElapsedMs: time.Since(started).Milliseconds()}
	if info, err := page.Info(); err == nil && info != nil {
		report.URL = auditURL(info.URL)
	}
	type group struct {
		dup   NetworkAuditDuplicate
		seen  map[string]bool
		first int
	}
	groups := map[string]*group{}
	for _, req := range kept {
		report.Requests = append(report.Requests, req.NetworkAuditRequest)
		key := req.Method + " " + req.URL
		g := groups[key]
		if g == nil {
			g = &group{dup: NetworkAuditDuplicate{Method: req.Method, URL: req.URL}, seen: map[string]bool{}, first: req.order}
			groups[key] = g
		}
		g.dup.Count++
		g.dup.StartsMs = append(g.dup.StartsMs, req.StartMs)
		caller := "(no application frame)"
		if len(req.Initiator) > 0 {
			caller = req.Initiator[len(req.Initiator)-1]
		}
		if !g.seen[caller] {
			g.seen[caller] = true
			g.dup.Callers = append(g.dup.Callers, caller)
		}
	}
	dups := make([]*group, 0)
	for _, g := range groups {
		if g.dup.Count > 1 {
			dups = append(dups, g)
		}
	}
	sort.Slice(dups, func(i, j int) bool {
		if dups[i].dup.Count != dups[j].dup.Count {
			return dups[i].dup.Count > dups[j].dup.Count
		}
		return dups[i].first < dups[j].first
	})
	report.Duplicates = make([]NetworkAuditDuplicate, 0, len(dups))
	for _, g := range dups {
		report.Duplicates = append(report.Duplicates, g.dup)
	}
	return report, nil
}

func cdpMs(seconds float64) int64 {
	if seconds <= 0 {
		return 0
	}
	return int64(seconds * 1000)
}

// initiatorFrames returns up to depth application frames, sync stack first
// then the async parents, as "function file:line" with the origin dropped.
func initiatorFrames(initiator *proto.NetworkInitiator, depth int) []string {
	if initiator == nil {
		return nil
	}
	frames := make([]string, 0, depth)
	for stack := initiator.Stack; stack != nil && len(frames) < depth; stack = stack.Parent {
		for _, frame := range stack.CallFrames {
			if len(frames) >= depth {
				break
			}
			if frame == nil || frame.URL == "" || auditLibraryFrame.MatchString(frame.URL) {
				continue
			}
			name := frame.FunctionName
			if name == "" {
				name = "(anonymous)"
			}
			line := fmt.Sprintf("%s %s:%d", name, framePath(frame.URL), frame.LineNumber+1)
			if len(frames) == 0 || frames[len(frames)-1] != line {
				frames = append(frames, line)
			}
		}
	}
	if len(frames) == 0 && initiator.URL != "" && !auditLibraryFrame.MatchString(initiator.URL) {
		line := framePath(initiator.URL)
		if initiator.LineNumber != nil {
			line = fmt.Sprintf("%s:%d", line, int(*initiator.LineNumber)+1)
		}
		frames = append(frames, string(initiator.Type)+" "+line)
	}
	return frames
}

// framePath keeps the path of a script URL: the origin is the same for every
// frame of a page, and a dev server appends cache-busting queries.
func framePath(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Path == "" {
		return raw
	}
	return u.Path
}

var auditSecretKey = regexp.MustCompile(`(?i)token|secret|password|passwd|key|auth|code|signature|session|cookie|credential`)

// auditURL elides data URLs and redacts the values of sensitive query keys.
func auditURL(raw string) string {
	if strings.HasPrefix(raw, "data:") {
		return ElideDataURL(raw)
	}
	u, err := url.Parse(raw)
	if err != nil || u.RawQuery == "" {
		return raw
	}
	query := u.Query()
	changed := false
	for k, values := range query {
		if auditSecretKey.MatchString(k) {
			for i := range values {
				values[i] = "REDACTED"
			}
			changed = true
		}
	}
	if changed {
		u.RawQuery = query.Encode()
	}
	return u.String()
}
