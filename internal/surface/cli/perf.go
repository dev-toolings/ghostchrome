package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-rod/rod/lib/proto"
	"github.com/spf13/cobra"
)

var flagPerfBudget string

type perfMetrics struct {
	URL    string             `json:"url"`
	Title  string             `json:"title"`
	TTFB   float64            `json:"ttfb_ms"`
	FCP    float64            `json:"fcp_ms"`
	LCP    float64            `json:"lcp_ms"`
	INP    float64            `json:"inp_ms"`
	CLS    float64            `json:"cls"`
	DOM    float64            `json:"dom_ms"`
	Load   float64            `json:"load_ms"`
	Budget map[string]float64 `json:"budget,omitempty"`
	Misses []string           `json:"misses,omitempty"`
}

var perfCmd = &cobra.Command{
	Use:   "perf <url>",
	Short: "Capture Web Vitals (TTFB, FCP, LCP, CLS) and optionally compare to a budget",
	Long: `perf navigates to <url>, waits for 'load', then collects Web Vitals via
the browser's PerformanceObserver and Navigation Timing APIs. With --budget,
any metric exceeding its limit yields exit code 1.

Budget format: JSON object with metric names. Units follow the metric:
  TTFB, FCP, LCP, DOM, Load → milliseconds
  CLS                        → unitless score

Examples:
  ghostchrome perf https://example.com
  ghostchrome perf https://app.example.com --budget '{"LCP":2500,"CLS":0.1}'
  ghostchrome perf https://app --budget '@budget.json'`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		targetURL := args[0]

		b, page := openPage()
		defer b.Close()

		navigateIfRequested(page, targetURL, "load")

		metrics, err := collectPerfMetrics(page)
		if err != nil {
			exitErr("perf", err)
		}

		info, _ := page.Info()
		if info != nil {
			metrics.URL = info.URL
			metrics.Title = info.Title
		}

		budget, err := parsePerfBudget(flagPerfBudget)
		if err != nil {
			exitErr("perf", err)
		}
		metrics.Budget = budget

		misses := checkBudget(metrics, budget)
		metrics.Misses = misses

		text := formatPerfText(metrics)
		output(metrics, text)

		if len(misses) > 0 {
			exitNow(1)
		}
	},
}

