package pagesetup

import (
	"fmt"
	"strings"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

// ApplyServiceWorkersMode applies the subset of Playwright's
// contextOptions.serviceWorkers that CDP can honor directly.
func ApplyServiceWorkersMode(page *rod.Page, mode string) error {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", "allow":
		return nil
	case "block":
	default:
		return fmt.Errorf("serviceWorkers: expected allow|block, got %q", mode)
	}
	if err := (proto.NetworkEnable{}).Call(page); err != nil {
		return fmt.Errorf("network enable: %w", err)
	}
	if err := (proto.NetworkSetBypassServiceWorker{Bypass: true}).Call(page); err != nil {
		return fmt.Errorf("bypass service worker: %w", err)
	}
	_, err := page.EvalOnNewDocument(`(() => {
	const err = () => new DOMException('Service workers are blocked by ghostchrome config', 'SecurityError');
	if (globalThis.ServiceWorkerContainer && ServiceWorkerContainer.prototype) {
		try {
			Object.defineProperty(ServiceWorkerContainer.prototype, 'register', {
				value: function register() { return Promise.reject(err()); },
				configurable: true
			});
		} catch (_) {}
	}
	if (navigator.serviceWorker && typeof navigator.serviceWorker.getRegistrations === 'function') {
		navigator.serviceWorker.getRegistrations()
			.then((registrations) => registrations.forEach((registration) => registration.unregister()))
			.catch(() => {});
	}
})();`)
	if err != nil {
		return fmt.Errorf("service worker init script: %w", err)
	}
	return nil
}
