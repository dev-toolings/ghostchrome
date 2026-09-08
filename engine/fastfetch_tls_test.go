package engine

import (
	"compress/gzip"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"
)

func TestFastFetchTLSFallback(t *testing.T) {
	for _, status := range []int{403, 503, 200, 429, 401, 404} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if calls.Add(1) == 1 {
					w.WriteHeader(status)
					fmt.Fprint(w, "no payload")
					return
				}
				fmt.Fprint(w, `<script id="__NEXT_DATA__" type="application/json">{"ok":true}</script>`)
			}))
			defer server.Close()
			result, err := FastFetch(context.Background(), server.URL, FastFetchOpts{FallbackTLS: true})
			if err != nil {
				t.Fatal(err)
			}
			want := int32(2)
			if status == 429 || status == 401 || status == 404 {
				want = 1
			}
			if calls.Load() != want {
				t.Fatalf("calls=%d, want %d", calls.Load(), want)
			}
			if want == 2 && (result.Transport != "http-tls" || result.NextData == nil || result.Blocked) {
				t.Fatalf("unexpected result: %+v", result)
			}
		})
	}
}

func TestFastFetchTLSNoRetryForUsefulSSR(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		fmt.Fprint(w, `<script type="application/ld+json">{"@type":"Product","name":"Example"}</script>`)
	}))
	defer server.Close()
	result, err := FastFetch(context.Background(), server.URL, FastFetchOpts{FallbackTLS: true})
	if err != nil || result.Transport != "http" || calls.Load() != 1 {
		t.Fatalf("result=%+v calls=%d err=%v", result, calls.Load(), err)
	}
}

func TestFastFetchTLSSharedDeadline(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer server.Close()
	start := time.Now()
	_, err := FastFetch(context.Background(), server.URL, FastFetchOpts{FallbackTLS: true, Timeout: 40 * time.Millisecond})
	if err == nil || time.Since(start) > time.Second {
		t.Fatalf("deadline not honored: %v", err)
	}
}

func TestFastFetchTLSLive(t *testing.T) {
	url := os.Getenv("GHOSTCHROME_TEST_TLS_URL")
	if url == "" || testing.Short() {
		t.Skip("opt-in public HTTP probe")
	}
	result, err := FastFetch(context.Background(), url, FastFetchOpts{FallbackTLS: true, Timeout: 20 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("status=%d transport=%s blocked=%v ssr=%d bytes=%d", result.Status, result.Transport, result.Blocked, len(result.SSRPayloads), len(result.HTML))
	if result.Blocked || result.Status != 200 || len(result.SSRPayloads) == 0 {
		t.Fatal("public page did not yield usable data")
	}
}

func TestFastFetchTLSReadsCompressedPayloadOnce(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(403)
			return
		}
		w.Header().Set("Content-Encoding", "gzip")
		writer := gzip.NewWriter(w)
		fmt.Fprint(writer, `<script id="__NEXT_DATA__" type="application/json">{"ok":true}</script>`)
		writer.Close()
	}))
	defer server.Close()
	result, err := FastFetch(context.Background(), server.URL, FastFetchOpts{FallbackTLS: true})
	if err != nil {
		t.Fatal(err)
	}
	if string(result.NextData) != `{"ok":true}` {
		t.Fatalf("unexpected payload: %s", result.NextData)
	}
}

func TestFastFetchTLSDoesNotFollowCrossOriginRedirect(t *testing.T) {
	var leaked atomic.Bool
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked.Store(true) }))
	defer other.Close()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(403)
			return
		}
		http.Redirect(w, r, other.URL, http.StatusFound)
	}))
	defer server.Close()
	result, err := FastFetch(context.Background(), server.URL, FastFetchOpts{FallbackTLS: true, ExtraHeaders: map[string]string{"Authorization": "test-only"}})
	if err != nil || !result.Blocked || leaked.Load() {
		t.Fatalf("result=%+v err=%v leaked=%v", result, err, leaked.Load())
	}
}
