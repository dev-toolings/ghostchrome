package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

// CaptureSpec configures a passive network capture session.
// Generic DevTools-Network-tab style recorder: no request modification,
// no auth bypass — just observation of what the page already requested.
type CaptureSpec struct {
	// URLMatch is a regex applied to the full request URL. Empty = match all.
	URLMatch string
	// MimeMatch is a regex applied to the response MIME type. Empty = match all.
	MimeMatch string
	// Max is the number of MATCHING entries to collect before ReachedMax fires.
	// 0 = unlimited.
	Max int
	// IncludeBody, when true, fetches the response body via
	// Network.getResponseBody for every matching entry.
	IncludeBody bool
	// ExcludeStatic drops script, stylesheet, font, image, and media requests from
	// matching entries. It is intended for Playwright CLI-compatible network
	// output; raw capture keeps static resources by default.
	ExcludeStatic bool
	// OutputPath, if set, streams each entry as it is captured (NDJSON).
	OutputPath string
}

// CapturedEntry is one fully-hydrated request/response pair.
type CapturedEntry struct {
	RequestID    string            `json:"request_id"`
	Method       string            `json:"method"`
	URL          string            `json:"url"`
	ResourceType string            `json:"resource_type"`
	Status       int               `json:"status"`
	StatusText   string            `json:"status_text,omitempty"`
	MimeType     string            `json:"mime_type,omitempty"`
	ReqHeaders   map[string]string `json:"request_headers,omitempty"`
	ResHeaders   map[string]string `json:"response_headers,omitempty"`
	PostData     string            `json:"post_data,omitempty"`
	Body         string            `json:"body,omitempty"`
	BodyBase64   bool              `json:"body_base64,omitempty"`
	BodyError    string            `json:"body_error,omitempty"`
	StartedAt    string            `json:"started_at"`
}

// CaptureSession owns the goroutine that listens to Network events.
type CaptureSession struct {
	spec       CaptureSpec
	urlRe      *regexp.Regexp
	mimeRe     *regexp.Regexp
	page       *rod.Page
	stop       func()
	done       chan struct{}
	stopOnce   sync.Once
	outFile    *os.File
	outMu      sync.Mutex
	writeErr   error
	mu         sync.Mutex
	pending    map[proto.NetworkRequestID]*CapturedEntry
	matched    []*CapturedEntry
	reachedMax chan struct{}
	maxFired   bool
}

