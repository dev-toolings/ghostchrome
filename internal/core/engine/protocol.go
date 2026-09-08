package engine

import (
	"context"
	"errors"
	"os"
	"strings"
	"time"
)

// ProtocolVersion is the JSONL/MCP handshake version. Bump when the
// envelope or core result shapes change incompatibly.
const ProtocolVersion = 1

// OpError is a classified interaction/navigation failure. The JSONL
// envelope copies Code/Retryable so agents can retry without parsing
// English error strings.
type OpError struct {
	Code      string
	Retryable bool
	Err       error
}

func (e *OpError) Error() string {
	if e == nil || e.Err == nil {
		return "operation failed"
	}
	return e.Err.Error()
}

func (e *OpError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

const (
	ErrCodeStaleRef     = "stale_ref"
	ErrCodeAmbiguousRef = "ambiguous_ref"
	ErrCodeHitTarget    = "hit_target"
	ErrCodeTimeout      = "timeout"
	ErrCodeActionable   = "not_actionable"
	ErrCodePolicy       = "policy"
	ErrCodeUnknown      = "error"
)

// ClassifyError maps a failure to a stable code + retry hint.
func ClassifyError(err error) (code string, retryable bool) {
	if err == nil {
		return "", false
	}
	var op *OpError
	if errors.As(err, &op) && op != nil && op.Code != "" {
		return op.Code, op.Retryable
	}
	switch {
	case errors.Is(err, ErrStaleRef):
		return ErrCodeStaleRef, false
	case errors.Is(err, ErrAmbiguousRef):
		return ErrCodeAmbiguousRef, false
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, os.ErrDeadlineExceeded):
		return ErrCodeTimeout, true
	}

	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "covered by"):
		return ErrCodeHitTarget, true
	case strings.Contains(msg, "actionability"):
		return ErrCodeActionable, true
	case strings.Contains(msg, "timeout") || strings.Contains(msg, "deadline") || strings.Contains(msg, "timed out"):
		return ErrCodeTimeout, true
	case strings.Contains(msg, "policy"):
		return ErrCodePolicy, false
	}
	return ErrCodeUnknown, false
}

// NavWaitTimeout is the upper bound for a pre-armed lifecycle wait.
// Navigation is allowed to exceed DefaultActionTimeout (clicks/types)
// because a cold page can easily take >8s to reach load.
const NavWaitTimeout = 30 * time.Second
