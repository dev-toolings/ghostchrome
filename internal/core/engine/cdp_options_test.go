package engine

import "testing"

func TestCDPHeader(t *testing.T) {
	got := cdpHeader(map[string]string{
		"Authorization": "Bearer token",
		"X-Test":        "yes",
	})
	if got.Get("Authorization") != "Bearer token" {
		t.Fatalf("Authorization = %q", got.Get("Authorization"))
	}
	if got.Get("X-Test") != "yes" {
		t.Fatalf("X-Test = %q", got.Get("X-Test"))
	}
}

func TestCDPHeaderEmpty(t *testing.T) {
	if got := cdpHeader(nil); got != nil {
		t.Fatalf("cdpHeader(nil) = %#v", got)
	}
}

func TestSplitChromiumArg(t *testing.T) {
	name, values, ok := splitChromiumArg("--disable-web-security")
	if !ok || name != "disable-web-security" || len(values) != 0 {
		t.Fatalf("split boolean arg = %q %#v %t", name, values, ok)
	}
	name, values, ok = splitChromiumArg("--window-size=800,600")
	if !ok || name != "window-size" || len(values) != 1 || values[0] != "800,600" {
		t.Fatalf("split value arg = %q %#v %t", name, values, ok)
	}
	if _, _, ok := splitChromiumArg("not-a-switch"); ok {
		t.Fatal("expected non-switch arg to be rejected")
	}
}
