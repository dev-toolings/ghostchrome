package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"regexp"
	"sync"
	"testing"
	"time"

	"github.com/go-rod/rod/lib/proto"
)

// --- unit tests (no Chrome required) ---

func TestObserverFilters_NetTypes(t *testing.T) {
	filters := ObserverFilters{
		NetTypes: []string{"xhr", "fetch"},
	}
	o := &Observer{opts: ObserverOpts{Filters: filters}}
	if !o.acceptNetType("xhr") {
		t.Error("expected xhr to be accepted")
	}
	if !o.acceptNetType("fetch") {
		t.Error("expected fetch to be accepted")
	}
	if o.acceptNetType("document") {
		t.Error("expected document to be rejected")
	}
	// Empty NetTypes → accept all
	o2 := &Observer{opts: ObserverOpts{}}
	if !o2.acceptNetType("document") {
		t.Error("empty filter should accept all types")
	}
}

func TestObserverConcurrentEmitAndStop(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	o := &Observer{
		opts:    ObserverOpts{BufferSize: 1},
		events:  make(chan ObserverEvent, 1),
		pending: make(map[proto.NetworkRequestID]*pendingNet),
		cancel:  cancel,
		stopped: make(chan struct{}),
	}
	o.wg.Add(1)
	go o.waitAndClose()

	var emitters sync.WaitGroup
	emitters.Add(32)
	for range 32 {
		go func() {
			defer emitters.Done()
			for range 100 {
				o.emit(ObserverEvent{TS: time.Now().UnixMilli(), Kind: KindPage})
			}
		}()
	}

	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		_ = o.Stop()
	}()
	cancel()
	o.wg.Done()
	emitters.Wait()
	select {
	case <-stopped:
	case <-ctx.Done():
		select {
		case <-stopped:
		case <-time.After(time.Second):
			t.Fatal("Observer.Stop did not return")
		}
	}
}

func TestObserverFilters_NetURLRegex(t *testing.T) {
	re := regexp.MustCompile(`api\.example\.com`)
	filters := ObserverFilters{NetURLRegex: re}
	o := &Observer{opts: ObserverOpts{Filters: filters}}
	if !o.acceptNetURL("https://api.example.com/v1/data") {
		t.Error("should match regex")
	}
	if o.acceptNetURL("https://cdn.example.com/style.css") {
		t.Error("should not match regex")
	}
}

func TestObserverFilters_ConsoleLevels(t *testing.T) {
	filters := ObserverFilters{ConsoleLevels: []string{"error", "warning"}}
	o := &Observer{opts: ObserverOpts{Filters: filters}}
	if !o.acceptConsoleLevel("error") {
		t.Error("should accept error")
	}
	if o.acceptConsoleLevel("info") {
		t.Error("should not accept info")
	}
	// Empty → accept all
	o2 := &Observer{opts: ObserverOpts{}}
	if !o2.acceptConsoleLevel("debug") {
		t.Error("empty filter should accept all console levels")
	}
}

func TestObserverFilters_Kinds(t *testing.T) {
	o := &Observer{opts: ObserverOpts{Filters: ObserverFilters{
		Kinds: []ObserverKind{KindNet, KindPage},
	}}}
	if !o.acceptKind(KindNet) {
		t.Error("net should be accepted")
	}
	if o.acceptKind(KindConsole) {
		t.Error("console should be rejected")
	}
	// Empty → accept all
	o2 := &Observer{opts: ObserverOpts{}}
	if !o2.acceptKind(KindError) {
		t.Error("empty filter should accept all kinds")
	}
}

func TestObserver_MaxEvents(t *testing.T) {
	o := &Observer{
		opts:    ObserverOpts{MaxEvents: 3, BufferSize: 10},
		events:  make(chan ObserverEvent, 10),
		pending: make(map[proto.NetworkRequestID]*pendingNet),
		stopped: make(chan struct{}),
	}
	// Emit 5 events
	for i := 0; i < 5; i++ {
		o.emit(ObserverEvent{TS: time.Now().UnixMilli(), Kind: KindPage, Event: "test"})
	}
	stats := o.Stats()
	if stats.Total != 3 {
		t.Errorf("expected 3 total events, got %d", stats.Total)
	}
	if stats.Dropped < 1 {
		t.Error("expected at least 1 dropped event due to MaxEvents")
	}
}

