package engine

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

const (
	continuousLogKeep    = 1000
	continuousLogRewrite = 1125
	continuousValueLimit = 64 * 1024
	continuousClearEvent = "clear"
)

// PersistentSessionObserver keeps CDP observation alive in the transparent
// daemon, including the idle time between short CLI processes.
type PersistentSessionObserver struct {
	browser *rod.Browser
	path    string

	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	mu      sync.Mutex
	ring    []ObserverEvent
	size    int64
	logInfo os.FileInfo
	pages   map[proto.TargetTargetID]*Observer
}

// StartPersistentSessionObserver connects a second CDP client to the daemon
// Chrome and appends bounded network/console events to its session log.
func StartPersistentSessionObserver(connectURL string) (*PersistentSessionObserver, error) {
	path, err := continuousObserverLogPath(connectURL)
	if err != nil {
		return nil, err
	}
	browser, err := connectPersistentObserverBrowser(connectURL, 15*time.Second, 0, nil)
	if err != nil {
		return nil, fmt.Errorf("connect continuous observer: %w", err)
	}
	if err := ensureContinuousLog(path); err != nil {
		return nil, fmt.Errorf("open continuous observer log: %w", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	p := &PersistentSessionObserver{
		browser: browser,
		path:    path,
		ctx:     ctx,
		cancel:  cancel,
		pages:   make(map[proto.TargetTargetID]*Observer),
	}
	if info, statErr := os.Stat(path); statErr == nil {
		p.size = info.Size()
		p.logInfo = info
	}
	p.ring, _ = readContinuousEvents(path)
	p.wg.Add(1)
	go p.monitorPages()
	return p, nil
}

var connectPersistentObserverBrowser = connectRodBrowser

// Stop detaches observers. Chrome itself remains owned by serve and is not
// closed through this secondary CDP connection.
func (p *PersistentSessionObserver) Stop() {
	if p == nil {
		return
	}
	p.cancel()
	p.wg.Wait()
}

func (p *PersistentSessionObserver) monitorPages() {
	defer p.wg.Done()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		p.attachNewPages()
		select {
		case <-p.ctx.Done():
			for _, observer := range p.pages {
				_ = observer.Stop()
			}
			return
		case <-ticker.C:
		}
	}
}

func (p *PersistentSessionObserver) attachNewPages() {
	pages, err := p.browser.Pages()
	if err != nil {
		return
	}
	live := make(map[proto.TargetTargetID]struct{}, len(pages))
	for _, page := range pages {
		id := page.TargetID
		live[id] = struct{}{}
		if _, exists := p.pages[id]; exists {
			continue
		}
		observer := NewObserver(page, ObserverOpts{BufferSize: 1024})
		if err := observer.Start(p.ctx); err != nil {
			continue
		}
		p.pages[id] = observer
		p.wg.Add(1)
		go func(obs *Observer) {
			defer p.wg.Done()
			for {
				select {
				case <-p.ctx.Done():
					return
				case event, ok := <-obs.Events():
					if !ok {
						return
					}
					_ = p.appendEvent(event)
				}
			}
		}(observer)
	}
	for id, observer := range p.pages {
		if _, ok := live[id]; ok {
			continue
		}
		_ = observer.Stop()
		delete(p.pages, id)
	}
}

func (p *PersistentSessionObserver) appendEvent(event ObserverEvent) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	event = boundContinuousEvent(event)
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	data = append(data, '\n')

	return withContinuousLogLock(p.path, func() error {
		info, err := os.Stat(p.path)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		if info == nil || p.logInfo == nil || !os.SameFile(info, p.logInfo) || info.Size() != p.size {
			// A CLI clear marker or another daemon writer changed the current
			// generation. Reload while holding the same lock before appending so
			// compaction cannot resurrect cleared events.
			p.ring, err = readContinuousEventsUnlocked(p.path)
			if err != nil {
				return err
			}
			if info != nil {
				p.size = info.Size()
			}
		}

		p.ring = append(p.ring, event)
		if len(p.ring) >= continuousLogRewrite {
			p.ring = append([]ObserverEvent(nil), p.ring[len(p.ring)-continuousLogKeep:]...)
			if err := writeContinuousEventsUnlocked(p.path, p.ring); err != nil {
				return err
			}
		} else if err := appendContinuousLogDataUnlocked(p.path, data); err != nil {
			return err
		}

		info, err = os.Stat(p.path)
		if err != nil {
			return err
		}
		p.size = info.Size()
		p.logInfo = info
		return nil
	})
}

