package engine

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-rod/rod"
)

func TestPersistentObserverFailedStartDoesNotCreateLog(t *testing.T) {
	connectURL := "ws://persistent-observer-failure.test/devtools/browser/test"
	path, err := continuousObserverLogPath(connectURL)
	if err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(path)
	_ = os.Remove(path + ".lock")
	t.Cleanup(func() {
		_ = os.Remove(path)
		_ = os.Remove(path + ".lock")
	})

	original := connectPersistentObserverBrowser
	connectPersistentObserverBrowser = func(string, time.Duration, int, map[string]string) (*rod.Browser, error) {
		return nil, fmt.Errorf("unavailable")
	}
	t.Cleanup(func() { connectPersistentObserverBrowser = original })

	if _, err := StartPersistentSessionObserver(connectURL); err == nil {
		t.Fatal("expected failed observer start")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("failed observer start created an active log: %v", err)
	}
}

func TestContinuousEventsApplyTypedClearMarkers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.ndjson")
	events := []ObserverEvent{
		{TS: 1, Kind: KindConsole, Text: "before"},
		{TS: 2, Kind: KindError, Text: "error before"},
		{TS: 3, Kind: KindNet, URL: "https://before.test"},
	}
	if err := writeContinuousEvents(path, events); err != nil {
		t.Fatal(err)
	}
	if err := appendContinuousClear(path, KindConsole); err != nil {
		t.Fatal(err)
	}
	if err := appendContinuousLogData(path, []byte(`{"ts":4,"kind":"console","text":"after"}`+"\n")); err != nil {
		t.Fatal(err)
	}

	got, err := readContinuousEvents(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Kind != KindNet || got[1].Text != "after" {
		t.Fatalf("typed clear should retain network and later console events: %#v", got)
	}
}

func TestContinuousEventValuesAreBounded(t *testing.T) {
	event := boundContinuousEvent(ObserverEvent{
		Body:     strings.Repeat("b", continuousValueLimit+1),
		PostData: strings.Repeat("p", continuousValueLimit+1),
		Text:     strings.Repeat("t", continuousValueLimit+1),
	})
	for name, value := range map[string]string{"body": event.Body, "post": event.PostData, "text": event.Text} {
		if !strings.HasSuffix(value, "[ghostchrome: value truncated]") {
			t.Fatalf("%s was not truncated: len=%d", name, len(value))
		}
	}
}

func TestContinuousObserverCompactionPreservesInterleavedTypedClear(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.ndjson")
	initial := make([]ObserverEvent, 0, continuousLogRewrite-1)
	initial = append(initial, ObserverEvent{TS: 1, Kind: KindConsole, Text: "old console"})
	for i := 1; i < continuousLogRewrite-1; i++ {
		initial = append(initial, ObserverEvent{TS: int64(i + 1), Kind: KindNet, URL: "https://before.test"})
	}
	if err := writeContinuousEvents(path, initial); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	// This is the daemon's in-memory ring immediately before compaction. A
	// separate CLI process will clear console entries after the rename and
	// before the daemon's next append.
	p := &PersistentSessionObserver{
		path:    path,
		ring:    append([]ObserverEvent(nil), initial...),
		size:    info.Size(),
		logInfo: info,
	}
	if err := p.appendEvent(ObserverEvent{TS: 2000, Kind: KindConsole, Text: "clear me"}); err != nil {
		t.Fatalf("daemon compaction append: %v", err)
	}
	compactedInfo, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if os.SameFile(info, compactedInfo) {
		t.Fatal("expected compaction to publish a new log generation")
	}

	if err := appendContinuousClear(path, KindConsole); err != nil {
		t.Fatalf("CLI typed clear: %v", err)
	}
	if err := p.appendEvent(ObserverEvent{TS: 2001, Kind: KindNet, URL: "https://after.test"}); err != nil {
		t.Fatalf("daemon append after CLI clear: %v", err)
	}

	got, err := readContinuousEvents(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != continuousLogKeep {
		t.Fatalf("expected bounded log of %d events, got %d", continuousLogKeep, len(got))
	}
	for _, event := range got {
		if event.Kind == KindConsole || event.Kind == KindError {
			t.Fatalf("console clear was resurrected after compaction: %#v", event)
		}
	}
	if got[len(got)-1].URL != "https://after.test" {
		t.Fatalf("daemon append used a stale generation: last event %#v", got[len(got)-1])
	}
}
