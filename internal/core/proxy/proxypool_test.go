package proxy

import "testing"

func TestParseProxyList(t *testing.T) {
	cases := map[string][]string{
		"http://a:8000,http://b:8000":          {"http://a:8000", "http://b:8000"},
		" http://a:8000 \n http://b:8000 \n\n": {"http://a:8000", "http://b:8000"},
		"http://a:8000":                        {"http://a:8000"},
		"":                                     {},
		"  ,  ,  ":                             {},
	}
	for in, want := range cases {
		got := ParseProxyList(in)
		if len(got) != len(want) {
			t.Errorf("ParseProxyList(%q) = %v, want %v", in, got, want)
			continue
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("ParseProxyList(%q)[%d] = %q, want %q", in, i, got[i], want[i])
			}
		}
	}
}

func TestProxyPoolRoundRobin(t *testing.T) {
	p := NewProxyPool([]string{"p1", "p2", "p3"})
	if p.Len() != 3 {
		t.Fatalf("Len = %d, want 3", p.Len())
	}
	want := []string{"p1", "p2", "p3", "p1", "p2"}
	for i, w := range want {
		if got := p.Next(); got != w {
			t.Errorf("Next() call %d = %q, want %q", i, got, w)
		}
	}
}

func TestProxyPoolEmpty(t *testing.T) {
	p := NewProxyPool(nil)
	if p.Len() != 0 {
		t.Fatalf("Len = %d, want 0", p.Len())
	}
	if got := p.Next(); got != "" {
		t.Errorf("empty pool Next() = %q, want \"\"", got)
	}
}
