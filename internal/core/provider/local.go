package provider

import (
	"context"
	"fmt"
	"sync"

	"github.com/dev-toolings/ghostchrome/engine"
)

// Local provisions Chrome by launching a local process via Rod's launcher.
type Local struct{}

func (Local) Name() string { return "local" }

func (Local) Connect(_ context.Context, opts ConnectOpts) (string, func(), error) {
	l := engine.NewLauncher(engine.LauncherOpts{
		Headless:    opts.Headless,
		Proxy:       opts.Proxy,
		UserDataDir: opts.UserDataDir,
	})
	removeProfile := engine.LauncherOwnsRodTempProfile(l, opts.UserDataDir, nil)

	wsURL, err := l.Launch()
	if err != nil {
		engine.CleanupFailedLauncher(l, removeProfile)
		return "", nil, fmt.Errorf("local launcher: %w", err)
	}

	var once sync.Once
	cleanup := func() {
		once.Do(func() {
			engine.CleanupLauncher(l, removeProfile)
		})
	}
	return wsURL, cleanup, nil
}
