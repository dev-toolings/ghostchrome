package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

// ObserverKind classifies an ObserverEvent.
type ObserverKind string

const (
	KindNet     ObserverKind = "net"
	KindConsole ObserverKind = "console"
	KindError   ObserverKind = "error"
	KindPage    ObserverKind = "page"
)

// ObserverEvent is one emitted CDP event in a unified format.
type ObserverEvent struct {
	TS         int64             `json:"ts"` // unix ms
	Kind       ObserverKind      `json:"kind"`
	Method     string            `json:"method,omitempty"`
	URL        string            `json:"url,omitempty"`
	Status     int               `json:"status,omitempty"`
	MimeType   string            `json:"mime_type,omitempty"`
	Type       string            `json:"type,omitempty"`
	RequestID  string            `json:"request_id,omitempty"`
	Body       string            `json:"body,omitempty"`
	BodyBase64 bool              `json:"body_base64,omitempty"`
	BodyError  string            `json:"body_error,omitempty"`
	Size       int64             `json:"size,omitempty"`
	DurationMs int64             `json:"duration_ms,omitempty"`
	Failed     string            `json:"failed,omitempty"`
	Level      string            `json:"level,omitempty"`
	Text       string            `json:"text,omitempty"`
	Source     string            `json:"source,omitempty"`
	Event      string            `json:"event,omitempty"`
	Frame      string            `json:"frame,omitempty"`
	Loader     string            `json:"loader,omitempty"`
	ReqHeaders map[string]string `json:"request_headers,omitempty"`
	ResHeaders map[string]string `json:"response_headers,omitempty"`
	PostData   string            `json:"post_data,omitempty"`
}

// ObserverFilters selectively narrows which events are emitted.
type ObserverFilters struct {
	// NetTypes filters by network resource type (e.g. "xhr", "fetch", "document").
	// Empty means accept all.
	NetTypes []string
	// NetURLRegex filters net events by URL. Nil means accept all.
	NetURLRegex *regexp.Regexp
	// ConsoleLevels filters console events (e.g. "error", "warning", "info").
	// Empty means accept all.
	ConsoleLevels []string
	// Kinds limits event kinds emitted. Empty means accept all.
	Kinds []ObserverKind
}

// ObserverOpts configures an Observer.
type ObserverOpts struct {
	Filters    ObserverFilters
	MaxEvents  int           // 0 = unlimited
	Duration   time.Duration // 0 = unlimited
	BufferSize int           // channel buffer; default 256
	History    int           // Drain ring size; 0 = default 1000
}

// ObserverStats holds counters for each kind.
type ObserverStats struct {
	Net     int
	Console int
	Error   int
	Page    int
	Total   int
	Dropped int // events discarded due to MaxEvents or channel full
}

// pendingNet tracks in-flight network requests between requestWillBeSent and loadingFinished/Failed.
type pendingNet struct {
	method     string
	url        string
	resType    string
	frameID    string
	startedAt  float64
	status     int
	mimeType   string
	requestID  string
	reqHeaders map[string]string
	resHeaders map[string]string
	postData   string
	body       string
	bodyBase64 bool
	bodyError  string
}

// Observer multiplexes CDP Network + Runtime + Page events into a unified channel.
type Observer struct {
	page   *rod.Page
	opts   ObserverOpts
	events chan ObserverEvent

	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu      sync.Mutex
	pending map[proto.NetworkRequestID]*pendingNet
	allEvts []ObserverEvent // bounded ring for Drain
	stats   ObserverStats
	closing bool

	stopOnce sync.Once
	stopped  chan struct{}
}

// NewObserver creates an Observer for page. Call Start to begin listening.
func NewObserver(page *rod.Page, opts ObserverOpts) *Observer {
	if opts.BufferSize <= 0 {
		opts.BufferSize = 256
	}
	if opts.History <= 0 {
		opts.History = 1000
	}
	return &Observer{
		page:    page,
		opts:    opts,
		events:  make(chan ObserverEvent, opts.BufferSize),
		pending: make(map[proto.NetworkRequestID]*pendingNet),
		stopped: make(chan struct{}),
	}
}

