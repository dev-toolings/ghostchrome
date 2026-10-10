package engine

import (
	"strings"
	"testing"
)

func TestElideDataURL(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "payload", in: "data:image/svg+xml;charset=utf8," + strings.Repeat("a", 100), want: "data:image/svg+xml;charset=utf8,[100 chars]"},
		{name: "no comma", in: "data:abcdef", want: "data:[6 chars]"},
		{name: "http unchanged", in: "https://example.com/a.png?x=1", want: "https://example.com/a.png?x=1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ElideDataURL(tt.in); got != tt.want {
				t.Errorf("ElideDataURL() = %q, want %q", got, tt.want)
			}
		})
	}
}
