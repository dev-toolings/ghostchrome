package artifact

import (
	"encoding/json"
	"os"
	"time"

	"github.com/dev-toolings/ghostchrome/internal/core/engine"
)

// HAR follows the HAR 1.2 specification (W3C Web Performance Working Group).
// It's intentionally minimal: we only populate what NetworkEntry knows about,
// leaving the optional fields (headers, cookies, content text) empty so parsers
// treat them as "not captured" rather than "empty".
type HAR struct {
	Log HARLog `json:"log"`
}

// HARLog is the top-level container.
type HARLog struct {
	Version string     `json:"version"`
	Creator HARCreator `json:"creator"`
	Pages   []HARPage  `json:"pages"`
	Entries []HAREntry `json:"entries"`
}

// HARCreator identifies the tool that recorded the trace.
type HARCreator struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// HARPage groups entries by the top-level navigation that produced them.
type HARPage struct {
	StartedDateTime string `json:"startedDateTime"`
	ID              string `json:"id"`
	Title           string `json:"title"`
}

// HAREntry is one network request + response.
type HAREntry struct {
	PageRef         string      `json:"pageref,omitempty"`
	StartedDateTime string      `json:"startedDateTime"`
	Time            int64       `json:"time"`
	Request         HARRequest  `json:"request"`
	Response        HARResponse `json:"response"`
	Cache           struct{}    `json:"cache"`
	Timings         HARTimings  `json:"timings"`
}

// HARRequest describes the outgoing request.
type HARRequest struct {
	Method      string   `json:"method"`
	URL         string   `json:"url"`
	HTTPVersion string   `json:"httpVersion"`
	Headers     []string `json:"headers"`
	QueryString []string `json:"queryString"`
	HeadersSize int      `json:"headersSize"`
	BodySize    int      `json:"bodySize"`
}

// HARResponse describes the incoming response.
type HARResponse struct {
	Status      int        `json:"status"`
	StatusText  string     `json:"statusText"`
	HTTPVersion string     `json:"httpVersion"`
	Headers     []string   `json:"headers"`
	Cookies     []string   `json:"cookies"`
	Content     HARContent `json:"content"`
	RedirectURL string     `json:"redirectURL"`
	HeadersSize int        `json:"headersSize"`
	BodySize    int        `json:"bodySize"`
}

// HARContent carries the payload summary (size, MIME).
type HARContent struct {
	Size     int    `json:"size"`
	MimeType string `json:"mimeType"`
}

// HARTimings models per-phase durations. We only know the total so we put it
// on "wait".
type HARTimings struct {
	Send    int   `json:"send"`
	Wait    int64 `json:"wait"`
	Receive int   `json:"receive"`
}

// BuildHAR constructs a HAR from the passive NetworkEntry slice collected by
// requestTracker. pageURL and pageTitle name the top-level page.
func BuildHAR(entries []engine.NetworkEntry, pageURL, pageTitle, creatorVersion string) *HAR {
	now := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")

	page := HARPage{
		StartedDateTime: now,
		ID:              "page_1",
		Title:           pageTitle,
	}
	if page.Title == "" {
		page.Title = pageURL
	}

	out := &HAR{
		Log: HARLog{
			Version: "1.2",
			Creator: HARCreator{Name: "ghostchrome", Version: creatorVersion},
			Pages:   []HARPage{page},
			Entries: make([]HAREntry, 0, len(entries)),
		},
	}

	for _, e := range entries {
		method := e.Method
		if method == "" {
			method = "GET"
		}
		status := e.Status
		bodySize := e.Size
		if bodySize < 0 {
			bodySize = 0
		}
		entry := HAREntry{
			PageRef:         "page_1",
			StartedDateTime: now,
			Time:            e.TimeMs,
			Request: HARRequest{
				Method:      method,
				URL:         e.URL,
				HTTPVersion: "HTTP/1.1",
				Headers:     []string{},
				QueryString: []string{},
				HeadersSize: -1,
				BodySize:    -1,
			},
			Response: HARResponse{
				Status:      status,
				StatusText:  "",
				HTTPVersion: "HTTP/1.1",
				Headers:     []string{},
				Cookies:     []string{},
				Content: HARContent{
					Size:     bodySize,
					MimeType: e.MimeType,
				},
				RedirectURL: "",
				HeadersSize: -1,
				BodySize:    bodySize,
			},
			Timings: HARTimings{Wait: e.TimeMs},
		}
		out.Log.Entries = append(out.Log.Entries, entry)
	}

	return out
}

// WriteHAR persists the HAR JSON to path (0o600).
func WriteHAR(h *HAR, path string) error {
	data, err := json.MarshalIndent(h, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}
