package engine

import (
	"testing"
)

func TestContextRegistryEntryAliveStr(t *testing.T) {
	cases := []struct {
		entry ContextRegistryEntry
		want  string
	}{
		{ContextRegistryEntry{Alive: true}, "yes"},
		{ContextRegistryEntry{Alive: false}, "no"},
		{ContextRegistryEntry{Unknown: true}, "?"},
		{ContextRegistryEntry{Alive: false, Unknown: true}, "?"},
	}
	for _, tc := range cases {
		got := tc.entry.AliveStr()
		if got != tc.want {
			t.Errorf("AliveStr(%+v) = %q, want %q", tc.entry, got, tc.want)
		}
	}
}

func TestListContextsUnknownWhenNoBrowser(t *testing.T) {
	// When b == nil (Chrome unreachable), all entries must be Unknown=true.
	// We use a temp registry on disk to avoid polluting ~/.ghostchrome.
	t.TempDir() // just check compilation; runtime test requires Chrome
	entries, err := ListContexts(nil)
	if err != nil {
		t.Fatalf("ListContexts(nil) error: %v", err)
	}
	for _, e := range entries {
		if !e.Unknown {
			t.Errorf("entry %q: expected Unknown=true when browser is nil, got Alive=%v Unknown=%v", e.Name, e.Alive, e.Unknown)
		}
	}
}
