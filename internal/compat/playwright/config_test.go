package playwright

import "testing"

func TestProxyURLWithAuth(t *testing.T) {
	got := ProxyURLWithAuth("http://proxy.test:8080", "user", "pass")
	if got != "http://user:pass@proxy.test:8080" {
		t.Fatalf("ProxyURLWithAuth = %q", got)
	}
	if got := ProxyURLWithAuth("proxy.test:8080", "user", "pass"); got != "proxy.test:8080" {
		t.Fatalf("ProxyURLWithAuth invalid url = %q", got)
	}
}