// StartCapture enables the Network domain and begins listening.
func StartCapture(page *rod.Page, spec CaptureSpec) (*CaptureSession, error) {
	enable := proto.NetworkEnable{}
	if err := enable.Call(page); err != nil {
		return nil, fmt.Errorf("network enable: %w", err)
	}

	s := &CaptureSession{
		spec:       spec,
		page:       page,
		pending:    map[proto.NetworkRequestID]*CapturedEntry{},
		reachedMax: make(chan struct{}),
		done:       make(chan struct{}),
	}
	if spec.URLMatch != "" {
		re, err := regexp.Compile(spec.URLMatch)
		if err != nil {
			return nil, fmt.Errorf("url-match regex: %w", err)
		}
		s.urlRe = re
	}
	if spec.MimeMatch != "" {
		re, err := regexp.Compile(spec.MimeMatch)
		if err != nil {
			return nil, fmt.Errorf("mime-match regex: %w", err)
		}
		s.mimeRe = re
	}
	if spec.OutputPath != "" {
		f, err := os.OpenFile(spec.OutputPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
		if err != nil {
			return nil, fmt.Errorf("open output: %w", err)
		}
		s.outFile = f
	}

	ctx, cancel := context.WithCancel(page.GetContext())
	wait := page.Context(ctx).EachEvent(
		func(e *proto.NetworkRequestWillBeSent) {
			s.mu.Lock()
			entry := s.pending[e.RequestID]
			if entry == nil {
				entry = &CapturedEntry{
					RequestID:    string(e.RequestID),
					Method:       e.Request.Method,
					URL:          e.Request.URL,
					ResourceType: string(e.Type),
					ReqHeaders:   flattenHeaders(e.Request.Headers),
					PostData:     e.Request.PostData,
					StartedAt:    time.Now().UTC().Format(time.RFC3339Nano),
				}
				s.pending[e.RequestID] = entry
			}

			var redirect *CapturedEntry
			if e.RedirectResponse != nil {
				redirect = &CapturedEntry{
					RequestID:    string(e.RequestID),
					Method:       firstNonEmpty(entry.Method, e.Request.Method),
					URL:          firstNonEmpty(e.RedirectResponse.URL, e.Request.URL),
					ResourceType: firstNonEmpty(entry.ResourceType, string(e.Type)),
					Status:       e.RedirectResponse.Status,
					MimeType:     e.RedirectResponse.MIMEType,
					ReqHeaders:   cloneStringMap(entry.ReqHeaders),
					ResHeaders:   flattenHeaders(e.RedirectResponse.Headers),
					StartedAt:    entry.StartedAt,
				}
				if len(redirect.ReqHeaders) == 0 {
					redirect.ReqHeaders = flattenHeaders(e.Request.Headers)
				}
			}

			entry.Method = e.Request.Method
			entry.URL = e.Request.URL
			entry.ResourceType = string(e.Type)
			entry.ReqHeaders = flattenHeaders(e.Request.Headers)
			entry.PostData = e.Request.PostData
			s.mu.Unlock()

			if redirect != nil && s.matches(redirect) {
				s.record(redirect)
			}
		},
		func(e *proto.NetworkResponseReceived) {
			s.mu.Lock()
			entry, ok := s.pending[e.RequestID]
			s.mu.Unlock()
			if !ok {
				return
			}
			entry.Status = e.Response.Status
			entry.StatusText = e.Response.StatusText
			entry.MimeType = e.Response.MIMEType
			entry.ResHeaders = flattenHeaders(e.Response.Headers)
		},
		func(e *proto.NetworkLoadingFinished) {
			s.mu.Lock()
			entry, ok := s.pending[e.RequestID]
			if ok {
				delete(s.pending, e.RequestID)
			}
			s.mu.Unlock()
			if !ok {
				return
			}
			if !s.matches(entry) {
				return
			}
			if spec.IncludeBody {
				body, err := proto.NetworkGetResponseBody{RequestID: e.RequestID}.Call(page)
				if err != nil {
					entry.BodyError = err.Error()
				} else if body != nil {
					entry.Body = body.Body
					entry.BodyBase64 = body.Base64Encoded
				}
			}
			s.record(entry)
		},
		func(e *proto.NetworkLoadingFailed) {
			s.mu.Lock()
			delete(s.pending, e.RequestID)
			s.mu.Unlock()
		},
	)

	go func() {
		wait()
		close(s.done)
	}()
	s.stop = cancel

	return s, nil
}

func (s *CaptureSession) matches(e *CapturedEntry) bool {
	if s.urlRe != nil && !s.urlRe.MatchString(e.URL) {
		return false
	}
	if s.mimeRe != nil && !s.mimeRe.MatchString(e.MimeType) {
		return false
	}
	if s.spec.ExcludeStatic && IsStaticNetworkEntry(e.ResourceType, e.MimeType) {
		return false
	}
	return true
}

// IsStaticNetworkEntry reports whether a request is usually noise for
// Playwright CLI-style network inspection.
func IsStaticNetworkEntry(resourceType, mimeType string) bool {
	switch strings.ToLower(resourceType) {
	case "script":
		return true
	case "image", "stylesheet", "font", "media":
		return true
	}
	mime := strings.ToLower(mimeType)
	return strings.HasPrefix(mime, "image/") ||
		strings.Contains(mime, "font") ||
		strings.HasPrefix(mime, "audio/") ||
		strings.HasPrefix(mime, "video/") ||
		mime == "text/css" ||
		strings.Contains(mime, "javascript") ||
		strings.Contains(mime, "ecmascript")
}

func (s *CaptureSession) record(e *CapturedEntry) {
	s.mu.Lock()
	s.matched = append(s.matched, e)
	n := len(s.matched)
	s.mu.Unlock()

	if s.outFile != nil {
		s.outMu.Lock()
		if s.writeErr == nil {
			data, err := json.Marshal(e)
			if err != nil {
				s.writeErr = fmt.Errorf("marshal: %w", err)
			} else {
				_, err := s.outFile.Write(data)
				if err != nil {
					s.writeErr = fmt.Errorf("write data: %w", err)
				} else {
					_, err := s.outFile.Write([]byte("\n"))
					if err != nil {
						s.writeErr = fmt.Errorf("write newline: %w", err)
					}
				}
			}
		}
		s.outMu.Unlock()
	}

	if s.spec.Max > 0 && n >= s.spec.Max {
		s.mu.Lock()
		if !s.maxFired {
			s.maxFired = true
			close(s.reachedMax)
		}
		s.mu.Unlock()
	}
}

// ReachedMax fires once Max matches have been collected.
func (s *CaptureSession) ReachedMax() <-chan struct{} { return s.reachedMax }

// Stop detaches listeners, closes the output file, and returns any write errors.
func (s *CaptureSession) Stop() ([]*CapturedEntry, error) {
	s.stopOnce.Do(func() {
		if s.stop != nil {
			s.stop()
		}
		if s.done != nil {
			select {
			case <-s.done:
			case <-time.After(2 * time.Second):
			}
		}
		if s.outFile != nil {
			s.outFile.Close()
		}
	})
	s.outMu.Lock()
	writeErr := s.writeErr
	s.outMu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.matched, writeErr
}

// Entries returns a snapshot of collected entries.
func (s *CaptureSession) Entries() []*CapturedEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*CapturedEntry, len(s.matched))
	copy(out, s.matched)
	return out
}

// GetResponseBodyByRequestID fetches a response body from the current CDP session.
func GetResponseBodyByRequestID(page *rod.Page, requestID string) (string, bool, error) {
	if page == nil || requestID == "" {
		return "", false, errors.New("missing page/request id")
	}
	resp, err := proto.NetworkGetResponseBody{
		RequestID: proto.NetworkRequestID(requestID),
	}.Call(page)
	if err != nil {
		return "", false, err
	}
	return resp.Body, resp.Base64Encoded, nil
}

func cloneStringMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func flattenHeaders(h proto.NetworkHeaders) map[string]string {
	if h == nil {
		return nil
	}
	out := make(map[string]string, len(h))
	for k, v := range h {
		switch vv := v.Val().(type) {
		case string:
			out[k] = vv
		default:
			out[k] = strings.TrimSpace(fmt.Sprintf("%v", vv))
		}
	}
	return out
}
