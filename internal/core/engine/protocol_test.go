package engine

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

func TestClassifyError(t *testing.T) {
	code, retry := ClassifyError(ErrStaleRef)
	if code != ErrCodeStaleRef || retry {
		t.Fatalf("stale: %s retry=%v", code, retry)
	}
	code, retry = ClassifyError(ErrAmbiguousRef)
	if code != ErrCodeAmbiguousRef || retry {
		t.Fatalf("ambiguous: %s retry=%v", code, retry)
	}
	code, retry = ClassifyError(context.DeadlineExceeded)
	if code != ErrCodeTimeout || !retry {
		t.Fatalf("timeout: %s retry=%v", code, retry)
	}
	code, retry = ClassifyError(errors.New("click: element is covered by <div#overlay> at its click point"))
	if code != ErrCodeHitTarget || !retry {
		t.Fatalf("hit: %s retry=%v", code, retry)
	}
}

func TestParseSnapshotMode(t *testing.T) {
	mode, err := ParseSnapshotMode("")
	if err != nil || mode != SnapshotModeDiff {
		t.Fatalf("default: %s %v", mode, err)
	}
	if _, err := ParseSnapshotMode("nope"); err == nil {
		t.Fatal("expected error")
	}
}

func TestDiffRefsUnchanged(t *testing.T) {
	prev := map[string]RefSnapshot{"@1": {Role: "button", Name: "Go"}}
	curr := map[string]RefSnapshot{"@1": {Role: "button", Name: "Go"}}
	d := DiffRefs(prev, curr)
	if !d.Unchanged || d.Stats.KeptCount != 1 {
		t.Fatalf("%+v", d)
	}
	if FormatDiff(d) != "unchanged" {
		t.Fatalf("format %q", FormatDiff(d))
	}
}

func TestDiffRefsAddedRemoved(t *testing.T) {
	prev := map[string]RefSnapshot{"@1": {Role: "link", Name: "A"}}
	curr := map[string]RefSnapshot{"@1": {Role: "link", Name: "B"}, "@2": {Role: "button", Name: "Go"}}
	d := DiffRefs(prev, curr)
	if d.Unchanged || d.Stats.ChangedCount != 1 || d.Stats.AddedCount != 1 {
		t.Fatalf("%+v", d)
	}
}

func TestActionableCoveredReason(t *testing.T) {
	err := fmt.Errorf("element is covered by <div#overlay> at its click point")
	code, retry := ClassifyError(err)
	if code != ErrCodeHitTarget || !retry {
		t.Fatalf("%s %v", code, retry)
	}
}
