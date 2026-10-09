package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

// Tab garbage collection. ghostchrome must not leave pages behind once an
// agent stops browsing:
//
//   - In a foreign Chrome it works in tabs it created itself (OwnTab or
//     AttachFresh) and closes only those.
//   - In a Chrome it owns (daemon, MCP), pages go back to about:blank after
//     PageIdleTimeout without activity, so no site keeps running JS or network
//     while the browser stays warm for the next command.

// DefaultPageIdleTimeout is how long an owned Chrome keeps idle pages loaded.
const DefaultPageIdleTimeout = 5 * time.Minute

// PageIdleTimeout parses GHOSTCHROME_PAGE_IDLE_TIMEOUT: empty means the 5m
// default, 0/off disables the stage, otherwise a Go duration or seconds.
func PageIdleTimeout() time.Duration {
	v := strings.TrimSpace(os.Getenv("GHOSTCHROME_PAGE_IDLE_TIMEOUT"))
	switch strings.ToLower(v) {
	case "":
		return DefaultPageIdleTimeout
	case "0", "off", "false", "no":
		return 0
	}
	if d, err := time.ParseDuration(v); err == nil && d > 0 {
		return d
	}
	var secs int
	if _, err := fmt.Sscanf(v, "%d", &secs); err == nil && secs > 0 {
		return time.Duration(secs) * time.Second
	}
	return DefaultPageIdleTimeout
}

// ownedPage returns this session's own tab in a foreign Chrome, creating it
// in a new window the first time. A remembered target is reused only if
// ghostchrome created it, so a tab id saved by an older version (one of the
// user's tabs) is never driven again.
func (b *Browser) ownedPage() (*rod.Page, error) {
	if b.state != nil && b.state.CurrentTargetID != "" && slices.Contains(b.state.OwnedTargetIDs, b.state.CurrentTargetID) {
		if p, err := b.browser.PageFromTarget(proto.TargetTargetID(b.state.CurrentTargetID)); err == nil {
			b.page = p
			b.maybeApplyProfile(p)
			return p, nil
		}
	}
	// A background tab is hidden: Chrome stops requestAnimationFrame there and
	// clicks waiting for a stable layout hang. A window of its own keeps the
	// tab visible without switching any of the user's tabs.
	p, err := b.browser.Page(proto.TargetCreateTarget{URL: "about:blank", NewWindow: true})
	if err != nil {
		return nil, err
	}
	b.page = p
	if b.state != nil {
		b.state.OwnedTargetIDs = pruneOwned(b.browser, append(b.state.OwnedTargetIDs, string(p.TargetID)))
		_ = b.setCurrentTargetID(p.TargetID)
	}
	b.maybeApplyProfile(p)
	return p, nil
}

// pruneOwned drops owned ids whose target no longer exists.
func pruneOwned(browser *rod.Browser, ids []string) []string {
	targets, err := proto.TargetGetTargets{}.Call(browser)
	if err != nil {
		return ids
	}
	live := map[string]bool{}
	for _, t := range targets.TargetInfos {
		live[string(t.TargetID)] = true
	}
	out := ids[:0]
	for _, id := range ids {
		if live[id] && !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	return out
}

// CloseOwnedTabs closes the tabs ghostchrome created in the foreign Chrome
// behind connectURL and forgets them. The user's own tabs and the browser are
// left alone. It returns how many tabs were closed.
func CloseOwnedTabs(connectURL string, timeout time.Duration) (int, error) {
	path, err := sessionStatePath(connectURL)
	if err != nil {
		return 0, err
	}
	state, err := loadSessionState(path)
	if err != nil {
		return 0, err
	}
	if len(state.OwnedTargetIDs) == 0 {
		return 0, nil
	}
	rb, err := connectRodBrowser(connectURL, timeout, 0, nil)
	if err != nil {
		return 0, err
	}
	closed := 0
	for _, id := range state.OwnedTargetIDs {
		if _, err := (proto.TargetCloseTarget{TargetID: proto.TargetTargetID(id)}).Call(rb); err == nil {
			closed++
		}
		delete(state.Snapshots, id)
		if state.CurrentTargetID == id {
			state.CurrentTargetID = ""
		}
	}
	state.OwnedTargetIDs = nil
	return closed, saveSessionState(path, state)
}

// BlankPages sends every page of a Chrome ghostchrome owns back to
// about:blank and closes the extra ones, keeping one empty tab. It talks to
// the DevTools HTTP endpoints only, so it opens no websocket and cannot close
// the browser. It returns how many pages it cleared (0 when all were blank).
func BlankPages(port int, timeout time.Duration) (int, error) {
	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	client := &http.Client{Timeout: timeout}
	pages, err := listPageTargets(client, base)
	if err != nil {
		return 0, err
	}
	loaded := 0
	for _, p := range pages {
		if p.URL != "about:blank" {
			loaded++
		}
	}
	if loaded == 0 && len(pages) <= 1 {
		return 0, nil
	}
	// Open the replacement first: Chrome exits when its last tab closes.
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPut, base+"/json/new?"+url.QueryEscape("about:blank"), nil)
	if err != nil {
		return 0, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	var fresh cdpTarget
	err = json.NewDecoder(resp.Body).Decode(&fresh)
	resp.Body.Close()
	if err != nil || resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("open blank tab: HTTP %d %v", resp.StatusCode, err)
	}
	cleared := 0
	for _, p := range pages {
		if p.ID == fresh.ID {
			continue
		}
		resp, err := client.Get(base + "/json/close/" + p.ID)
		if err != nil {
			continue
		}
		resp.Body.Close()
		cleared++
	}
	return cleared, nil
}

type cdpTarget struct {
	ID   string `json:"id"`
	Type string `json:"type"`
	URL  string `json:"url"`
}

func listPageTargets(client *http.Client, base string) ([]cdpTarget, error) {
	resp, err := client.Get(base + "/json/list")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var all []cdpTarget
	if err := json.NewDecoder(resp.Body).Decode(&all); err != nil {
		return nil, err
	}
	pages := all[:0]
	for _, t := range all {
		if t.Type == "page" {
			pages = append(pages, t)
		}
	}
	return pages, nil
}

// BlankPages is the in-process variant for a held Browser (MCP, JSONL agent):
// keep (the caller's current page, or the Browser's when nil) goes back to
// about:blank, other pages are closed, and the stored refs are dropped
// because they pointed at the old document.
func (b *Browser) BlankPages(keep *rod.Page) error {
	if b == nil || b.browser == nil {
		return nil
	}
	pages, err := b.browser.Pages()
	if err != nil {
		return err
	}
	if keep == nil {
		keep = b.page
	}
	if keep == nil && len(pages) > 0 {
		keep = pages[0]
	}
	for _, p := range pages {
		if keep != nil && p.TargetID == keep.TargetID {
			continue
		}
		_ = p.Close()
		_ = b.deleteSnapshot(p.TargetID)
	}
	if keep == nil {
		return nil
	}
	_ = b.deleteSnapshot(keep.TargetID)
	return keep.Timeout(10 * time.Second).Navigate("about:blank")
}
