package engine

import (
	"sort"
	"sync"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

type requestTracker struct {
	mu                    sync.Mutex
	requests              map[proto.NetworkRequestID]*trackedRequest
	mainDocumentRequestID proto.NetworkRequestID
	mainFrameID           proto.PageFrameID
	cancel                func()
}

type trackedRequest struct {
	id        proto.NetworkRequestID
	method    string
	url       string
	status    int
	sizeBytes int
	timeMs    int64
	mimeType  string
	errorText string
	reqType   proto.NetworkResourceType
	frameID   proto.PageFrameID
	startedAt float64
	ended     bool
	failed    bool
}

func newRequestTracker(page *rod.Page) *requestTracker {
	return &requestTracker{
		requests:    map[proto.NetworkRequestID]*trackedRequest{},
		mainFrameID: page.FrameID,
	}
}

func (t *requestTracker) listen(page *rod.Page) {
	scoped, cancel := page.WithCancel()
	t.cancel = cancel
	wait := scoped.EachEvent(
		func(e *proto.NetworkRequestWillBeSent) {
			t.mu.Lock()
			defer t.mu.Unlock()

			req := t.requests[e.RequestID]
			if req == nil {
				req = &trackedRequest{id: e.RequestID}
				t.requests[e.RequestID] = req
			}
			req.method = e.Request.Method
			req.url = e.Request.URL
			req.reqType = e.Type
			req.frameID = e.FrameID
			req.startedAt = float64(e.Timestamp)
			req.failed = false
			req.ended = false
			req.errorText = ""
			req.timeMs = 0
			req.sizeBytes = 0

			if t.mainDocumentRequestID == "" && e.Type == proto.NetworkResourceTypeDocument && e.FrameID == t.mainFrameID {
				t.mainDocumentRequestID = e.RequestID
			}
		},
		func(e *proto.NetworkResponseReceived) {
			t.mu.Lock()
			defer t.mu.Unlock()

			req := t.requests[e.RequestID]
			if req == nil {
				req = &trackedRequest{id: e.RequestID}
				t.requests[e.RequestID] = req
			}
			req.url = e.Response.URL
			req.status = e.Response.Status
			req.mimeType = e.Response.MIMEType
			req.reqType = e.Type
			req.frameID = e.FrameID
		},
		func(e *proto.NetworkLoadingFinished) {
			t.mu.Lock()
			defer t.mu.Unlock()

			req := t.requests[e.RequestID]
			if req == nil {
				return
			}
			req.ended = true
			req.sizeBytes = int(e.EncodedDataLength)
			if req.startedAt > 0 {
				req.timeMs = int64((float64(e.Timestamp) - req.startedAt) * 1000)
			}
		},
		func(e *proto.NetworkLoadingFailed) {
			t.mu.Lock()
			defer t.mu.Unlock()

			req := t.requests[e.RequestID]
			if req == nil {
				req = &trackedRequest{id: e.RequestID}
				t.requests[e.RequestID] = req
			}
			req.failed = true
			req.ended = true
			req.errorText = e.ErrorText
			req.reqType = e.Type
			if req.startedAt > 0 {
				req.timeMs = int64((float64(e.Timestamp) - req.startedAt) * 1000)
			}
		},
	)
	go wait()
}

func (t *requestTracker) close() {
	if t.cancel != nil {
		t.cancel()
		t.cancel = nil
	}
}

func (t *requestTracker) MainDocumentStatus() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.mainDocumentRequestID == "" {
		return 0
	}
	req := t.requests[t.mainDocumentRequestID]
	if req == nil {
		return 0
	}
	return req.status
}

func (t *requestTracker) Entries() []NetworkEntry {
	t.mu.Lock()
	defer t.mu.Unlock()

	entries := make([]NetworkEntry, 0, len(t.requests))
	for _, req := range t.requests {
		if req.url == "" {
			continue
		}
		entries = append(entries, NetworkEntry{
			Method:   req.method,
			URL:      req.url,
			Status:   req.status,
			Size:     req.sizeBytes,
			TimeMs:   req.timeMs,
			MimeType: req.mimeType,
			Error:    req.errorText,
		})
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].URL < entries[j].URL
	})
	return entries
}

func (t *requestTracker) ErrorEntries() []ErrorEntry {
	t.mu.Lock()
	defer t.mu.Unlock()

	errors := make([]ErrorEntry, 0)
	for _, req := range t.requests {
		switch {
		case req.failed:
			errors = append(errors, ErrorEntry{
				Type:    "network",
				Level:   "error",
				Message: req.url,
				Source:  req.errorText,
				Method:  req.method,
				TimeMs:  req.timeMs,
			})
		case req.status >= 500:
			errors = append(errors, ErrorEntry{
				Type:    "network",
				Level:   "5xx",
				Message: req.url,
				Source:  req.url,
				Status:  req.status,
				Method:  req.method,
				TimeMs:  req.timeMs,
			})
		case req.status >= 400:
			errors = append(errors, ErrorEntry{
				Type:    "network",
				Level:   "4xx",
				Message: req.url,
				Source:  req.url,
				Status:  req.status,
				Method:  req.method,
				TimeMs:  req.timeMs,
			})
		}
	}

	sort.Slice(errors, func(i, j int) bool {
		if errors[i].TimeMs == errors[j].TimeMs {
			return errors[i].Message < errors[j].Message
		}
		return errors[i].TimeMs < errors[j].TimeMs
	})
	return errors
}