func continuousObserverLogPath(connectURL string) (string, error) {
	statePath, err := sessionStatePath(connectURL)
	if err != nil {
		return "", err
	}
	return statePath + ".events.ndjson", nil
}

func readContinuousEvents(path string) ([]ObserverEvent, error) {
	var events []ObserverEvent
	err := withContinuousLogLock(path, func() error {
		var err error
		events, err = readContinuousEventsUnlocked(path)
		return err
	})
	return events, err
}

func readContinuousEventsUnlocked(path string) ([]ObserverEvent, error) {
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer file.Close()
	var events []ObserverEvent
	scanner := bufio.NewScanner(file)
	// Response bodies may legitimately exceed Scanner's small default token.
	scanner.Buffer(make([]byte, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		var event ObserverEvent
		if json.Unmarshal(scanner.Bytes(), &event) != nil {
			continue
		}
		if event.Event == continuousClearEvent {
			events = filterContinuousEvents(events, event.Kind)
			continue
		}
		events = append(events, event)
		if len(events) > continuousLogRewrite {
			events = append([]ObserverEvent(nil), events[len(events)-continuousLogKeep:]...)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if len(events) > continuousLogKeep {
		events = events[len(events)-continuousLogKeep:]
	}
	return events, nil
}

func writeContinuousEvents(path string, events []ObserverEvent) error {
	return withContinuousLogLock(path, func() error {
		return writeContinuousEventsUnlocked(path, events)
	})
}

func writeContinuousEventsUnlocked(path string, events []ObserverEvent) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".events-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	enc := json.NewEncoder(tmp)
	for _, event := range events {
		if err := enc.Encode(event); err != nil {
			_ = tmp.Close()
			return err
		}
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return replaceContinuousLog(name, path)
}

func appendContinuousClear(path string, kind ObserverKind) error {
	if path == "" {
		return nil
	}
	data, err := json.Marshal(ObserverEvent{
		TS:    time.Now().UnixMilli(),
		Kind:  kind,
		Event: continuousClearEvent,
	})
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return withContinuousLogLock(path, func() error {
		return appendContinuousLogDataUnlocked(path, data)
	})
}

func ensureContinuousLog(path string) error {
	return withContinuousLogLock(path, func() error {
		file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			return err
		}
		return file.Close()
	})
}

func appendContinuousLogData(path string, data []byte) error {
	return withContinuousLogLock(path, func() error {
		return appendContinuousLogDataUnlocked(path, data)
	})
}

func appendContinuousLogDataUnlocked(path string, data []byte) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = file.Write(data)
	return err
}

func withContinuousLogLock(path string, fn func() error) error {
	return lockContinuousLog(path, fn)
}

func filterContinuousEvents(events []ObserverEvent, cleared ObserverKind) []ObserverEvent {
	out := events[:0]
	for _, event := range events {
		remove := event.Kind == cleared
		if cleared == KindConsole {
			remove = event.Kind == KindConsole || event.Kind == KindError
		}
		if !remove {
			out = append(out, event)
		}
	}
	return out
}

func boundContinuousEvent(event ObserverEvent) ObserverEvent {
	event.Body = boundContinuousValue(event.Body)
	event.PostData = boundContinuousValue(event.PostData)
	event.Text = boundContinuousValue(event.Text)
	return event

}

func boundContinuousValue(value string) string {
	if len(value) <= continuousValueLimit {
		return value
	}
	return value[:continuousValueLimit] + "\n[ghostchrome: value truncated]"
}
