package feedback

import (
	"errors"
	"fmt"
	"time"

	"github.com/dev-toolings/ghostchrome/internal/core/engine"
	"github.com/go-rod/rod"
)

// RecoveryContext carries the inputs available to a RecoveryHook.
type RecoveryContext struct {
	// Page is the active Rod page.
	Page *rod.Page

	// Err is the error returned by the original op.
	Err error

	// OpName is the JSONL op name for logging (e.g. "click", "navigate").
	OpName string
}

// RecoveryHook is a function that inspects the failure context and optionally
// performs a repair action.
//
// Return values:
//   - retry=true: the hook resolved the issue; the caller should retry the op.
//   - retry=false, err=nil: the hook ran but did not fix anything; try next hook.
//   - retry=false, err=non-nil: the hook itself failed with a new error; abort.
type RecoveryHook func(ctx RecoveryContext) (retry bool, err error)

// RecoveryChain runs hooks in order until one returns retry=true or all are
// exhausted. Returns (true, nil) if a hook signalled recovery, (false, nil)
// if no hook could help, or (false, err) if a hook returned an error.
func RecoveryChain(ctx RecoveryContext, hooks []RecoveryHook) (retry bool, err error) {
	for _, h := range hooks {
		if h == nil {
			continue
		}
		r, e := h(ctx)
		if e != nil {
			return false, e
		}
		if r {
			return true, nil
		}
	}
	return false, nil
}

// ----------------------------------------------------------------------------
// Built-in hooks
// ----------------------------------------------------------------------------

// RecoverStaleRef handles ErrStaleRef. Per the spec: it must NEVER silently
// remap refs. Instead it returns retry=false with an informative error so the
// agent can decide to run an explicit re-extract op.
func RecoverStaleRef(ctx RecoveryContext) (bool, error) {
	if !errors.Is(ctx.Err, engine.ErrStaleRef) {
		return false, nil
	}
	// Do not retry — force the agent to re-extract explicitly.
	return false, fmt.Errorf("stale ref in op %q: re-run extract before retrying — %w", ctx.OpName, ctx.Err)
}

// RecoverDialogAccept handles the case where a JS dialog is blocking the page.
// It accepts the dialog and signals retry=true so the op can be retried.
func RecoverDialogAccept(ctx RecoveryContext) (bool, error) {
	// Only trigger when there is an actual dialog open.
	// We use a zero-timeout probe: if no dialog appears immediately, skip.
	result, err := engine.HandleNextDialog(ctx.Page, true, "", 300*time.Millisecond)
	if err != nil {
		return false, nil // dialog probe error → not our problem
	}
	if result == nil || result.TimedOut {
		return false, nil // no dialog
	}
	// Dialog was accepted — signal retry.
	return true, nil
}

// RecoverBotChallenge handles DataDome 403 / Cloudflare 503 bot-challenge
// pages. It waits up to 10s for the challenge to resolve, then signals retry.
func RecoverBotChallenge(ctx RecoveryContext) (bool, error) {
	hint := detectCaptchaHint(ctx.Page)
	if hint == "" {
		return false, nil
	}
	resolved := engine.WaitForBotChallenge(ctx.Page, 10*time.Second)
	if !resolved {
		return false, fmt.Errorf("bot challenge (%s) not resolved after 10s", hint)
	}
	return true, nil
}

// RecoverNetworkSettle handles transient network timeouts by waiting 2s for
// the network to become idle, then signalling retry.
func RecoverNetworkSettle(ctx RecoveryContext) (bool, error) {
	// Heuristic: only trigger on timeout-flavoured errors.
	if ctx.Err == nil {
		return false, nil
	}
	msg := ctx.Err.Error()
	if !containsAny(msg, "timeout", "timed out", "deadline exceeded", "net::ERR_") {
		return false, nil
	}
	// Wait up to 2s for network idle.
	ctx.Page.WaitRequestIdle(2*time.Second, nil, nil, nil)()
	return true, nil
}

// DefaultRecoveryHooks returns the standard set of hooks in priority order:
//  1. RecoverDialogAccept — dialogs block everything
//  2. RecoverBotChallenge — captcha/CF interstitial
//  3. RecoverNetworkSettle — transient timeout
//  4. RecoverStaleRef — stale ref is last (never silently remaps)
func DefaultRecoveryHooks() []RecoveryHook {
	return []RecoveryHook{
		RecoverDialogAccept,
		RecoverBotChallenge,
		RecoverNetworkSettle,
		RecoverStaleRef,
	}
}

// ----------------------------------------------------------------------------
// helpers
// ----------------------------------------------------------------------------

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if len(s) >= len(sub) {
			// simple linear scan; avoid importing strings for a trivial check
			for i := 0; i <= len(s)-len(sub); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
		}
	}
	return false
}
