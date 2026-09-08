package dashboard

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"sync"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

// wsMessage is sent to dashboard clients over WebSocket.
type wsMessage struct {
	Type    string `json:"type"`
	Data    string `json:"data,omitempty"`
	FrameID uint64 `json:"frame_id,omitempty"`
	Event   string `json:"event,omitempty"`
}

const retainedDashboardFrames = 60

type streamer struct {
	page    *rod.Page
	mu      sync.RWMutex
	clients map[chan []byte]struct{}
	cancel  context.CancelFunc
	lastID  uint64
	frames  map[uint64][]byte
	order   []uint64
}

func newStreamer(ctx context.Context, page *rod.Page) (*streamer, error) {
	ctx, cancel := context.WithCancel(ctx)
	s := &streamer{
		page:    page,
		clients: make(map[chan []byte]struct{}),
		frames:  make(map[uint64][]byte),
		cancel:  cancel,
	}

	err := proto.PageStartScreencast{
		Format:        proto.PageStartScreencastFormatJpeg,
		Quality:       intPtr(40),
		MaxWidth:      intPtr(1280),
		MaxHeight:     intPtr(720),
		EveryNthFrame: intPtr(3),
	}.Call(page)
	if err != nil {
		cancel()
		return nil, err
	}

	go s.captureLoop(ctx)
	return s, nil
}

func (s *streamer) captureLoop(ctx context.Context) {
	events := s.page.EachEvent(func(e *proto.PageScreencastFrame) bool {
		_ = proto.PageScreencastFrameAck{SessionID: e.SessionID}.Call(s.page)
		frameID := s.storeFrame(e.Data)
		b64 := base64.StdEncoding.EncodeToString(e.Data)
		msg := wsMessage{Type: "frame", Data: b64, FrameID: frameID}
		data, _ := json.Marshal(msg)
		s.broadcast(data)
		return ctx.Err() != nil
	})
	go func() {
		<-ctx.Done()
		events()
	}()
}

func (s *streamer) storeFrame(data []byte) uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.frames == nil {
		s.frames = make(map[uint64][]byte)
	}
	s.lastID++
	id := s.lastID
	s.frames[id] = append([]byte(nil), data...)
	s.order = append(s.order, id)
	if len(s.order) > retainedDashboardFrames {
		old := s.order[0]
		s.order = s.order[1:]
		delete(s.frames, old)
	}
	return id
}

func (s *streamer) frame(id uint64) ([]byte, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	data, ok := s.frames[id]
	return append([]byte(nil), data...), ok
}

func (s *streamer) subscribe() chan []byte {
	ch := make(chan []byte, 16)
	s.mu.Lock()
	s.clients[ch] = struct{}{}
	s.mu.Unlock()
	return ch
}

func (s *streamer) unsubscribe(ch chan []byte) {
	s.mu.Lock()
	delete(s.clients, ch)
	s.mu.Unlock()
	close(ch)
}

func (s *streamer) broadcast(data []byte) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for ch := range s.clients {
		select {
		case ch <- data:
		default:
		}
	}
}

func (s *streamer) pushEvent(text string) {
	msg := wsMessage{Type: "event", Event: text}
	data, _ := json.Marshal(msg)
	s.broadcast(data)
}

func (s *streamer) stop() {
	if s.cancel != nil {
		s.cancel()
	}
	_ = proto.PageStopScreencast{}.Call(s.page)
}

func intPtr(v int) *int { return &v }
