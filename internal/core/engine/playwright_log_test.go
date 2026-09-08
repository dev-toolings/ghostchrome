package engine

import "testing"

func TestAppendBoundedConsole(t *testing.T) {
	existing := []ObserverEvent{{Text: "old"}}
	incoming := []ObserverEvent{{Text: "one"}, {Text: "two"}}
	got := appendBoundedConsole(existing, incoming, 2)
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].Text != "one" || got[1].Text != "two" {
		t.Fatalf("unexpected console log trim: %#v", got)
	}
}

func TestTrimNetworkLog(t *testing.T) {
	entries := []CapturedEntry{{URL: "old"}, {URL: "one"}, {URL: "two"}}
	got := trimNetworkLog(entries, 2)
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].URL != "one" || got[1].URL != "two" {
		t.Fatalf("unexpected network log trim: %#v", got)
	}
}
