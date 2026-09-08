// Package provider defines the interface for connecting to a Chrome instance.
// The default Local provider wraps the existing launcher. Future cloud
// providers (Browserless, Browserbase) implement the same interface.
package provider

import "context"

// ConnectOpts carries the parameters any provider needs to set up a connection.
type ConnectOpts struct {
	Headless    bool
	Proxy       string
	UserDataDir string
	TimeoutSec  int
}

// Provider abstracts Chrome acquisition. A provider returns a WebSocket URL
// the caller can pass to rod.New().ControlURL(), plus a cleanup function
// that releases whatever the provider allocated (kill process, release slot, …).
type Provider interface {
	// Name returns a human-readable identifier (e.g. "local", "browserless").
	Name() string

	// Connect provisions a Chrome instance and returns the DevTools WS URL.
	// The cleanup function MUST be called when the browser is no longer needed.
	Connect(ctx context.Context, opts ConnectOpts) (wsURL string, cleanup func(), err error)
}