func TestObserver_DrainNonDestructive(t *testing.T) {
	o := &Observer{
		opts:    ObserverOpts{BufferSize: 10},
		events:  make(chan ObserverEvent, 10),
		pending: make(map[proto.NetworkRequestID]*pendingNet),
		stopped: make(chan struct{}),
	}
	now := time.Now().UnixMilli()
	o.emit(ObserverEvent{TS: now - 100, Kind: KindPage, Event: "old"})
	o.emit(ObserverEvent{TS: now, Kind: KindPage, Event: "new"})
	o.emit(ObserverEvent{TS: now + 100, Kind: KindPage, Event: "newer"})

	// Drain only events >= now
	drained := o.Drain(now)
	if len(drained) != 2 {
		t.Errorf("expected 2 drained events, got %d", len(drained))
	}
	// Second drain returns same results (non-destructive)
	drained2 := o.Drain(now)
	if len(drained2) != 2 {
		t.Errorf("Drain should be non-destructive, got %d on second call", len(drained2))
	}
}

func TestObserver_HistoryRing(t *testing.T) {
	o := &Observer{
		opts:    ObserverOpts{BufferSize: 8, History: 3},
		events:  make(chan ObserverEvent, 8),
		pending: make(map[proto.NetworkRequestID]*pendingNet),
		stopped: make(chan struct{}),
	}
	for i := 0; i < 5; i++ {
		o.emit(ObserverEvent{TS: int64(i + 1), Kind: KindPage, Event: "e"})
	}
	got := o.Drain(0)
	if len(got) != 3 {
		t.Fatalf("expected ring of 3, got %d", len(got))
	}
	if got[0].TS != 3 || got[2].TS != 5 {
		t.Fatalf("expected oldest retained TS=3, newest=5, got %+v", got)
	}
}

func TestObserver_NDJSONRoundTrip(t *testing.T) {
	evt := ObserverEvent{
		TS:         1234567890123,
		Kind:       KindNet,
		Method:     "GET",
		URL:        "https://example.com/api",
		Status:     200,
		MimeType:   "application/json",
		Type:       "xhr",
		Size:       512,
		DurationMs: 42,
	}

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	if err := enc.Encode(evt); err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var got ObserverEvent
	if err := json.NewDecoder(&buf).Decode(&got); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if got.TS != evt.TS {
		t.Errorf("TS mismatch: got %d want %d", got.TS, evt.TS)
	}
	if got.Kind != evt.Kind {
		t.Errorf("Kind mismatch: got %q want %q", got.Kind, evt.Kind)
	}
	if got.URL != evt.URL {
		t.Errorf("URL mismatch: got %q want %q", got.URL, evt.URL)
	}
}

func TestObserver_WriteNDJSON(t *testing.T) {
	o := &Observer{
		opts:    ObserverOpts{BufferSize: 10},
		events:  make(chan ObserverEvent, 10),
		pending: make(map[proto.NetworkRequestID]*pendingNet),
		stopped: make(chan struct{}),
	}

	// Pre-populate the channel and close it to simulate a finished observer.
	for i := 0; i < 3; i++ {
		o.events <- ObserverEvent{TS: int64(i), Kind: KindPage, Event: "test"}
	}
	close(o.events)

	var buf bytes.Buffer
	ctx := context.Background()
	if err := o.WriteNDJSON(ctx, &buf); err != nil {
		t.Fatalf("WriteNDJSON: %v", err)
	}

	// Count lines
	lines := bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n"))
	if len(lines) != 3 {
		t.Errorf("expected 3 NDJSON lines, got %d", len(lines))
	}

	// Each line must be valid JSON
	for i, line := range lines {
		var e ObserverEvent
		if err := json.Unmarshal(line, &e); err != nil {
			t.Errorf("line %d invalid JSON: %v", i, err)
		}
	}
}

func TestObserver_StopDrainsGoroutines(t *testing.T) {
	// Verify that Stop returns within a reasonable timeout (no goroutine leak).
	// This test does NOT require Chrome.
	o := &Observer{
		opts:    ObserverOpts{BufferSize: 10},
		events:  make(chan ObserverEvent, 10),
		pending: make(map[proto.NetworkRequestID]*pendingNet),
		stopped: make(chan struct{}),
	}

	var wg sync.WaitGroup
	// Simulate the closer goroutine (normally started by Start).
	go func() {
		o.wg.Wait()
		o.stopOnce.Do(func() { close(o.events) })
		close(o.stopped)
	}()

	done := make(chan struct{})
	go func() {
		defer close(done)
		// With no wg.Add, Stop should return immediately.
		_ = o.Stop()
	}()

	select {
	case <-done:
		// ok
	case <-time.After(2 * time.Second):
		t.Error("Stop did not return within 2s — possible goroutine leak")
	}

	wg.Wait()
}

// TestObserver_Integration is an integration test that skips when Chrome is unavailable.
func TestObserver_Integration(t *testing.T) {
	t.Skip("integration test requires Chrome; run manually with ghostchrome serve")
}
