package engine

import "testing"

// RemoveProfile must reject any name that could escape the profiles directory
// or is otherwise invalid, before touching the filesystem.
func TestRemoveProfileRejectsUnsafeNames(t *testing.T) {
	for _, bad := range []string{"", "..", ".", "../etc", "a/b", "foo/../bar", "/abs", "x\x00y", "a b"} {
		if err := RemoveProfile(bad); err == nil {
			t.Errorf("RemoveProfile(%q) = nil, want error", bad)
		}
	}
}