// Start enables CDP domains and begins listening. It must be called once.
func (o *Observer) Start(ctx context.Context) error {
	// Network domain must be explicitly enabled; Rod does not auto-enable it
	// when using EachEvent. Runtime and Page are enabled automatically by Rod.
	if err := (proto.NetworkEnable{}).Call(o.page); err != nil {
		return fmt.Errorf("network enable: %w", err)
	}
	_ = proto.PageSetLifecycleEventsEnabled{Enabled: true}.Call(o.page)
	// Log domain carries browser-side messages that Runtime.consoleAPICalled
	// does NOT surface: CORS errors, mixed content, CSP violations, deprecated
	// API warnings, network failures (ERR_*), cookie SameSite warnings, etc.
	// These show up in DevTools Console but come from the Log domain. Failure
	// to enable is non-fatal: keep the runtime listeners working.
	_ = (proto.LogEnable{}).Call(o.page)

	// Derive a scoped context so we can cancel all goroutines on Stop.
	ctx, o.cancel = context.WithCancel(ctx)

	if o.opts.Duration > 0 {
		ctx, o.cancel = context.WithTimeout(ctx, o.opts.Duration)
	}

	o.wg.Add(1)
	go o.listenNetwork(ctx)

	// Runtime.consoleAPICalled auto-sends `Runtime.enable` (a DataDome
	// automation tell). Skip it under evasion; the network/log/page listeners
	// use other CDP domains and stay active.
	if !EvadeRuntimeEnable() {
		o.wg.Add(1)
		go o.listenConsole(ctx)
	}

	o.wg.Add(1)
	go o.listenLog(ctx)

	o.wg.Add(1)
	go o.listenPage(ctx)

	// Closer goroutine: wait for all listeners then close the events channel.
	go o.waitAndClose()

	return nil
}

func (o *Observer) waitAndClose() {
	o.wg.Wait()
	o.mu.Lock()
	o.closing = true
	o.stopOnce.Do(func() { close(o.events) })
	o.mu.Unlock()
	close(o.stopped)
}

// Events returns the read-only event channel.
func (o *Observer) Events() <-chan ObserverEvent {
	return o.events
}

// Stop cancels listening, waits for goroutines, and closes the channel.
// Idempotent.
func (o *Observer) Stop() error {
	o.mu.Lock()
	o.closing = true
	o.mu.Unlock()

	if o.cancel != nil {
		o.cancel()
	}
	<-o.stopped
	return nil
}

// Stats returns a snapshot of event counters.
func (o *Observer) Stats() ObserverStats {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.stats
}

// Drain returns all events whose TS >= afterMs without removing them.
func (o *Observer) Drain(afterMs int64) []ObserverEvent {
	o.mu.Lock()
	defer o.mu.Unlock()
	var out []ObserverEvent
	for _, e := range o.allEvts {
		if e.TS >= afterMs {
			out = append(out, e)
		}
	}
	return out
}

// WriteNDJSON streams events from the channel to w as NDJSON until ctx is done
// or the channel is closed. It flushes after every line if w implements
// http.Flusher or has a Flush() method.
func (o *Observer) WriteNDJSON(ctx context.Context, w io.Writer) error {
	enc := json.NewEncoder(w)
	type flusher interface{ Flush() }
	fl, _ := w.(flusher)
	for {
		select {
		case <-ctx.Done():
			return nil
		case evt, ok := <-o.events:
			if !ok {
				return nil
			}
			if err := enc.Encode(evt); err != nil {
				return err
			}
			if fl != nil {
				fl.Flush()
			}
		}
	}
}

// emit sends an event to the channel and records it in the history.
// Returns false if the event was dropped (channel full).
func (o *Observer) emit(evt ObserverEvent) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closing {
		return false
	}
	// Kind filter
	if !o.acceptKind(evt.Kind) {
		return true
	}
	// MaxEvents enforcement
	if o.opts.MaxEvents > 0 && o.stats.Total >= o.opts.MaxEvents {
		o.stats.Dropped++
		// Signal stop by cancelling context
		if o.cancel != nil {
			o.cancel()
		}
		return false
	}
	// Record in history
	o.allEvts = append(o.allEvts, evt)
	if limit := o.historyLimit(); limit > 0 && len(o.allEvts) > limit {
		o.allEvts = append([]ObserverEvent(nil), o.allEvts[len(o.allEvts)-limit:]...)
	}
	// Update stats
	switch evt.Kind {
	case KindNet:
		o.stats.Net++
	case KindConsole:
		o.stats.Console++
	case KindError:
		o.stats.Error++
	case KindPage:
		o.stats.Page++
	}
	o.stats.Total++

	select {
	case o.events <- evt:
		return true
	default:
		o.stats.Dropped++
		return false
	}
}

func (o *Observer) acceptKind(k ObserverKind) bool {
	if len(o.opts.Filters.Kinds) == 0 {
		return true
	}
	for _, fk := range o.opts.Filters.Kinds {
		if fk == k {
			return true
		}
	}
	return false
}

