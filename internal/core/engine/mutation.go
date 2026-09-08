package engine

import (
	"fmt"
	"strings"
	"time"

	"github.com/go-rod/rod"
)

const defaultImminentDOMTimeout = 80 * time.Millisecond

// SnapshotMode controls post-mutation output.
type SnapshotMode string

const (
	SnapshotModeNone SnapshotMode = "none"
	SnapshotModeDiff SnapshotMode = "diff"
	SnapshotModeFull SnapshotMode = "full"
)

// ParseSnapshotMode accepts none/diff/full. Empty defaults to diff.
func ParseSnapshotMode(s string) (SnapshotMode, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "diff":
		return SnapshotModeDiff, nil
	case "none":
		return SnapshotModeNone, nil
	case "full":
		return SnapshotModeFull, nil
	default:
		return "", fmt.Errorf("invalid snapshot mode %q: use none, diff, or full", s)
	}
}

// CaptureMutation invalidates the extract cache, takes a skeleton snapshot,
// persists refs, and returns the compact diff against prev.
func CaptureMutation(b *Browser, page *rod.Page, prev *PageSnapshot) (SnapshotDiff, *ExtractionResult, error) {
	if page == nil {
		return SnapshotDiff{Unchanged: true}, nil, nil
	}
	if b != nil {
		_ = b.InvalidateCachedExtract(page)
	}
	diff, result, err := extractMutation(page, prev)
	if err != nil {
		return SnapshotDiff{Unchanged: true}, nil, err
	}
	// Most browser actions update the DOM synchronously. Returning that result
	// immediately keeps high-volume agent loops fast while retaining a bounded
	// second chance for delayed XHR/framework updates.
	if !diff.Unchanged {
		if b != nil {
			_ = b.SaveSnapshot(page, result)
		}
		return diff, result, nil
	}

	_ = waitForImminentDOM(page, defaultImminentDOMTimeout)
	diff, result, err = extractMutation(page, prev)
	if err != nil {
		return SnapshotDiff{Unchanged: true}, nil, err
	}
	if b != nil {
		_ = b.SaveSnapshot(page, result)
	}
	return diff, result, nil
}

func extractMutation(page *rod.Page, prev *PageSnapshot) (SnapshotDiff, *ExtractionResult, error) {
	result, err := Extract(page, LevelSkeleton, "", false)
	if err != nil {
		return SnapshotDiff{Unchanged: true}, nil, err
	}
	curr, err := BuildSnapshot(page, result)
	if err != nil {
		return SnapshotDiff{Unchanged: true}, result, err
	}
	var prevRefs map[string]RefSnapshot
	if prev != nil {
		prevRefs = prev.Refs
	}
	return DiffRefs(prevRefs, curr.Refs), result, nil
}

// WaitForImminentDOM briefly gives an action-triggered DOM update a chance to
// land. It is intended only for post-mutation snapshot paths.
func WaitForImminentDOM(page *rod.Page, timeout time.Duration) error {
	return waitForImminentDOM(page, timeout)
}

// waitForImminentDOM resolves after the first DOM mutation or timeout. Waiting
// for the bounded timeout on a static page also covers delayed XHR callbacks;
// animation frames alone can run before their response arrives on fast hosts.
func waitForImminentDOM(page *rod.Page, timeout time.Duration) error {
	if page == nil {
		return nil
	}
	if timeout <= 0 {
		timeout = defaultImminentDOMTimeout
	}

	_, err := page.Timeout(timeout+time.Second).Eval(`(timeout) => new Promise((resolve) => {
		let settled = false;
		let timer;
		let observer;
		const finish = () => {
			if (settled) return;
			settled = true;
			observer.disconnect();
			clearTimeout(timer);
			resolve();
		};
		observer = new MutationObserver(finish);
		observer.observe(document, {
			childList: true,
			attributes: true,
			characterData: true,
			subtree: true,
		});
		timer = setTimeout(finish, timeout);
	})`, timeout.Milliseconds())
	return err
}