// collectPerfMetrics runs a tiny polyfill to observe LCP + CLS then reads
// navigation timings + paint entries synchronously.
func collectPerfMetrics(page any) (*perfMetrics, error) {
	p, ok := page.(interface {
		Eval(js string, params ...interface{}) (*proto.RuntimeRemoteObject, error)
	})
	if !ok {
		return nil, fmt.Errorf("page does not support Eval")
	}

	// Give LCP/CLS/INP observers ~500 ms of idle to settle.
	res, err := p.Eval(`async () => {
		let lcp = 0, cls = 0, inp = 0;
		try {
			new PerformanceObserver((list) => {
				for (const e of list.getEntries()) lcp = Math.max(lcp, e.startTime);
			}).observe({type: 'largest-contentful-paint', buffered: true});
		} catch (e) {}
		try {
			new PerformanceObserver((list) => {
				for (const e of list.getEntries()) if (!e.hadRecentInput) cls += e.value;
			}).observe({type: 'layout-shift', buffered: true});
		} catch (e) {}
		try {
			new PerformanceObserver((list) => {
				for (const e of list.getEntries()) {
					const d = e.processingEnd - e.startTime;
					if (d > inp) inp = d;
				}
			}).observe({type: 'event', buffered: true, durationThreshold: 0});
		} catch (e) {}
		await new Promise(r => setTimeout(r, 500));

		const nav = performance.getEntriesByType('navigation')[0] || {};
		const fcp = (performance.getEntriesByName('first-contentful-paint')[0] || {}).startTime || 0;
		return {
			ttfb: nav.responseStart || 0,
			fcp,
			lcp,
			inp,
			cls,
			dom: nav.domContentLoadedEventEnd || 0,
			load: nav.loadEventEnd || 0,
		};
	}`)
	if err != nil {
		return nil, fmt.Errorf("eval perf: %w", err)
	}

	raw, err := res.Value.MarshalJSON()
	if err != nil {
		return nil, err
	}
	var parsed struct {
		TTFB float64 `json:"ttfb"`
		FCP  float64 `json:"fcp"`
		LCP  float64 `json:"lcp"`
		INP  float64 `json:"inp"`
		CLS  float64 `json:"cls"`
		DOM  float64 `json:"dom"`
		Load float64 `json:"load"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("decode perf payload: %w", err)
	}

	return &perfMetrics{
		TTFB: round2(parsed.TTFB),
		FCP:  round2(parsed.FCP),
		LCP:  round2(parsed.LCP),
		INP:  round2(parsed.INP),
		CLS:  round4(parsed.CLS),
		DOM:  round2(parsed.DOM),
		Load: round2(parsed.Load),
	}, nil
}

func round2(v float64) float64 { return float64(int(v*100)) / 100 }
func round4(v float64) float64 { return float64(int(v*10000)) / 10000 }

func parsePerfBudget(raw string) (map[string]float64, error) {
	if raw == "" {
		return nil, nil
	}
	if strings.HasPrefix(raw, "@") {
		filePath := raw[1:]

		// Reject paths with ".." to prevent directory traversal
		cleaned := filepath.Clean(filePath)
		if strings.Contains(cleaned, "..") {
			return nil, fmt.Errorf("budget file path %q contains parent directory references", filePath)
		}

		// Ensure the path is within the current working directory
		cwd, err := os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("cannot determine working directory: %w", err)
		}

		absPath, err := filepath.Abs(cleaned)
		if err != nil {
			return nil, fmt.Errorf("cannot resolve budget file path %q: %w", filePath, err)
		}

		// Check that absPath is within CWD
		if !strings.HasPrefix(absPath, cwd+string(os.PathSeparator)) && absPath != cwd {
			return nil, fmt.Errorf("budget file path %q is outside current working directory", filePath)
		}

		data, err := os.ReadFile(absPath)
		if err != nil {
			return nil, fmt.Errorf("budget file: %w", err)
		}
		raw = string(data)
	}
	out := map[string]float64{}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, fmt.Errorf("budget JSON: %w", err)
	}
	return out, nil
}

func checkBudget(m *perfMetrics, budget map[string]float64) []string {
	if len(budget) == 0 {
		return nil
	}
	var misses []string
	check := func(name string, got, limit float64) {
		if limit > 0 && got > limit {
			misses = append(misses, fmt.Sprintf("%s=%.2f > limit %.2f", name, got, limit))
		}
	}
	for k, v := range budget {
		switch strings.ToUpper(k) {
		case "TTFB":
			check("TTFB", m.TTFB, v)
		case "FCP":
			check("FCP", m.FCP, v)
		case "LCP":
			check("LCP", m.LCP, v)
		case "CLS":
			check("CLS", m.CLS, v)
		case "DOM":
			check("DOM", m.DOM, v)
		case "INP":
			check("INP", m.INP, v)
		case "LOAD":
			check("Load", m.Load, v)
		}
	}
	return misses
}

func formatPerfText(m *perfMetrics) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "[perf] %s\n", m.URL)
	fmt.Fprintf(&sb, "  TTFB %.0fms  FCP %.0fms  LCP %.0fms  INP %.0fms  CLS %.3f\n", m.TTFB, m.FCP, m.LCP, m.INP, m.CLS)
	fmt.Fprintf(&sb, "  DOM  %.0fms  Load %.0fms\n", m.DOM, m.Load)
	if len(m.Misses) > 0 {
		sb.WriteString("  FAIL:\n")
		for _, miss := range m.Misses {
			fmt.Fprintf(&sb, "    ✗ %s\n", miss)
		}
	}
	return strings.TrimRight(sb.String(), "\n")
}

func init() {
	perfCmd.Flags().StringVar(&flagPerfBudget, "budget", "", "JSON budget object or @path/to/file.json")
	rootCmd.AddCommand(perfCmd)
}
