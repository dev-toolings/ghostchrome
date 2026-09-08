package engine

import (
	"context"
	"os"
	"sync"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

const popupWatchCap = 32

type popupEvt struct {
	seq    uint64
	opener proto.TargetTargetID
	target proto.TargetTargetID
}

type popupWatch struct {
	browser   *rod.Browser
	mu        sync.Mutex
	seq       uint64
	events    []popupEvt
	downloads uint64
	stop      context.CancelFunc
}

var popupWatchers sync.Map // *rod.Browser -> *popupWatch

var clickPopupHints sync.Map // proto.TargetTargetID -> bool

var clickNavHints sync.Map // proto.TargetTargetID -> bool

var clickDownloadHints sync.Map // proto.TargetTargetID -> bool

func watcherFor(browser *rod.Browser) *popupWatch {
	if browser == nil {
		return nil
	}
	if v, ok := popupWatchers.Load(browser); ok {
		if w, _ := v.(*popupWatch); w != nil {
			return w
		}
	}
	w := startPopupWatch(browser)
	actual, loaded := popupWatchers.LoadOrStore(browser, w)
	if loaded {
		w.Stop()
		if existing, _ := actual.(*popupWatch); existing != nil {
			return existing
		}
	}
	return w
}

func startPopupWatch(browser *rod.Browser) *popupWatch {
	w := &popupWatch{browser: browser}
	ctx, cancel := context.WithCancel(context.Background())
	w.stop = cancel
	_ = proto.BrowserSetDownloadBehavior{
		Behavior:      proto.BrowserSetDownloadBehaviorBehaviorAllowAndName,
		DownloadPath:  os.TempDir(),
		EventsEnabled: true,
	}.Call(browser)
	go w.loop(ctx)
	return w
}

func (w *popupWatch) loop(ctx context.Context) {
	if w == nil || w.browser == nil {
		return
	}
	events := w.browser.Context(ctx).Event()
	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-events:
			if !ok || msg == nil {
				return
			}
			switch msg.Method {
			case (proto.TargetTargetCreated{}).ProtoEvent():
				e := &proto.TargetTargetCreated{}
				if !msg.Load(e) || e.TargetInfo == nil || e.TargetInfo.OpenerID == "" {
					continue
				}
				switch e.TargetInfo.Type {
				case proto.TargetTargetInfoTypeBackgroundPage, proto.TargetTargetInfoTypeServiceWorker, proto.TargetTargetInfoTypeSharedWorker, proto.TargetTargetInfoTypeBrowser:
					continue
				}
				w.push(e.TargetInfo.OpenerID, e.TargetInfo.TargetID)
			case (proto.BrowserDownloadWillBegin{}).ProtoEvent(), (proto.PageDownloadWillBegin{}).ProtoEvent():
				w.noteDownload()
			}
		}
	}
}

func (w *popupWatch) Stop() {
	if w == nil {
		return
	}
	if w.stop != nil {
		w.stop()
		w.stop = nil
	}
	if w.browser != nil {
		popupWatchers.Delete(w.browser)
	}
}

func (w *popupWatch) push(opener, target proto.TargetTargetID) {
	if w == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.seq++
	w.events = append(w.events, popupEvt{seq: w.seq, opener: opener, target: target})
	if len(w.events) > popupWatchCap {
		w.events = append([]popupEvt(nil), w.events[len(w.events)-popupWatchCap:]...)
	}
}

