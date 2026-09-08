package engine

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

// InterceptSpec configures a request interception router.
type InterceptSpec struct {
	// BlockPatterns list glob URL patterns to block with
	// NetworkErrorReasonBlockedByClient.
	BlockPatterns []string

	// FulfillPattern optionally matches requests to be answered with the
	// FulfillBody payload and FulfillStatus response code. Only set one pattern.
	FulfillPattern       string
	FulfillBody          []byte
	FulfillStatus        int
	FulfillContentType   string
	FulfillHeaders       map[string]string
	RemoveRequestHeaders []string

	// Rules are pre-compiled rule-file entries. When non-empty, each incoming
	// request is matched against them before falling through to FulfillPattern /
	// BlockPatterns. Populated by LoadRules; callers should not build this slice
	// manually.
	Rules []compiledRule
}

// InterceptStats are cumulative counters updated by the router goroutine.
type InterceptStats struct {
	mu        sync.Mutex
	Blocked   int
	Fulfilled int
	Passed    int
}

// Snapshot returns a concurrent-safe copy of the counters.
func (s *InterceptStats) Snapshot() (blocked, fulfilled, passed int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Blocked, s.Fulfilled, s.Passed
}

// InterceptSession owns the router lifetime. Stop() must be called to release
// resources.
type InterceptSession struct {
	router *rod.HijackRouter
	stats  *InterceptStats
	done   chan struct{}
}

// Stats returns the live counters.
func (s *InterceptSession) Stats() *InterceptStats { return s.stats }

// Stop disables interception and waits for the background goroutine.
func (s *InterceptSession) Stop() error {
	err := s.router.Stop()
	<-s.done
	return err
}

// StartIntercept enables Fetch interception on the browser and returns an
// InterceptSession. The caller is responsible for Stop().
func StartIntercept(browser *rod.Browser, spec InterceptSpec) (*InterceptSession, error) {
	if len(spec.BlockPatterns) == 0 && spec.FulfillPattern == "" && len(spec.Rules) == 0 {
		return nil, fmt.Errorf("intercept: need at least one --block pattern, --fulfill pattern, or --rules file")
	}

	router := browser.HijackRequests()
	stats := &InterceptStats{}

	if len(spec.Rules) > 0 {
		rules := spec.Rules
		if err := router.Add("*", "", func(h *rod.Hijack) {
			url := h.Request.URL().String()
			method := h.Request.Method()
			rule, ok := MatchRule(rules, url, method)
			if !ok {
				h.ContinueRequest(&proto.FetchContinueRequest{})
				stats.mu.Lock()
				stats.Passed++
				stats.mu.Unlock()
				return
			}
			h.Response.Payload().ResponseCode = rule.status
			h.Response.Payload().Body = rule.body
			if rule.contentType != "" {
				h.Response.SetHeader("Content-Type", rule.contentType)
			}
			for k, v := range rule.headers {
				h.Response.SetHeader(k, v)
			}
			stats.mu.Lock()
			stats.Fulfilled++
			stats.mu.Unlock()
		}); err != nil {
			return nil, fmt.Errorf("add rules catch-all: %w", err)
		}
	}

	for _, pattern := range spec.BlockPatterns {
		p := pattern
		if err := router.Add(p, "", func(h *rod.Hijack) {
			h.Response.Fail(proto.NetworkErrorReasonBlockedByClient)
			stats.mu.Lock()
			stats.Blocked++
			stats.mu.Unlock()
		}); err != nil {
			return nil, fmt.Errorf("add block pattern %q: %w", p, err)
		}
	}

	if spec.FulfillPattern != "" {
		body := spec.FulfillBody
		status := spec.FulfillStatus
		if status == 0 {
			status = 200
		}
		contentType := spec.FulfillContentType
		if contentType == "" {
			contentType = detectContentType(body, spec.FulfillPattern)
		}
		if err := router.Add(spec.FulfillPattern, "", func(h *rod.Hijack) {
			if len(spec.RemoveRequestHeaders) > 0 {
				h.ContinueRequest(&proto.FetchContinueRequest{Headers: filteredRequestHeaders(h, spec.RemoveRequestHeaders)})
				stats.mu.Lock()
				stats.Passed++
				stats.mu.Unlock()
				return
			}
			h.Response.Payload().ResponseCode = status
			h.Response.Payload().Body = body
			if contentType != "" {
				h.Response.SetHeader("Content-Type", contentType)
			}
			for k, v := range spec.FulfillHeaders {
				h.Response.SetHeader(k, v)
			}
			stats.mu.Lock()
			stats.Fulfilled++
			stats.mu.Unlock()
		}); err != nil {
			return nil, fmt.Errorf("add fulfill pattern %q: %w", spec.FulfillPattern, err)
		}
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		router.Run()
	}()

	return &InterceptSession{router: router, stats: stats, done: done}, nil
}

func filteredRequestHeaders(h *rod.Hijack, remove []string) []*proto.FetchHeaderEntry {
	removeSet := make(map[string]struct{}, len(remove))
	for _, name := range remove {
		name = strings.ToLower(strings.TrimSpace(name))
		if name != "" {
			removeSet[name] = struct{}{}
		}
	}
	var headers []*proto.FetchHeaderEntry
	for name, value := range h.Request.Headers() {
		if _, drop := removeSet[strings.ToLower(name)]; drop {
			continue
		}
		headers = append(headers, &proto.FetchHeaderEntry{Name: name, Value: value.String()})
	}
	return headers
}

// ParseBlockList splits a comma-separated glob list, trimming spaces and
// dropping empty entries.
func ParseBlockList(s string) []string {
	if s == "" {
		return nil
	}
	raw := strings.Split(s, ",")
	out := make([]string, 0, len(raw))
	for _, p := range raw {
		if trimmed := strings.TrimSpace(p); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

// LoadFulfillBody returns raw bytes from a @path literal or the string
// otherwise. Useful so CLI flags can take `"@mock.json"` or an inline payload.
func LoadFulfillBody(value string) ([]byte, error) {
	if strings.HasPrefix(value, "@") {
		return os.ReadFile(value[1:])
	}
	return []byte(value), nil
}

func detectContentType(body []byte, pattern string) string {
	if len(body) > 0 && (body[0] == '{' || body[0] == '[') {
		return "application/json"
	}
	switch strings.ToLower(filepath.Ext(pattern)) {
	case ".json":
		return "application/json"
	case ".html":
		return "text/html"
	case ".js":
		return "application/javascript"
	case ".css":
		return "text/css"
	}
	return http.DetectContentType(body)
}
