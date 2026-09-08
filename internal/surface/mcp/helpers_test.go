package mcp

import "testing"

func TestNewServerDefaults(t *testing.T) {
	s := New(Options{})
	if s.opts.TimeoutSec != 30 {
		t.Errorf("default TimeoutSec=%d, want 30", s.opts.TimeoutSec)
	}
	srv := s.Build("test", "0.0.0")
	if srv == nil {
		t.Fatal("Build returned nil")
	}
}

func TestNormalizeRef(t *testing.T) {
	cases := []struct{ in, want string }{
		{"@3", "@3"},
		{"3", "@3"},
		{"ref:7", "@7"},
		{"  @12 ", "@12"},
		{"", ""},
	}
	for _, c := range cases {
		if got := normalizeRef(c.in); got != c.want {
			t.Errorf("normalizeRef(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
