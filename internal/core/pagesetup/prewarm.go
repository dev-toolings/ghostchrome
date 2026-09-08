package pagesetup

import (
	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

// PrewarmDomains enables the CDP domains the engine will need lazily on its
// first call (Accessibility, DOM, Page lifecycle events). Each enable is
// idempotent and cheap, so calling this at session start removes the
// per-domain round-trip from the first extract / navigate / screenshot.
//
// Best-effort: failures are silently ignored — every consumer re-enables the
// domain it needs anyway.
func PrewarmDomains(page *rod.Page) {
	_ = proto.AccessibilityEnable{}.Call(page)
	_ = proto.DOMEnable{}.Call(page)
	_ = proto.PageSetLifecycleEventsEnabled{Enabled: true}.Call(page)
	_ = proto.NetworkEnable{}.Call(page)
}
