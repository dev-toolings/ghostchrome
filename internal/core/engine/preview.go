package engine

import (
	"fmt"
	"strings"
	"time"

	"github.com/go-rod/rod"
)

// NetworkEntry represents a captured network request.
type NetworkEntry struct {
	Method   string `json:"method,omitempty"`
	URL      string `json:"url"`
	Status   int    `json:"status"`
	Size     int    `json:"size_bytes"`
	TimeMs   int64  `json:"time_ms"`
	MimeType string `json:"mime_type,omitempty"`
	Error    string `json:"error,omitempty"`
}

// PreviewResult is the all-in-one dev report for a page.
type PreviewResult struct {
	PageInfo *PageInfo         `json:"page"`
	Errors   []ErrorEntry      `json:"errors"`
	Network  []NetworkEntry    `json:"network"`
	DOM      *ExtractionResult `json:"dom"`
	Summary  PreviewSummary    `json:"summary"`
}

// PreviewSummary provides quick stats.
type PreviewSummary struct {
	TotalRequests    int `json:"total_requests"`
	FailedRequests   int `json:"failed_requests"`
	ErrorCount       int `json:"error_count"`
	WarningCount     int `json:"warning_count"`
	InteractiveCount int `json:"interactive_count"`
}

// Preview performs a full page analysis: navigate + collect errors + collect network + extract DOM.
func Preview(page *rod.Page, url string, waitStrategy string, extractLevel ExtractLevel, afterNavigate func(*rod.Page) error, stealth bool) (*PreviewResult, error) {
	errorCollector := NewErrorCollector(page)
	defer errorCollector.Close()

	requestTracker := newRequestTracker(page)
	requestTracker.listen(page)
	defer requestTracker.close()

	info, err := Navigate(page, url, waitStrategy)
	if err != nil {
		return nil, err
	}

	// If stealth mode and we got a bot challenge, wait for it to resolve.
	// 403 is typical of DataDome; 503 is Cloudflare's "Just a moment" page.
	if stealth && (info.Status == 403 || info.Status == 503) {
		if WaitForBotChallenge(page, 10*time.Second) {
			// Challenge resolved — re-capture page info
			pageInfo, err := page.Info()
			if err == nil {
				info.URL = pageInfo.URL
				info.Title = pageInfo.Title
				info.Status = requestTracker.MainDocumentStatus()
			}
		}
	}

	if afterNavigate != nil {
		if err := afterNavigate(page); err != nil {
			return nil, err
		}
	}

	dom, err := Extract(page, extractLevel, "", false)
	if err != nil {
		return nil, fmt.Errorf("extract: %w", err)
	}

	errors := append(errorCollector.Errors(), requestTracker.ErrorEntries()...)
	network := requestTracker.Entries()

	failedReqs := 0
	for _, n := range network {
		if n.Status >= 400 || n.Error != "" {
			failedReqs++
		}
	}

	errorCount := 0
	warningCount := 0
	for _, e := range errors {
		if e.Level == "warning" || e.Level == "4xx" {
			warningCount++
			continue
		}
		errorCount++
	}

	return &PreviewResult{
		PageInfo: info,
		Errors:   errors,
		Network:  network,
		DOM:      dom,
		Summary: PreviewSummary{
			TotalRequests:    len(network),
			FailedRequests:   failedReqs,
			ErrorCount:       errorCount,
			WarningCount:     warningCount,
			InteractiveCount: dom.Stats.InteractiveCount,
		},
	}, nil
}

// FormatPreview renders a compact text report (human profile).
func FormatPreview(r *PreviewResult) string {
	return FormatPreviewProfile(r, ProfileHuman("text"))
}

// FormatPreviewProfile renders the preview using the given profile. In agent
// mode, empty sections and zero-stat headers are dropped, failed requests are
// grouped by status code and the DOM dump uses one-letter role tags.
func FormatPreviewProfile(r *PreviewResult, p RenderProfile) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("[%d] %s — %s (%dms)\n", r.PageInfo.Status, r.PageInfo.Title, r.PageInfo.URL, r.PageInfo.TimeMs))

	writePreviewErrors(&sb, r, p)
	writePreviewNetwork(&sb, r, p)
	writePreviewDOM(&sb, r, p)

	return strings.TrimRight(sb.String(), "\n")
}

func writePreviewErrors(sb *strings.Builder, r *PreviewResult, p RenderProfile) {
	if len(r.Errors) == 0 {
		if p.DropEmptyStats {
			return
		}
		sb.WriteString("[errors] none\n")
		return
	}
	sb.WriteString(fmt.Sprintf("[errors] %d error(s), %d warning(s)\n", r.Summary.ErrorCount, r.Summary.WarningCount))
	for _, e := range r.Errors {
		switch e.Type {
		case "console":
			src := ""
			if e.Source != "" {
				src = fmt.Sprintf(" (%s)", truncateURL(e.Source))
			}
			sb.WriteString(fmt.Sprintf("  [%s] %s%s\n", e.Level, truncate(e.Message, 120), src))
		case "network":
			if e.Status > 0 {
				sb.WriteString(fmt.Sprintf("  [%d] %s %s\n", e.Status, defaultMethod(e.Method), truncateURL(e.Message)))
			} else {
				sb.WriteString(fmt.Sprintf("  [error] %s %s (%s)\n", defaultMethod(e.Method), truncateURL(e.Message), truncate(e.Source, 80)))
			}
		}
	}
}

func writePreviewNetwork(sb *strings.Builder, r *PreviewResult, p RenderProfile) {
	if len(r.Network) == 0 {
		if p.DropEmptyStats {
			return
		}
		sb.WriteString("[network] no requests\n")
		return
	}
	if r.Summary.FailedRequests == 0 {
		if p.DropEmptyStats {
			return
		}
		sb.WriteString(fmt.Sprintf("[network] %d reqs, 0 failed\n", r.Summary.TotalRequests))
		return
	}
	sb.WriteString(fmt.Sprintf("[network] %d reqs, %d failed\n", r.Summary.TotalRequests, r.Summary.FailedRequests))
	count := 0
	for _, n := range r.Network {
		if n.Status < 400 && n.Error == "" {
			continue
		}
		if n.Status > 0 {
			sb.WriteString(fmt.Sprintf("  [%d] %s (%dms)\n", n.Status, truncateURL(n.URL), n.TimeMs))
		} else {
			sb.WriteString(fmt.Sprintf("  [error] %s (%dms)\n", truncateURL(n.URL), n.TimeMs))
		}
		count++
		if count >= 5 {
			break
		}
	}
}

func writePreviewDOM(sb *strings.Builder, r *PreviewResult, p RenderProfile) {
	if r.DOM == nil || len(r.DOM.Nodes) == 0 {
		if !p.DropEmptyStats {
			sb.WriteString("[dom] empty\n")
		}
		return
	}
	if !p.Agent {
		sb.WriteString("[dom]\n")
	}
	domText := FormatTextProfile(r.DOM, p)
	for _, line := range strings.Split(domText, "\n") {
		if line == "" {
			continue
		}
		if p.Agent {
			sb.WriteString(line + "\n")
		} else {
			sb.WriteString("  " + line + "\n")
		}
	}
}

func defaultMethod(method string) string {
	if method == "" {
		return "GET"
	}
	return method
}

// truncateURL shortens URLs for preview display (80-char budget).
func truncateURL(u string) string {
	return TruncateURL(u, 80)
}

// truncate shortens a string to maxLen with a trailing ellipsis.
func truncate(s string, maxLen int) string {
	if maxLen <= 3 || len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}