func (w *popupWatch) mark() uint64 {
	if w == nil {
		return 0
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.seq
}

func (w *popupWatch) take(opener proto.TargetTargetID, mark uint64) proto.TargetTargetID {
	if w == nil {
		return ""
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	for i, e := range w.events {
		if e.seq <= mark || e.opener != opener {
			continue
		}
		w.events = append(w.events[:i], w.events[i+1:]...)
		return e.target
	}
	return ""
}

func (w *popupWatch) noteDownload() {
	if w == nil {
		return
	}
	w.mu.Lock()
	w.downloads++
	w.mu.Unlock()
}

func (w *popupWatch) downloadMark() uint64 {
	if w == nil {
		return 0
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.downloads
}

// StopPopupWatch cancels the Target.targetCreated subscriber for browser.
func StopPopupWatch(browser *rod.Browser) {
	if browser == nil {
		return
	}
	if v, ok := popupWatchers.Load(browser); ok {
		if w, _ := v.(*popupWatch); w != nil {
			w.Stop()
		}
	}
}

// PopupMark snapshots the popup-event cursor for page's browser. Call this
// before a click so TakePopup can see targets created during the action.
func PopupMark(page *rod.Page) uint64 {
	if page == nil {
		return 0
	}
	return watcherFor(page.Browser()).mark()
}

// TakePopup returns a page opened by page since mark. wait=0 is a non-blocking
// peek so the button click hot path does not sleep.
func TakePopup(page *rod.Page, mark uint64, wait time.Duration) *rod.Page {
	if page == nil {
		return nil
	}
	w := watcherFor(page.Browser())
	if w == nil {
		return nil
	}
	deadline := time.Now().Add(wait)
	for {
		if id := w.take(page.TargetID, mark); id != "" {
			popup, err := w.browser.PageFromTarget(id)
			if err == nil {
				return popup
			}
		}
		if wait <= 0 || !time.Now().Before(deadline) {
			return nil
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func setClickPopupHint(page *rod.Page, mayPopup bool) {
	if page == nil {
		return
	}
	clickPopupHints.Store(page.TargetID, mayPopup)
}

// ClickPopupHint reports whether the last click actionability probe on page
// saw target=_blank or javascript:window.open on the live element.
func ClickPopupHint(page *rod.Page) bool {
	if page == nil {
		return false
	}
	v, ok := clickPopupHints.Load(page.TargetID)
	return ok && v.(bool)
}

func setClickNavHint(page *rod.Page, mayNav bool) {
	if page == nil {
		return
	}
	clickNavHints.Store(page.TargetID, mayNav)
}

// ClickNavHint reports whether the last actionability probe saw an in-page
// navigation (same-tab link or form submit), not a button that stays put.
func ClickNavHint(page *rod.Page) bool {
	if page == nil {
		return false
	}
	v, ok := clickNavHints.Load(page.TargetID)
	return ok && v.(bool)
}

func setClickDownloadHint(page *rod.Page, mayDownload bool) {
	if page == nil {
		return
	}
	clickDownloadHints.Store(page.TargetID, mayDownload)
}

func ClickDownloadHint(page *rod.Page) bool {
	if page == nil {
		return false
	}
	v, ok := clickDownloadHints.Load(page.TargetID)
	return ok && v.(bool)
}

// WaitForClickDownload peeks for Browser.downloadWillBegin after a click that
// looked like a file download. Button clicks skip this entirely.
func WaitForClickDownload(page *rod.Page) {
	if page == nil || !ClickDownloadHint(page) {
		return
	}
	w := watcherFor(page.Browser())
	if w == nil {
		return
	}
	mark := w.downloadMark()
	deadline := time.Now().Add(250 * time.Millisecond)
	for time.Now().Before(deadline) {
		if w.downloadMark() > mark {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// WaitForClickNavigation waits for a same-tab navigation after a click that
// looked like a link or form submit. Button clicks return immediately.
func WaitForClickNavigation(page *rod.Page) {
	if page == nil || !ClickNavHint(page) {
		return
	}
	timeout := 2 * time.Second
	if remain := navTimeout(page); remain > 0 && remain < timeout {
		timeout = remain
	}
	since := time.Now().UnixMilli() - 50
	if hub := HubForPage(page); hub != nil {
		peek := 250 * time.Millisecond
		if peek > timeout {
			peek = timeout
		}
		kind, frame := waitFrameNavigated(hub, page, since, peek)
		if kind == navKindDocument {
			_ = hub.WaitLifecycleSince(proto.PageLifecycleEventNameDOMContentLoaded, proto.PageFrameID(frame), "", time.Second, since)
			StartFileChooserIntercept(page)
		}
		return
	}
	before := ""
	if info, err := page.Info(); err == nil && info != nil {
		before = info.URL
	}
	peek := 250 * time.Millisecond
	if peek > timeout {
		peek = timeout
	}
	deadline := time.Now().Add(peek)
	for time.Now().Before(deadline) {
		info, err := page.Info()
		if err == nil && info != nil && info.URL != "" && info.URL != before {
			_ = WaitForPage(page.Timeout(time.Until(deadline)), "domcontentloaded")
			StartFileChooserIntercept(page)
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
}

type navKind int

const (
	navKindNone navKind = iota
	navKindDocument
	navKindSameDocument
)

func waitFrameNavigated(hub *EventHub, page *rod.Page, sinceMS int64, timeout time.Duration) (navKind, string) {
	if hub == nil || page == nil {
		return navKindNone, ""
	}
	deadline := time.Now().Add(timeout)
	for {
		for _, e := range hub.Drain(sinceMS) {
			if e.Kind != KindPage {
				continue
			}
			switch e.Event {
			case "frameNavigated":
				return navKindDocument, e.Frame
			case "navigatedWithinDocument":
				return navKindSameDocument, e.Frame
			}
		}
		if !time.Now().Before(deadline) {
			return navKindNone, ""
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func popupWaitAfterClick(page *rod.Page, snapshot *PageSnapshot, ref string) time.Duration {
	if ClickPopupHint(page) || RefMayOpenPopup(snapshot, ref) {
		return 250 * time.Millisecond
	}
	return 0
}

// AdoptClickPopup waits (briefly, only when the last click looked like a popup)
// for a Target.targetCreated opened by page since mark. Button clicks peek
// without sleeping.
func AdoptClickPopup(page *rod.Page, mark uint64, snapshot *PageSnapshot, ref string) *rod.Page {
	return TakePopup(page, mark, popupWaitAfterClick(page, snapshot, ref))
}