func (o *Observer) historyLimit() int {
	if o.opts.History > 0 {
		return o.opts.History
	}
	return 1000
}

func (o *Observer) acceptNetType(t string) bool {
	if len(o.opts.Filters.NetTypes) == 0 {
		return true
	}
	// CDP emits resource types CamelCased ("Document", "Fetch", "XHR").
	// Filter inputs come from the CLI lowercased; match case-insensitively
	// so `--filter-net=doc,xhr` works without leaking CDP casing.
	target := strings.ToLower(t)
	for _, ft := range o.opts.Filters.NetTypes {
		f := strings.ToLower(ft)
		if f == target {
			return true
		}
		// Friendly short aliases.
		if f == "doc" && target == "document" {
			return true
		}
	}
	return false
}

func (o *Observer) acceptNetURL(u string) bool {
	if o.opts.Filters.NetURLRegex == nil {
		return true
	}
	return o.opts.Filters.NetURLRegex.MatchString(u)
}

func getResponseBody(page *rod.Page, requestID proto.NetworkRequestID) (string, bool, error) {
	if page == nil || requestID == "" {
		return "", false, nil
	}
	resp, err := proto.NetworkGetResponseBody{RequestID: requestID}.Call(page)
	if err != nil {
		return "", false, err
	}
	return resp.Body, resp.Base64Encoded, nil
}

