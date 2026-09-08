package inspect

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/dev-toolings/ghostchrome/engine"
	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

// CollectedItem is a single listing item extracted from a page.
type CollectedItem struct {
	Title  string            `json:"title"`
	Price  string            `json:"price,omitempty"`
	URL    string            `json:"url,omitempty"`
	Fields map[string]string `json:"fields,omitempty"`
}

// CollectResult holds all collected items from a listing page.
type CollectResult struct {
	PageURL   string          `json:"page_url"`
	ItemCount int             `json:"item_count"`
	Items     []CollectedItem `json:"items"`
}

// Collect auto-detects repeated listing cards on a page and extracts structured data.
// It finds elements containing price patterns (€, $, £), groups them by common ancestor,
// and extracts title, price, URL, and metadata from each card.
func Collect(page *rod.Page, limit int) (*CollectResult, error) {
	script := `() => {
		// --- Step 1: Find all leaf elements containing a price ---
		const priceRegex = /[\d\s.,]+\s*[€$£]/;
		const allElements = document.querySelectorAll('*');
		const priceElements = [];
		for (const el of allElements) {
			if (el.children.length === 0 && priceRegex.test(el.textContent)) {
				priceElements.push(el);
			}
		}
		if (priceElements.length === 0) return JSON.stringify({items: [], error: "no price elements found"});

		// --- Step 2: Find the common card ancestor for each price element ---
		// Walk up until we find a repeated parent (li, article, div with siblings of same tag/class)
		function findCard(priceEl) {
			let el = priceEl;
			for (let depth = 0; depth < 10; depth++) {
				el = el.parentElement;
				if (!el || el === document.body) return null;
				const tag = el.tagName;
				const cls = el.className;
				// Check if parent has multiple siblings with the same structure
				const parent = el.parentElement;
				if (!parent) continue;
				let siblingCount = 0;
				for (const sib of parent.children) {
					if (sib.tagName === tag && (cls === '' || sib.className === cls)) {
						siblingCount++;
					}
				}
				// A listing usually has 3+ similar siblings
				if (siblingCount >= 3) return el;
			}
			return null;
		}

		const cards = new Map(); // card element -> price text
		for (const pe of priceElements) {
			const card = findCard(pe);
			if (card && !cards.has(card)) {
				cards.set(card, pe.textContent.trim());
			}
		}

		if (cards.size === 0) return JSON.stringify({items: [], error: "no card structure detected"});

		// --- Step 3: Extract structured data from each card ---
		const yearRegex = /(?:Année\s*)?(\b20[0-2]\d\b)/;
		// Match km: look for "NNN NNN km" pattern, skip if preceded by "Année YYYY"
		const kmRegex = /(?:Année\s+\d{4}\s+)?([\d]{1,3}(?:[\s.]\d{3})*)\s*km/i;
		const locationRegex = /([A-ZÀ-Ü][a-zà-ü\u00C0-\u024F\-\s']{2,})\s*\((\d{5})\)/;
		const fuelRegex = /\b(Diesel|Essence|Hybride|Électrique|GPL|Ethanol)\b/i;
		const gearboxRegex = /\b(Boîte\s+(?:automatique|manuelle|semi[- ]automatique)|BVA|BVM|EAT\d?|DSG\d?)\b/i;

		const items = [];
		const LIMIT = ` + fmt.Sprintf("%d", limit) + `;

		for (const [card, priceText] of cards) {
			if (items.length >= LIMIT) break;

			const text = card.textContent.replace(/\s+/g, ' ').trim();

			// Price: clean the matched price text
			const priceClean = priceText.replace(/\s+/g, ' ').trim();

			// URL: first link with a long href (ad detail page, not category)
			let url = '';
			const links = card.querySelectorAll('a[href]');
			for (const link of links) {
				const href = link.href;
				if (href && href.length > 40 && /\d{5,}/.test(href)) {
					url = href;
					break;
				}
			}
			// Fallback: any link
			if (!url && links.length > 0) {
				for (const link of links) {
					if (link.href && !link.href.endsWith('/')) {
						url = link.href;
						break;
					}
				}
			}

			// Title: text from the first significant link, or first heading
			let title = '';
			const heading = card.querySelector('h1, h2, h3, h4, [class*="title"], [class*="Title"]');
			if (heading) {
				title = heading.textContent.replace(/\s+/g, ' ').trim();
			}
			if (!title) {
				for (const link of links) {
					const lt = link.textContent.replace(/\s+/g, ' ').trim();
					if (lt.length > 10 && lt.length < 200) {
						title = lt;
						break;
					}
				}
			}
			if (!title) {
				// Use the first 100 chars of card text, excluding the price
				title = text.replace(priceClean, '').trim().substring(0, 100);
			}

			// Fields: auto-detect common patterns
			const fields = {};
			const yearMatch = text.match(yearRegex);
			if (yearMatch) fields.year = yearMatch[1];

			// Extract km: strip the year prefix first to avoid "2019157913"
			const textNoYear = text.replace(/Année\s+\d{4}\s*/g, '');
			const kmMatch = textNoYear.match(kmRegex);
			if (kmMatch) fields.km = kmMatch[1].replace(/\s/g, '').replace(/\./g, '');

			const locMatch = text.match(locationRegex);
			if (locMatch) fields.location = locMatch[1].trim() + ' (' + locMatch[2] + ')';

			const fuelMatch = text.match(fuelRegex);
			if (fuelMatch) fields.fuel = fuelMatch[1];

			const gearboxMatch = text.match(gearboxRegex);
			if (gearboxMatch) fields.gearbox = gearboxMatch[1];

			// Filter noise: skip items with no URL, script-like titles, or no real price
			if (!url || title.length < 5) continue;
			if (/function\s*\(|var\s+|const\s+|let\s+|document\.|window\.|jQuery|\$\(/.test(title)) continue;
			if (!/\d/.test(priceClean)) continue;

			items.push({title, price: priceClean, url, fields});
		}

		return JSON.stringify({items});
	}`

	res, err := page.Eval(script)
	if err != nil {
		return nil, fmt.Errorf("collect eval: %w", err)
	}

	raw := res.Value.Str()

	// Parse the JSON result
	var parsed struct {
		Items []struct {
			Title  string            `json:"title"`
			Price  string            `json:"price"`
			URL    string            `json:"url"`
			Fields map[string]string `json:"fields"`
		} `json:"items"`
		Error string `json:"error"`
	}

	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return nil, fmt.Errorf("collect parse: %w", err)
	}

	if parsed.Error != "" && len(parsed.Items) == 0 {
		return nil, fmt.Errorf("collect: %s", parsed.Error)
	}

	items := make([]CollectedItem, len(parsed.Items))
	for i, p := range parsed.Items {
		items[i] = CollectedItem{
			Title:  p.Title,
			Price:  p.Price,
			URL:    p.URL,
			Fields: p.Fields,
		}
	}

	info, _ := page.Info()
	pageURL := ""
	if info != nil {
		pageURL = info.URL
	}

	return &CollectResult{
		PageURL:   pageURL,
		ItemCount: len(items),
		Items:     items,
	}, nil
}

