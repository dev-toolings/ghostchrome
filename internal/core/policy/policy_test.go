package policy

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestAllowURL_NoPolicy(t *testing.T) {
	var p *Policy
	if err := p.AllowURL("https://example.com"); err != nil {
		t.Fatalf("nil policy should allow everything: %v", err)
	}
}

func TestAllowURL_AllowedDomains(t *testing.T) {
	p := &Policy{AllowedDomains: []string{"*.example.com", "api.stripe.com"}}

	cases := []struct {
		url  string
		want bool
	}{
		{"https://www.example.com/page", true},
		{"https://example.com/page", true},
		{"https://sub.example.com", true},
		{"https://api.stripe.com/v1/charges", true},
		{"https://evil.com", false},
		{"https://notexample.com", false},
		{"data:text/html,<h1>ok</h1>", true},
		{"about:blank", true},
		{"javascript:alert(1)", false},
		{"file:///etc/passwd", false},
		{"ftp://example.com/file", false},
	}

	for _, c := range cases {
		err := p.AllowURL(c.url)
		got := err == nil
		if got != c.want {
			t.Errorf("AllowURL(%q) = %v, want allowed=%v", c.url, err, c.want)
		}
	}
}

func TestAllowURL_BlockedDomains(t *testing.T) {
	p := &Policy{BlockedDomains: []string{"*.evil.com", "bad.org"}}

	if err := p.AllowURL("https://evil.com"); err == nil {
		t.Error("expected evil.com to be blocked")
	}
	if err := p.AllowURL("https://sub.evil.com"); err == nil {
		t.Error("expected sub.evil.com to be blocked")
	}
	if err := p.AllowURL("https://bad.org/page"); err == nil {
		t.Error("expected bad.org to be blocked")
	}
	if err := p.AllowURL("https://good.com"); err != nil {
		t.Errorf("good.com should be allowed: %v", err)
	}
}

func TestAllowURL_BlockTakesPrecedence(t *testing.T) {
	p := &Policy{
		AllowedDomains: []string{"*.example.com"},
		BlockedDomains: []string{"blocked.example.com"},
	}

	if err := p.AllowURL("https://blocked.example.com"); err == nil {
		t.Error("blocked.example.com should be denied even though *.example.com is allowed")
	}
	if err := p.AllowURL("https://ok.example.com"); err != nil {
		t.Errorf("ok.example.com should be allowed: %v", err)
	}
}

func TestAllowURL_MaxNavigations(t *testing.T) {
	p := &Policy{MaxNavigations: 2}

	if err := p.AllowURL("https://a.com"); err != nil {
		t.Fatalf("nav 1 should pass: %v", err)
	}
	if err := p.AllowURL("https://b.com"); err != nil {
		t.Fatalf("nav 2 should pass: %v", err)
	}
	if err := p.AllowURL("https://c.com"); err == nil {
		t.Fatal("nav 3 should be denied (limit=2)")
	}
}

func TestAllowAction(t *testing.T) {
	p := &Policy{AllowEval: false, AllowFileUpload: false, AllowClipboard: false}

	if err := p.AllowAction("eval"); !errors.Is(err, ErrPolicyDenied) {
		t.Errorf("eval should be denied: %v", err)
	}
	if err := p.AllowAction("upload"); !errors.Is(err, ErrPolicyDenied) {
		t.Errorf("upload should be denied: %v", err)
	}
	if err := p.AllowAction("clipboard"); !errors.Is(err, ErrPolicyDenied) {
		t.Errorf("clipboard should be denied: %v", err)
	}
	if err := p.AllowAction("click"); err != nil {
		t.Errorf("click should always be allowed: %v", err)
	}
}

func TestAllowAction_NilPolicy(t *testing.T) {
	var p *Policy
	if err := p.AllowAction("eval"); err != nil {
		t.Fatalf("nil policy should allow everything: %v", err)
	}
}

func TestAllowURL_DeniedSchemes(t *testing.T) {
	p := &Policy{}
	for _, raw := range []string{
		"javascript:alert(1)",
		"file:///tmp/x",
		"blob:https://example.com/uuid",
		"about:srcdoc",
		"chrome://settings",
	} {
		if err := p.AllowURL(raw); !errors.Is(err, ErrPolicyDenied) {
			t.Errorf("AllowURL(%q) = %v, want policy denied", raw, err)
		}
	}
}

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "policy.json")
	data := `{
		"allowed_domains": ["*.example.com"],
		"blocked_domains": ["bad.example.com"],
		"max_navigations": 100,
		"allow_file_upload": false,
		"allow_clipboard": true,
		"allow_eval": true
	}`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}

	p, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if len(p.AllowedDomains) != 1 || p.AllowedDomains[0] != "*.example.com" {
		t.Errorf("AllowedDomains = %v", p.AllowedDomains)
	}
	if p.MaxNavigations != 100 {
		t.Errorf("MaxNavigations = %d", p.MaxNavigations)
	}
	if p.AllowFileUpload {
		t.Error("AllowFileUpload should be false")
	}
}

func TestFromDomains(t *testing.T) {
	p := FromDomains([]string{"a.com", "*.b.com"})
	if err := p.AllowURL("https://a.com"); err != nil {
		t.Errorf("a.com should be allowed: %v", err)
	}
	if err := p.AllowURL("https://c.com"); err == nil {
		t.Error("c.com should be denied")
	}
}

func TestMatchDomain(t *testing.T) {
	cases := []struct {
		pattern, host string
		want          bool
	}{
		{"example.com", "example.com", true},
		{"example.com", "sub.example.com", false},
		{"*.example.com", "sub.example.com", true},
		{"*.example.com", "example.com", true},
		{"*.example.com", "deep.sub.example.com", true},
		{"*.example.com", "notexample.com", false},
		{"Example.COM", "example.com", true},
	}
	for _, c := range cases {
		if got := matchDomain(c.pattern, c.host); got != c.want {
			t.Errorf("matchDomain(%q, %q) = %v, want %v", c.pattern, c.host, got, c.want)
		}
	}
}
