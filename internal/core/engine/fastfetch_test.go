package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestExtractNextData(t *testing.T) {
	cases := map[string]struct {
		body  string
		empty bool
		want  string
	}{
		"canonical": {
			body: `<html><body>x<script id="__NEXT_DATA__" type="application/json">{"a":1}</script></body></html>`,
			want: `{"a":1}`,
		},
		"reordered_attrs": {
			body: `<script type="application/json" id="__NEXT_DATA__">{"b":2}</script>`,
			want: `{"b":2}`,
		},
		"with_whitespace": {
			body: `<script id="__NEXT_DATA__" type="application/json">  {"c":3}  </script>`,
			want: `{"c":3}`,
		},
		"missing": {
			body:  `<html><body>nothing</body></html>`,
			empty: true,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got := extractNextData(tc.body)
			if tc.empty {
				if got != nil {
					t.Fatalf("expected nil, got %q", got)
				}
				return
			}
			if string(got) != tc.want {
				t.Fatalf("expected %q, got %q", tc.want, got)
			}
		})
	}
}

func TestDetectAntiBot(t *testing.T) {
	cases := []struct {
		name    string
		status  int
		body    string
		blocked bool
	}{
		{"ok", 200, `<html>real content</html>`, false},
		{"403", 403, ``, true},
		{"429", 429, ``, true},
		{"503", 503, ``, true},
		{"datadome_challenge", 200, `<html><title>Access Denied</title>geo.captcha-delivery.com you have been blocked</html>`, true},
		{"datadome_sdk_only", 200, strings.Repeat("x", 60_000) + `<script src="//geo.captcha-delivery.com/tags.js"></script>`, false},
		{"cloudflare", 200, `<title>Just a moment...</title><div class="cf-browser-verification"></div>challenge-platform`, true},
		{"captcha_title", 200, `<html><title>Captcha required</title></html>`, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			blocked, reason := detectAntiBot(tc.status, tc.body)
			if blocked != tc.blocked {
				t.Fatalf("blocked: got %v (%q), want %v", blocked, reason, tc.blocked)
			}
		})
	}
}

func TestFastFetchHappyPath(t *testing.T) {
	payload := map[string]any{"props": map[string]any{"pageProps": map[string]any{"listings": []int{1, 2, 3}}}}
	jb, _ := json.Marshal(payload)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, `<html><body><script id="__NEXT_DATA__" type="application/json">%s</script></body></html>`, jb)
	}))
	defer srv.Close()

	res, err := FastFetch(context.Background(), srv.URL, FastFetchOpts{})
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if res.Status != 200 {
		t.Fatalf("status: %d", res.Status)
	}
	if res.Blocked {
		t.Fatalf("unexpectedly blocked: %s", res.Reason)
	}
	if res.NextData == nil {
		t.Fatalf("missing __NEXT_DATA__")
	}
	var got map[string]any
	if err := json.Unmarshal(res.NextData, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
}

func TestFastFetchBlocked(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
		fmt.Fprint(w, "Forbidden")
	}))
	defer srv.Close()

	res, err := FastFetch(context.Background(), srv.URL, FastFetchOpts{})
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if !res.Blocked {
		t.Fatalf("expected blocked, got status=%d reason=%q", res.Status, res.Reason)
	}
}
