package engine

import (
	"context"
	"sync"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

var (
	pageHubs  sync.Map // *rod.Page -> *EventHub
	pageHubMu sync.Mutex
)

// EventHub is the single CDP event subscriber for a page session.
// CLI/JSONL/MCP should reuse this instead of stacking EachEvent loops.
type EventHub struct {
	page   *rod.Page
	obs    *Observer
	mu     sync.Mutex
	cancel context.CancelFunc
}

// StartEventHub enables Network/Page (and Runtime unless evasion is on)
// and keeps a bounded Drain ring. The live channel is drained so
// producers never block. Safe to call repeatedly for the same page.
func StartEventHub(page *rod.Page) (*EventHub, error) {
	if page == nil {
		return nil, nil
	}
	pageHubMu.Lock()
	defer pageHubMu.Unlock()
	if existing := hubForPageLocked(page); existing != nil {
		return existing, nil
	}
	obs := NewObserver(page, ObserverOpts{BufferSize: 256, History: 1000})
	ctx, cancel := context.WithCancel(context.Background())
	if err := obs.Start(ctx); err != nil {
		cancel()
		return nil, err
	}
	hub := &EventHub{page: page, obs: obs, cancel: cancel}
	go func() {
		for range obs.Events() {
		}
	}()
	pageHubs.Store(page, hub)
	return hub, nil
}

func hubForPageLocked(page *rod.Page) *EventHub {
	if v, ok := pageHubs.Load(page); ok {
		if hub, _ := v.(*EventHub); hub != nil {
			return hub
		}
	}
	return nil
}

// HubForPage returns the EventHub registered for page, if any.
func HubForPage(page *rod.Page) *EventHub {
	if page == nil {
		return nil
	}
	if v, ok := pageHubs.Load(page); ok {
		if hub, _ := v.(*EventHub); hub != nil {
			return hub
		}
	}
	return nil
}

func (h *EventHub) Observer() *Observer {
	if h == nil {
		return nil
	}
	return h.obs
}

func (h *EventHub) Drain(sinceMS int64) []ObserverEvent {
	if h == nil || h.obs == nil {
		return nil
	}
	return h.obs.Drain(sinceMS)
}

// WaitLifecycle blocks until a page lifecycle event matching name/frame/loader
// is observed. An empty frame or loader matches any.
func (h *EventHub) WaitLifecycle(name proto.PageLifecycleEventName, frame proto.PageFrameID, loader proto.NetworkLoaderID, timeout time.Duration) error {
	return h.WaitLifecycleSince(name, frame, loader, timeout, time.Now().UnixMilli()-250)
}

func (h *EventHub) WaitLifecycleSince(name proto.PageLifecycleEventName, frame proto.PageFrameID, loader proto.NetworkLoaderID, timeout time.Duration, sinceMS int64) error {
	if h == nil || h.obs == nil {
		return context.DeadlineExceeded
	}
	if timeout <= 0 {
		timeout = NavWaitTimeout
	}
	if sinceMS <= 0 {
		sinceMS = time.Now().UnixMilli() - 250
	}
	deadline := time.Now().Add(timeout)
	wantEvent := string(name)
	wantFrame := string(frame)
	wantLoader := string(loader)
	for {
		for _, e := range h.Drain(sinceMS) {
			if e.Kind != KindPage || e.Event != wantEvent {
				continue
			}
			if wantFrame != "" && e.Frame != wantFrame {
				continue
			}
			if wantLoader != "" && e.Loader != wantLoader {
				continue
			}
			return nil
		}
		if !time.Now().Before(deadline) {
			return context.DeadlineExceeded
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func (h *EventHub) Stop() {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.page != nil {
		pageHubs.Delete(h.page)
	}
	if h.obs != nil {
		_ = h.obs.Stop()
		h.obs = nil
	}
	if h.cancel != nil {
		h.cancel()
		h.cancel = nil
	}
}

// stopBrowserEventHubs releases every hub attached to a browser. Runtime and
// MCP normally stop their current hub explicitly, but Browser.Close is also a
// public ownership boundary and must not leave page-keyed hubs retained when
// callers close the browser directly.
func stopBrowserEventHubs(browser *rod.Browser) {
	if browser == nil {
		return
	}
	pageHubs.Range(func(_, value interface{}) bool {
		hub, ok := value.(*EventHub)
		if !ok || hub == nil || hub.page == nil || hub.page.Browser() != browser {
			return true
		}
		hub.Stop()
		return true
	})
}