func cloneHeaders(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func redirectNetEvent(
	requestID proto.NetworkRequestID,
	e *proto.NetworkRequestWillBeSent,
	p *pendingNet,
) (ObserverEvent, bool) {
	if e == nil || e.RedirectResponse == nil {
		return ObserverEvent{}, false
	}

	url := e.RedirectResponse.URL
	if url == "" {
		if p != nil && p.url != "" {
			url = p.url
		} else {
			url = e.Request.URL
		}
	}
	if url == "" {
		return ObserverEvent{}, false
	}

	method := firstNonEmpty(e.Request.Method, "")
	resType := string(e.Type)
	frameID := string(e.FrameID)
	evt := ObserverEvent{
		TS:         time.Now().UnixMilli(),
		Kind:       KindNet,
		Method:     method,
		URL:        url,
		Status:     e.RedirectResponse.Status,
		MimeType:   e.RedirectResponse.MIMEType,
		Type:       resType,
		RequestID:  string(requestID),
		ResHeaders: cloneHeaders(flattenHeaders(e.RedirectResponse.Headers)),
		Frame:      frameID,
	}
	if p != nil {
		method = firstNonEmpty(p.method, method)
		resType = firstNonEmpty(p.resType, resType)
		frameID = firstNonEmpty(p.frameID, frameID)
		evt.Method = method
		evt.Type = resType
		evt.Frame = frameID
		evt.ReqHeaders = cloneHeaders(p.reqHeaders)
		if p.startedAt > 0 {
			evt.DurationMs = int64((float64(e.Timestamp) - p.startedAt) * 1000)
		}
	} else {
		evt.ReqHeaders = cloneHeaders(flattenHeaders(e.Request.Headers))
	}

	if evt.DurationMs < 0 {
		evt.DurationMs = 0
	}
	return evt, true
}

func (o *Observer) acceptConsoleLevel(l string) bool {
	if len(o.opts.Filters.ConsoleLevels) == 0 {
		return true
	}
	for _, fl := range o.opts.Filters.ConsoleLevels {
		if fl == l {
			return true
		}
	}
	return false
}

// listenNetwork subscribes to the four Network.* events.
func (o *Observer) listenNetwork(ctx context.Context) {
	defer o.wg.Done()
	scoped, cancel := o.page.WithCancel()
	defer cancel()

	stop := scoped.EachEvent(
		func(e *proto.NetworkRequestWillBeSent) {
			o.mu.Lock()
			p := o.pending[e.RequestID]
			if p == nil {
				p = &pendingNet{}
				o.pending[e.RequestID] = p
			}

			var redirectEvent *ObserverEvent
			if e.RedirectResponse != nil {
				evt, ok := redirectNetEvent(e.RequestID, e, p)
				if ok {
					redirectEvent = &evt
				}
			}

			p.method = e.Request.Method
			p.url = e.Request.URL
			p.resType = string(e.Type)
			p.frameID = string(e.FrameID)
			p.startedAt = float64(e.Timestamp)
			p.status = 0
			p.mimeType = ""
			p.requestID = string(e.RequestID)
			p.reqHeaders = cloneHeaders(flattenHeaders(e.Request.Headers))
			p.postData = e.Request.PostData
			o.mu.Unlock()

			if redirectEvent != nil {
				_ = o.emit(*redirectEvent)
			}
		},
		func(e *proto.NetworkResponseReceived) {
			o.mu.Lock()
			p := o.pending[e.RequestID]
			if p == nil {
				p = &pendingNet{url: e.Response.URL, resType: string(e.Type), frameID: string(e.FrameID)}
				o.pending[e.RequestID] = p
			}
			p.status = e.Response.Status
			p.mimeType = e.Response.MIMEType
			p.resHeaders = cloneHeaders(flattenHeaders(e.Response.Headers))
			o.mu.Unlock()
		},
		func(e *proto.NetworkLoadingFinished) {
			o.mu.Lock()
			p := o.pending[e.RequestID]
			if p == nil {
				o.mu.Unlock()
				return
			}
			method := p.method
			url := p.url
			resType := p.resType
			frameID := p.frameID
			startedAt := p.startedAt
			status := p.status
			mimeType := p.mimeType
			requestID := p.requestID
			reqHeaders := cloneHeaders(p.reqHeaders)
			resHeaders := cloneHeaders(p.resHeaders)
			postData := p.postData
			body, bodyBase64, bodyErr := getResponseBody(o.page, e.RequestID)
			if bodyErr == nil {
				p.body = body
				p.bodyBase64 = bodyBase64
			} else {
				p.bodyError = bodyErr.Error()
			}
			bodyOut := p.body
			bodyBase64Out := p.bodyBase64
			bodyErrorOut := p.bodyError
			delete(o.pending, e.RequestID)
			o.mu.Unlock()

			if !o.acceptNetType(resType) || !o.acceptNetURL(url) {
				return
			}
			var durMs int64
			if startedAt > 0 {
				durMs = int64((float64(e.Timestamp) - startedAt) * 1000)
			}
			o.emit(ObserverEvent{
				TS:         time.Now().UnixMilli(),
				Kind:       KindNet,
				Method:     method,
				URL:        url,
				Status:     status,
				MimeType:   mimeType,
				Type:       resType,
				RequestID:  requestID,
				ReqHeaders: reqHeaders,
				ResHeaders: resHeaders,
				PostData:   postData,
				Body:       bodyOut,
				BodyBase64: bodyBase64Out,
				BodyError:  bodyErrorOut,
				Size:       int64(e.EncodedDataLength),
				DurationMs: durMs,
				Frame:      frameID,
			})
		},
		func(e *proto.NetworkLoadingFailed) {
			o.mu.Lock()
			p := o.pending[e.RequestID]
			if p == nil {
				o.mu.Unlock()
				return
			}
			delete(o.pending, e.RequestID)
			method := p.method
			url := p.url
			resType := p.resType
			frameID := p.frameID
			startedAt := p.startedAt
			requestID := p.requestID
			o.mu.Unlock()

			if !o.acceptNetType(resType) || !o.acceptNetURL(url) {
				return
			}
			var durMs int64
			if startedAt > 0 {
				durMs = int64((float64(e.Timestamp) - startedAt) * 1000)
			}
			o.emit(ObserverEvent{
				TS:         time.Now().UnixMilli(),
				Kind:       KindNet,
				Method:     method,
				URL:        url,
				RequestID:  requestID,
				Type:       resType,
				Failed:     e.ErrorText,
				DurationMs: durMs,
				Frame:      frameID,
			})
		},
	)
	go stop()

	<-ctx.Done()
	cancel()
}

// listenConsole subscribes to Runtime.consoleAPICalled and Runtime.exceptionThrown.
func (o *Observer) listenConsole(ctx context.Context) {
	defer o.wg.Done()
	scoped, cancel := o.page.WithCancel()
	defer cancel()

	stop := scoped.EachEvent(
		func(e *proto.RuntimeConsoleAPICalled) {
			level := string(e.Type)
			if !o.acceptConsoleLevel(level) {
				return
			}
			var parts []string
			for _, arg := range e.Args {
				if !arg.Value.Nil() {
					parts = append(parts, arg.Value.String())
				} else if arg.Description != "" {
					parts = append(parts, arg.Description)
				} else if arg.UnserializableValue != "" {
					parts = append(parts, string(arg.UnserializableValue))
				}
			}
			text := joinParts(parts, " ")
			src := ""
			if e.StackTrace != nil && len(e.StackTrace.CallFrames) > 0 {
				f := e.StackTrace.CallFrames[0]
				src = f.URL
			}
			o.emit(ObserverEvent{
				TS:     time.Now().UnixMilli(),
				Kind:   KindConsole,
				Level:  level,
				Text:   text,
				Source: src,
			})
		},
		func(e *proto.RuntimeExceptionThrown) {
			if !o.acceptConsoleLevel("error") {
				return
			}
			msg := ""
			src := ""
			if e.ExceptionDetails.Exception != nil {
				if e.ExceptionDetails.Exception.Description != "" {
					msg = e.ExceptionDetails.Exception.Description
				} else if !e.ExceptionDetails.Exception.Value.Nil() {
					msg = e.ExceptionDetails.Exception.Value.String()
				}
			}
			if msg == "" {
				msg = e.ExceptionDetails.Text
			}
			if e.ExceptionDetails.URL != "" {
				src = e.ExceptionDetails.URL
			} else if e.ExceptionDetails.StackTrace != nil && len(e.ExceptionDetails.StackTrace.CallFrames) > 0 {
				src = e.ExceptionDetails.StackTrace.CallFrames[0].URL
			}
			o.emit(ObserverEvent{
				TS:     time.Now().UnixMilli(),
				Kind:   KindError,
				Level:  "error",
				Text:   msg,
				Source: src,
			})
		},
	)
	go stop()

	<-ctx.Done()
	cancel()
}

// listenLog subscribes to Log.entryAdded — the browser-side log channel
// that carries CORS, mixed-content, CSP, deprecation, and network-resource
// errors (ERR_*) which Runtime.consoleAPICalled does NOT surface. These are
// the red/yellow messages visible in DevTools Console that are NOT from
// JavaScript console.log() but from the browser itself.
func (o *Observer) listenLog(ctx context.Context) {
	defer o.wg.Done()
	scoped, cancel := o.page.WithCancel()
	defer cancel()

	stop := scoped.EachEvent(
		func(e *proto.LogEntryAdded) {
			if e.Entry == nil {
				return
			}
			level := string(e.Entry.Level)
			if !o.acceptConsoleLevel(level) {
				return
			}
			kind := KindConsole
			if level == "error" {
				kind = KindError
			}
			o.emit(ObserverEvent{
				TS:     time.Now().UnixMilli(),
				Kind:   kind,
				Level:  level,
				Text:   e.Entry.Text,
				URL:    e.Entry.URL,
				Source: string(e.Entry.Source), // "network"|"security"|"deprecation"|"violation"|...
			})
		},
	)
	go stop()

	<-ctx.Done()
	cancel()
}

// listenPage subscribes to Page.frameNavigated, Page.lifecycleEvent, Page.loadEventFired.
func (o *Observer) listenPage(ctx context.Context) {
	defer o.wg.Done()
	scoped, cancel := o.page.WithCancel()
	defer cancel()
	_ = proto.PageSetLifecycleEventsEnabled{Enabled: true}.Call(scoped)

	lifecycleAllowed := map[proto.PageLifecycleEventName]bool{
		"load":             true,
		"DOMContentLoaded": true,
		"networkIdle":      true,
	}

	stop := scoped.EachEvent(
		func(e *proto.PageFrameNavigated) {
			loader := ""
			if e.Frame != nil {
				loader = string(e.Frame.LoaderID)
			}
			o.emit(ObserverEvent{
				TS:     time.Now().UnixMilli(),
				Kind:   KindPage,
				Event:  "frameNavigated",
				URL:    e.Frame.URL,
				Frame:  string(e.Frame.ID),
				Loader: loader,
			})
		},
		func(e *proto.PageLifecycleEvent) {
			if !lifecycleAllowed[e.Name] {
				return
			}
			o.emit(ObserverEvent{
				TS:     time.Now().UnixMilli(),
				Kind:   KindPage,
				Event:  string(e.Name),
				Frame:  string(e.FrameID),
				Loader: string(e.LoaderID),
			})
		},
		func(e *proto.PageLoadEventFired) {
			o.emit(ObserverEvent{
				TS:    time.Now().UnixMilli(),
				Kind:  KindPage,
				Event: "loadEventFired",
			})
		},
		func(e *proto.PageNavigatedWithinDocument) {
			if e == nil {
				return
			}
			o.emit(ObserverEvent{
				TS:    time.Now().UnixMilli(),
				Kind:  KindPage,
				Event: "navigatedWithinDocument",
				URL:   e.URL,
				Frame: string(e.FrameID),
			})
		},
	)
	go stop()

	<-ctx.Done()
	cancel()
}

func joinParts(parts []string, sep string) string {
	out := strings.Join(parts, sep)
	if out == "" {
		out = "(empty)"
	}
	return out
}
