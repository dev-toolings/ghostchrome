package media

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

type ScreenRecorder struct {
	page    *rod.Page
	outDir  string
	quality int
	maxW    int
	maxH    int
	nth     int
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	count   atomic.Int64
	startAt time.Time
}

type ScreenRecorderOpts struct {
	OutputDir string
	Quality   int
	MaxWidth  int
	MaxHeight int
	NthFrame  int
}

func NewScreenRecorder(page *rod.Page, opts ScreenRecorderOpts) *ScreenRecorder {
	if opts.Quality <= 0 {
		opts.Quality = 60
	}
	if opts.MaxWidth <= 0 {
		opts.MaxWidth = 1280
	}
	if opts.MaxHeight <= 0 {
		opts.MaxHeight = 720
	}
	if opts.NthFrame <= 0 {
		// A video runtime must retain every renderer frame. Sampling every
		// second frame made short, cross-process recordings look frozen.
		opts.NthFrame = 1
	}
	return &ScreenRecorder{
		page:    page,
		outDir:  opts.OutputDir,
		quality: opts.Quality,
		maxW:    opts.MaxWidth,
		maxH:    opts.MaxHeight,
		nth:     opts.NthFrame,
	}
}

func (r *ScreenRecorder) Start() error {
	if err := os.MkdirAll(r.outDir, 0o700); err != nil {
		return fmt.Errorf("screenrecord: mkdir: %w", err)
	}
	q := r.quality
	w := r.maxW
	h := r.maxH
	n := r.nth
	err := proto.PageStartScreencast{
		Format:        proto.PageStartScreencastFormatJpeg,
		Quality:       &q,
		MaxWidth:      &w,
		MaxHeight:     &h,
		EveryNthFrame: &n,
	}.Call(r.page)
	if err != nil {
		return fmt.Errorf("screenrecord: start: %w", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	r.cancel = cancel
	r.startAt = time.Now()
	r.wg.Add(1)
	go r.captureLoop(ctx)
	return nil
}

func (r *ScreenRecorder) captureLoop(ctx context.Context) {
	defer r.wg.Done()
	page, cancel := r.page.WithCancel()
	defer cancel()
	go func() {
		<-ctx.Done()
		cancel()
	}()
	page.EachEvent(func(e *proto.PageScreencastFrame) bool {
		_ = proto.PageScreencastFrameAck{SessionID: e.SessionID}.Call(r.page)
		idx := r.count.Add(1)
		path := filepath.Join(r.outDir, fmt.Sprintf("frame-%06d.jpg", idx))
		data, err := base64.StdEncoding.DecodeString(string(e.Data))
		if err != nil {
			data = e.Data
		}
		_ = os.WriteFile(path, data, 0o600)
		return ctx.Err() != nil
	})()
}

func (r *ScreenRecorder) Stop() int64 {
	if r.cancel != nil {
		r.cancel()
	}
	_ = proto.PageStopScreencast{}.Call(r.page)
	r.wg.Wait()
	return r.count.Load()
}

func (r *ScreenRecorder) FrameCount() int64 {
	return r.count.Load()
}

func (r *ScreenRecorder) OutputDir() string {
	return r.outDir
}