// FormatCollect renders a compact text table from collected items.
func FormatCollect(r *CollectResult) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("[collect] %d items from %s\n", r.ItemCount, r.PageURL))

	for i, item := range r.Items {
		sb.WriteString(fmt.Sprintf("\n#%d %s\n", i+1, item.Price))
		sb.WriteString(fmt.Sprintf("  %s\n", item.Title))

		// Fields on one line
		if len(item.Fields) > 0 {
			parts := make([]string, 0, len(item.Fields))
			order := []string{"year", "km", "fuel", "gearbox", "location"}
			for _, key := range order {
				if v, ok := item.Fields[key]; ok {
					parts = append(parts, key+": "+v)
				}
			}
			// Any remaining fields
			for k, v := range item.Fields {
				found := false
				for _, o := range order {
					if k == o {
						found = true
						break
					}
				}
				if !found {
					parts = append(parts, k+": "+v)
				}
			}
			if len(parts) > 0 {
				sb.WriteString("  " + strings.Join(parts, " | ") + "\n")
			}
		}

		if item.URL != "" {
			sb.WriteString(fmt.Sprintf("  %s\n", item.URL))
		}
	}

	return strings.TrimRight(sb.String(), "\n")
}

// SiteResult holds the collect result for a single URL in a multi-collect.
type SiteResult struct {
	URL    string          `json:"url"`
	Items  []CollectedItem `json:"items"`
	Count  int             `json:"count"`
	TimeMs int64           `json:"time_ms"`
	Error  string          `json:"error,omitempty"`
}

