package artifact

import (
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

// RecorderOp is a single user-action op, ready to be written as JSONL.
// The schema matches the agentRequest format consumed by `ghostchrome agent`.
type RecorderOp struct {
	Op   string         `json:"op"`
	Args map[string]any `json:"args"`
}

// RecorderOpts configures a Recorder session.
type RecorderOpts struct {
	IncludeScrolls bool
	IncludeHovers  bool
	StopOnNavigate bool // stop recording after first user-triggered navigation
}

// Recorder injects a JS listener into the page, coalesces typing, and emits
// JSONL ops to w. It uses Runtime.addBinding so the JS → Go channel is
// clean (no console-log pollution).
//
// Call Start() once then either Wait() (blocks until done) or Stop().
type Recorder struct {
	page *rod.Page
	opts RecorderOpts
	w    io.Writer
	mu   sync.Mutex
	enc  *json.Encoder

	stopCDP   func()
	done      chan struct{}
	closeOnce sync.Once

	// typing coalescer state
	typeTimers  map[string]*time.Timer // key: stable selector
	typePending map[string]string      // key: stable selector → accumulated text
	typeMu      sync.Mutex

	lastURL string
}

// bindingName is the CDP binding exposed to JS.
const recorderBinding = "__gcRec"

// NewRecorder creates a recorder but does not start it.
func NewRecorder(page *rod.Page, w io.Writer, opts RecorderOpts) *Recorder {
	return &Recorder{
		page:        page,
		opts:        opts,
		w:           w,
		enc:         json.NewEncoder(w),
		done:        make(chan struct{}),
		typeTimers:  map[string]*time.Timer{},
		typePending: map[string]string{},
	}
}

// Start registers the CDP binding, injects the JS listener, and begins
// listening for user events. It is safe to call once.
func (r *Recorder) Start() error {
	// Capture initial URL for navigation dedup.
	if info, err := r.page.Info(); err == nil && info != nil {
		r.lastURL = info.URL
	}

	// Register the binding in the browser target so it's available in the page.
	// Note: this — and the RuntimeBindingCalled subscription below — auto-enable
	// the Runtime CDP domain (residual, known: the recorder is opt-in, so this
	// is acceptable unlike the always-on background observer).
	if err := (proto.RuntimeAddBinding{Name: recorderBinding}).Call(r.page); err != nil {
		return fmt.Errorf("recorder: addBinding: %w", err)
	}

	// Inject the listener script and expose it as a page script so it survives
	// navigation (addScriptToEvaluateOnNewDocument).
	if err := r.injectListenerScript(); err != nil {
		return fmt.Errorf("recorder: inject script: %w", err)
	}

	// Listen for binding calls from JS.
	r.stopCDP = r.page.EachEvent(
		func(e *proto.RuntimeBindingCalled) bool {
			if e.Name != recorderBinding {
				return false
			}
			r.handleBinding(e.Payload)
			return false
		},
		func(e *proto.PageFrameNavigated) bool {
			if e.Frame.ParentID != "" {
				return false // ignore subframes
			}
			r.handleFrameNavigated(e.Frame.URL)
			return false
		},
	)

	return nil
}

// Stop tears down the recorder and flushes any pending typed text.
func (r *Recorder) Stop() {
	r.closeOnce.Do(func() {
		r.flushAllTyping()
		if r.stopCDP != nil {
			r.stopCDP()
		}
		close(r.done)
	})
}

// Done returns a channel closed when the recorder stops.
func (r *Recorder) Done() <-chan struct{} {
	return r.done
}

// injectListenerScript registers a JS listener that posts events to Go via the
// CDP binding. Uses event delegation (one listener per event type on document).
func (r *Recorder) injectListenerScript() error {
	js := buildListenerJS(recorderBinding, r.opts.IncludeScrolls, r.opts.IncludeHovers)

	// Evaluate immediately in the current page.
	if _, err := r.page.Eval(js); err != nil {
		return err
	}

	// Also register for new documents (navigations).
	_, err := proto.PageAddScriptToEvaluateOnNewDocument{Source: js}.Call(r.page)
	return err
}

// buildListenerJS returns the JS string for the injected listener.
// It is a self-contained IIFE, kept small with event delegation.
func buildListenerJS(binding string, includeScrolls, includeHovers bool) string {
	scrollListener := ""
	if includeScrolls {
		scrollListener = fmt.Sprintf(`
  document.addEventListener('scroll', function(e) {
    window[%q]('{"t":"scroll","dy":' + Math.round(window.scrollY) + '}');
  }, {capture:true, passive:true});`, binding)
	}

	hoverListener := ""
	if includeHovers {
		hoverListener = fmt.Sprintf(`
  document.addEventListener('mouseover', function(e) {
    var el = e.target;
    var sel = gcStableSelector(el);
    if (sel) window[%q](JSON.stringify({t:'hover',sel:sel}));
  }, {capture:true, passive:true});`, binding)
	}

	return fmt.Sprintf(`(function() {
  if (window.__gcRecInstalled) return;
  window.__gcRecInstalled = true;

  function gcStableSelector(el) {
    if (!el || el === document) return null;
    if (el.id) return '#' + el.id;
    var tag = el.tagName.toLowerCase();
    var name = el.getAttribute('name');
    if (name) return tag + '[name="' + name + '"]';
    var ph = el.getAttribute('placeholder');
    if (ph) return tag + '[placeholder="' + ph + '"]';
    var aria = el.getAttribute('aria-label');
    if (aria) return tag + '[aria-label="' + aria + '"]';
    var txt = (el.textContent || '').trim().slice(0, 40);
    if (txt) return tag + ':text("' + txt + '")';
    return tag;
  }

  function send(obj) {
    try { window[%q](JSON.stringify(obj)); } catch(e) {}
  }

  // click
  document.addEventListener('click', function(e) {
    var el = e.target;
    var sel = gcStableSelector(el);
    send({t:'click',sel:sel,txt:(el.textContent||'').trim().slice(0,60)});
  }, true);

  // change / input coalescing: we emit 'input' to Go, which debounces
  document.addEventListener('input', function(e) {
    var el = e.target;
    var tag = el.tagName.toLowerCase();
    if (tag !== 'input' && tag !== 'textarea') return;
    var sel = gcStableSelector(el);
    send({t:'input',sel:sel,val:el.value});
  }, true);

  // change for select elements
  document.addEventListener('change', function(e) {
    var el = e.target;
    if (el.tagName.toLowerCase() === 'select') {
      var sel = gcStableSelector(el);
      var val = el.value;
      send({t:'select',sel:sel,val:val});
    }
  }, true);

  // keydown: only meaningful keys
  var meaningfulKeys = {Enter:1,Tab:1,Escape:1};
  document.addEventListener('keydown', function(e) {
    if (!meaningfulKeys[e.key]) return;
    send({t:'key',key:e.key});
  }, true);

  // submit (form submit → Enter equivalent at form level)
  document.addEventListener('submit', function(e) {
    send({t:'submit'});
  }, true);
%s%s
})();`, binding, scrollListener, hoverListener)
}

// jsEvent is the shape posted by the JS listener via the binding.
type jsEvent struct {
	T   string `json:"t"`
	Sel string `json:"sel,omitempty"`
	Txt string `json:"txt,omitempty"`
	Val string `json:"val,omitempty"`
	Key string `json:"key,omitempty"`
	Dy  int    `json:"dy,omitempty"`
}

func (r *Recorder) handleBinding(payload string) {
	var ev jsEvent
	if err := json.Unmarshal([]byte(payload), &ev); err != nil {
		return
	}

	switch ev.T {
	case "click":
		r.flushTypingFor("") // flush any pending type before click
		r.emit(RecorderOp{Op: "click", Args: map[string]any{
			"target": ev.Sel,
			"byText": ev.Txt,
		}})

	case "input":
		r.coalesceType(ev.Sel, ev.Val)

	case "select":
		r.emit(RecorderOp{Op: "select", Args: map[string]any{
			"target": ev.Sel,
			"value":  ev.Val,
		}})

	case "key":
		r.flushTypingFor("")
		r.emit(RecorderOp{Op: "press", Args: map[string]any{
			"key": ev.Key,
		}})

	case "submit":
		r.flushTypingFor("")

	case "scroll":
		r.emit(RecorderOp{Op: "scroll_by", Args: map[string]any{
			"dy": ev.Dy,
		}})

	case "hover":
		r.emit(RecorderOp{Op: "hover", Args: map[string]any{
			"target": ev.Sel,
		}})
	}
}

func (r *Recorder) handleFrameNavigated(url string) {
	r.mu.Lock()
	last := r.lastURL
	r.mu.Unlock()

	if url == last {
		return
	}

	r.mu.Lock()
	r.lastURL = url
	r.mu.Unlock()

	r.flushAllTyping()
	r.emit(RecorderOp{Op: "navigate", Args: map[string]any{
		"url": url,
	}})
}

// coalesceType debounces typing events per input field.
// After 300ms of no input activity the accumulated value is emitted as a
// single `type` op. On subsequent inputs for the same field the timer resets.
func (r *Recorder) coalesceType(sel, val string) {
	r.typeMu.Lock()
	defer r.typeMu.Unlock()

	r.typePending[sel] = val

	if t, ok := r.typeTimers[sel]; ok {
		t.Stop()
	}
	r.typeTimers[sel] = time.AfterFunc(300*time.Millisecond, func() {
		r.flushTypingFor(sel)
	})
}

// flushTypingFor emits the pending type op for sel and removes its timer.
// If sel is empty it flushes all pending.
func (r *Recorder) flushTypingFor(sel string) {
	r.typeMu.Lock()
	defer r.typeMu.Unlock()

	if sel == "" {
		for k := range r.typePending {
			r.emitType(k)
		}
		return
	}
	r.emitType(sel)
}

func (r *Recorder) flushAllTyping() {
	r.typeMu.Lock()
	defer r.typeMu.Unlock()

	for k := range r.typePending {
		r.emitType(k)
	}
}

// emitType emits a type op and removes the pending entry. Must be called
// with typeMu held.
func (r *Recorder) emitType(sel string) {
	val, ok := r.typePending[sel]
	if !ok {
		return
	}
	delete(r.typePending, sel)
	if t, ok := r.typeTimers[sel]; ok {
		t.Stop()
		delete(r.typeTimers, sel)
	}
	// emit without holding typeMu to avoid deadlock
	r.typeMu.Unlock()
	r.emit(RecorderOp{Op: "type", Args: map[string]any{
		"target": sel,
		"text":   val,
	}})
	r.typeMu.Lock()
}

func (r *Recorder) emit(op RecorderOp) {
	r.mu.Lock()
	defer r.mu.Unlock()
	_ = r.enc.Encode(op)
}
