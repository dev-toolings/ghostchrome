package interact

import (
	"testing"

	"github.com/go-rod/rod/lib/proto"
)

func TestParseMouseButton(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want proto.InputMouseButton
		ok   bool
	}{
		{"", proto.InputMouseButtonLeft, true},
		{"left", proto.InputMouseButtonLeft, true},
		{"LEFT", proto.InputMouseButtonLeft, true},
		{" right ", proto.InputMouseButtonRight, true},
		{"middle", proto.InputMouseButtonMiddle, true},
		{"forward", "", false},
	}
	for _, tc := range cases {
		got, err := ParseMouseButton(tc.in)
		if tc.ok {
			if err != nil {
				t.Fatalf("ParseMouseButton(%q) unexpected error: %v", tc.in, err)
			}
			if got != tc.want {
				t.Fatalf("ParseMouseButton(%q)=%q want %q", tc.in, got, tc.want)
			}
			continue
		}
		if err == nil {
			t.Fatalf("ParseMouseButton(%q) expected error", tc.in)
		}
	}
}
