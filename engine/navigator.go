package engine

import (
	"fmt"
	"time"

	"github.com/dev-toolings/ghostchrome/internal/core/policy"
	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

// ActivePolicy is the session-wide policy enforced on navigation and actions.
// Set by the CLI or MCP server at startup. Nil means no restrictions.
var ActivePolicy *policy.Policy

// PageInfo holds the result of a navigation.
type PageInfo struct {
	URL    string `json:"url"`
	Title  string `json:"title"`
	Status int    `json:"status"`
	TimeMs int64  `json:"time_ms"`
}

func lifecycleName(waitStrategy string) proto.PageLifecycleEventName {
	switch waitStrategy {
	case "load":
		return proto.PageLifecycleEventNameLoad
	case "idle", "networkidle":
		return proto.PageLifecycleEventNameNetworkIdle
	case "", "domcontentloaded", "dcl":
		return proto.PageLifecycleEventNameDOMContentLoaded
	default:
		return ""
	}
}

func armLifecycleWait(page *rod.Page, name proto.PageLifecycleEventName) func() {
	if page == nil || name == "" {
		return func() {}
	}
	_ = proto.PageSetLifecycleEventsEnabled{Enabled: true}.Call(page)
	frameID := page.FrameID
	wait := page.EachEvent(func(e *proto.PageLifecycleEvent) bool {
		if e == nil || e.Name != name {
			return false
		}
		// Ignore iframe/subframe lifecycle so a nested networkIdle cannot
		// complete a main-document wait the way Playwright would reject.
		return frameID == "" || e.FrameID == frameID
	})
	return wait
}

// Navigate goes to the given URL and returns page info.
//
// The lifecycle waiter is registered BEFORE page.Navigate so a fast load
// cannot outrun it. page.Navigate is used so Rod drops the stale JS context.
func Navigate(page *rod.Page, rawURL string, waitStrategy string) (*PageInfo, error) {
	if err := ActivePolicy.AllowURL(rawURL); err != nil {
		return nil, err
	}
	if err := ApplyInitScripts(page); err != nil {
		return nil, fmt.Errorf("init scripts: %w", err)
	}
	start := time.Now()
	requestTracker := newRequestTracker(page)
	requestTracker.listen(page)
	defer requestTracker.close()

	name := lifecycleName(waitStrategy)
	target := page
	if name != "" {
		target = page.Timeout(navTimeout(page))
	}
	wait := armLifecycleWait(target, name)

	if err := target.Navigate(rawURL); err != nil {
		return nil, err
	}

	if name != "" {
		wait()
	} else if err := WaitForPage(page, waitStrategy); err != nil {
		return nil, err
	}

	StartFileChooserIntercept(page)
	EnableDialogAutoAccept(page)

	info, err := page.Info()
	if err != nil {
		return nil, err
	}
	return &PageInfo{
		URL:    info.URL,
		Title:  info.Title,
		Status: requestTracker.MainDocumentStatus(),
		TimeMs: time.Since(start).Milliseconds(),
	}, nil
}

// HistoryStep runs back/forward with a pre-armed lifecycle wait.
func HistoryStep(page *rod.Page, delta int, waitStrategy string) error {
	if page == nil {
		return fmt.Errorf("page is nil")
	}
	name := lifecycleName(waitStrategy)
	target := page
	if name != "" {
		target = page.Timeout(navTimeout(page))
	}
	wait := armLifecycleWait(target, name)
	var err error
	if delta < 0 {
		err = target.NavigateBack()
	} else {
		err = target.NavigateForward()
	}
	if err != nil {
		return err
	}
	if name != "" {
		wait()
	}
	if name == "" {
		if err := WaitForPage(page, waitStrategy); err != nil {
			return err
		}
	}
	StartFileChooserIntercept(page)
	EnableDialogAutoAccept(page)
	return nil
}

// ReloadPage reloads the current document. The lifecycle waiter is armed
// before Reload so a cached/fast load cannot outrun WaitForPage.
func ReloadPage(page *rod.Page, waitStrategy string) error {
	if page == nil {
		return fmt.Errorf("page is nil")
	}
	name := lifecycleName(waitStrategy)
	target := page
	if name != "" {
		target = page.Timeout(navTimeout(page))
	}
	wait := armLifecycleWait(target, name)
	if err := target.Reload(); err != nil {
		return err
	}
	if name != "" {
		wait()
	}
	if name == "" {
		if err := WaitForPage(page, waitStrategy); err != nil {
			return err
		}
	}
	StartFileChooserIntercept(page)
	EnableDialogAutoAccept(page)
	return nil
}
