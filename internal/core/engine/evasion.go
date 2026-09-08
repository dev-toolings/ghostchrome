package engine

import "sync/atomic"

// evadeRuntime, when true, makes ghostchrome avoid subscribing to CDP
// Runtime.* events. go-rod auto-sends `Runtime.enable` the first time any
// Runtime event is subscribed (via EachEvent reflection); anti-bot vendors
// (DataDome, PerimeterX, ...) treat a live `Runtime.enable` as an automation
// tell. Skipping those subscriptions is the Go-native equivalent of the
// rebrowser/patchright "Runtime.enable leak" mitigation.
//
// Opt-in and off by default: enabling it trades console-error/exception
// capture (snapshot's JS-error channel, the observer's console stream) for a
// smaller automation fingerprint. Network, Log and Page domains stay active,
// so network/CSP errors are still collected.
var evadeRuntime atomic.Bool

// SetEvadeRuntimeEnable toggles Runtime.enable evasion process-wide. Wired
// from the root `--evade-runtime` flag / GHOSTCHROME_EVADE_RUNTIME env.
func SetEvadeRuntimeEnable(on bool) { evadeRuntime.Store(on) }

// EvadeRuntimeEnable reports whether Runtime.* subscriptions must be skipped.
func EvadeRuntimeEnable() bool { return evadeRuntime.Load() }