// MultiCollectResult holds results from parallel multi-URL collection.
type MultiCollectResult struct {
	TotalItems  int          `json:"total_items"`
	TotalTimeMs int64        `json:"total_time_ms"`
	Sites       []SiteResult `json:"sites"`
}

// MultiCollect scrapes multiple URLs in parallel using separate browser tabs.
// Each URL gets its own tab, navigates, collects, and closes.
// maxParallel caps the number of concurrent tabs; <= 0 falls back to 5.
func MultiCollect(browser *rod.Browser, urls []string, limit int, stealth bool, maxParallel int) *MultiCollectResult {
	start := time.Now()
	results := make([]SiteResult, len(urls))
	var wg sync.WaitGroup

	if maxParallel <= 0 {
		maxParallel = 5
	}
	if maxParallel > len(urls) {
		maxParallel = len(urls)
	}
	sem := make(chan struct{}, maxParallel)

	for i, u := range urls {
		wg.Add(1)
		go func(idx int, targetURL string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			siteStart := time.Now()

			// Open a new tab
			page, err := browser.Page(proto.TargetCreateTarget{URL: "about:blank"})
			if err != nil {
				results[idx] = SiteResult{URL: targetURL, Error: err.Error()}
				return
			}
			defer page.Close()

			// Apply stealth per tab
			if stealth {
				if err := engine.ApplyStealth(page); err != nil {
					results[idx] = SiteResult{URL: targetURL, Error: "stealth: " + err.Error()}
					return
				}
			}

			// Navigate
			if err := page.Navigate(targetURL); err != nil {
				results[idx] = SiteResult{URL: targetURL, Error: "navigate: " + err.Error()}
				return
			}
			_ = page.WaitStable(500 * time.Millisecond)

			// Collect
			result, err := Collect(page, limit)
			if err != nil {
				results[idx] = SiteResult{
					URL:    targetURL,
					Error:  err.Error(),
					TimeMs: time.Since(siteStart).Milliseconds(),
				}
				return
			}

			results[idx] = SiteResult{
				URL:    targetURL,
				Items:  result.Items,
				Count:  result.ItemCount,
				TimeMs: time.Since(siteStart).Milliseconds(),
			}
		}(i, u)
	}

	wg.Wait()

	totalItems := 0
	for i := range results {
		totalItems += results[i].Count
	}

	return &MultiCollectResult{
		TotalItems:  totalItems,
		TotalTimeMs: time.Since(start).Milliseconds(),
		Sites:       results,
	}
}

// FormatMultiCollect renders a compact text report for multi-URL results.
func FormatMultiCollect(r *MultiCollectResult) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("[multi-collect] %d items from %d sites in %dms\n",
		r.TotalItems, len(r.Sites), r.TotalTimeMs))

	for _, site := range r.Sites {
		if site.Error != "" {
			sb.WriteString(fmt.Sprintf("\n[error] %s — %s\n", site.URL, site.Error))
			continue
		}
		sb.WriteString(fmt.Sprintf("\n[%s] %d items (%dms)\n", truncateCollectURL(site.URL), site.Count, site.TimeMs))
		for i, item := range site.Items {
			sb.WriteString(fmt.Sprintf("  #%d %s — %s", i+1, item.Price, item.Title))
			if y, ok := item.Fields["year"]; ok {
				sb.WriteString(fmt.Sprintf(" | %s", y))
			}
			if km, ok := item.Fields["km"]; ok {
				sb.WriteString(fmt.Sprintf(" | %s km", km))
			}
			sb.WriteString("\n")
		}
	}

	return strings.TrimRight(sb.String(), "\n")
}

func truncateCollectURL(u string) string {
	return engine.TruncateURL(u, 60)
}
