// Package dashboard provides a live browser viewport stream over WebSocket.
// It captures CDP screencast frames and broadcasts them to connected clients,
// plus an activity feed of navigation and interaction events.
package dashboard

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/go-rod/rod"
)

// Dashboard wraps the HTTP server and screencast streamer.
type Dashboard struct {
	srv          *server
	stream       *streamer
	mu           sync.Mutex
	cancel       context.CancelFunc
	artifactPath string
}

// Options configure optional human-collaboration features.
type Options struct {
	Annotate     bool
	ArtifactPath string
}

// Start begins screencasting and serving the dashboard UI on the given port.
// Returns the URL where the dashboard is accessible.
func Start(page *rod.Page, port int) (*Dashboard, string, error) {
	return StartWithOptions(page, port, Options{})
}

// StartWithOptions begins screencasting with optional annotation capture.
func StartWithOptions(page *rod.Page, port int, opts Options) (*Dashboard, string, error) {
	ctx, cancel := context.WithCancel(context.Background())

	str, err := newStreamer(ctx, page)
	if err != nil {
		cancel()
		return nil, "", fmt.Errorf("streamer: %w", err)
	}

	if opts.Annotate && opts.ArtifactPath == "" {
		base, cacheErr := os.UserCacheDir()
		if cacheErr != nil {
			base = os.TempDir()
		}
		opts.ArtifactPath = filepath.Join(base, "ghostchrome", "dashboard", fmt.Sprintf("annotations-%d.json", time.Now().UTC().UnixMilli()))
	}
	srv, addr, err := newServer(port, str, opts)
	if err != nil {
		cancel()
		return nil, "", fmt.Errorf("server: %w", err)
	}

	go srv.serve(ctx)
	if opts.Annotate {
		addr += "?annotate=1"
	}

	return &Dashboard{srv: srv, stream: str, cancel: cancel, artifactPath: opts.ArtifactPath}, addr, nil
}

// AnnotationArtifactPath returns the JSON result artifact when annotations are enabled.
func (d *Dashboard) AnnotationArtifactPath() string {
	if d == nil {
		return ""
	}
	return d.artifactPath
}

// PushEvent sends a text event to all connected dashboard clients.
func (d *Dashboard) PushEvent(text string) {
	if d == nil || d.stream == nil {
		return
	}
	d.stream.pushEvent(text)
}

// Stop shuts down the dashboard server and stops screencasting.
func (d *Dashboard) Stop() {
	if d == nil {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.cancel != nil {
		d.cancel()
		d.cancel = nil
	}
	if d.stream != nil {
		d.stream.stop()
	}
}
