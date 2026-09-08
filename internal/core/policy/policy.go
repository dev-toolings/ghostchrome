package policy

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
)

var ErrPolicyDenied = errors.New("policy denied")

type Policy struct {
	AllowedDomains  []string `json:"allowed_domains,omitempty"`
	BlockedDomains  []string `json:"blocked_domains,omitempty"`
	MaxNavigations  int      `json:"max_navigations,omitempty"`
	AllowFileUpload bool     `json:"allow_file_upload"`
	AllowClipboard  bool     `json:"allow_clipboard"`
	AllowEval       bool     `json:"allow_eval"`

	navCount atomic.Int64
}

func Load(path string) (*Policy, error) {
	clean := filepath.Clean(path)
	data, err := os.ReadFile(clean)
	if err != nil {
		return nil, fmt.Errorf("read policy file: %w", err)
	}
	var p Policy
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("parse policy: %w", err)
	}
	return &p, nil
}

func FromDomains(domains []string) *Policy {
	return &Policy{
		AllowedDomains:  domains,
		AllowFileUpload: true,
		AllowClipboard:  true,
		AllowEval:       true,
	}
}

func (p *Policy) AllowURL(rawURL string) error {
	if p == nil {
		return nil
	}

	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("%w: invalid URL %q", ErrPolicyDenied, rawURL)
	}

	switch strings.ToLower(parsed.Scheme) {
	case "http", "https":
		// fall through to host allow/block lists
	case "about":
		if parsed.Opaque == "blank" || parsed.Path == "blank" || parsed.String() == "about:blank" {
			return nil
		}
		return fmt.Errorf("%w: scheme %q is not allowed", ErrPolicyDenied, parsed.Scheme)
	case "data":
		return nil
	default:
		return fmt.Errorf("%w: scheme %q is not allowed", ErrPolicyDenied, parsed.Scheme)
	}

	host := strings.ToLower(parsed.Hostname())

	if host == "" {
		return fmt.Errorf("%w: invalid URL %q", ErrPolicyDenied, rawURL)
	}

	for _, pattern := range p.BlockedDomains {
		if matchDomain(pattern, host) {
			return fmt.Errorf("%w: domain %q is blocked", ErrPolicyDenied, host)
		}
	}

	if len(p.AllowedDomains) > 0 {
		for _, pattern := range p.AllowedDomains {
			if matchDomain(pattern, host) {
				return p.checkNavLimit()
			}
		}
		return fmt.Errorf("%w: domain %q not in allowlist", ErrPolicyDenied, host)
	}

	return p.checkNavLimit()
}

func (p *Policy) AllowAction(action string) error {
	if p == nil {
		return nil
	}
	switch action {
	case "eval":
		if !p.AllowEval {
			return fmt.Errorf("%w: eval is disabled by policy", ErrPolicyDenied)
		}
	case "upload":
		if !p.AllowFileUpload {
			return fmt.Errorf("%w: file upload is disabled by policy", ErrPolicyDenied)
		}
	case "clipboard":
		if !p.AllowClipboard {
			return fmt.Errorf("%w: clipboard access is disabled by policy", ErrPolicyDenied)
		}
	}
	return nil
}

func (p *Policy) checkNavLimit() error {
	if p.MaxNavigations <= 0 {
		return nil
	}
	n := p.navCount.Add(1)
	if int(n) > p.MaxNavigations {
		return fmt.Errorf("%w: navigation limit (%d) exceeded", ErrPolicyDenied, p.MaxNavigations)
	}
	return nil
}

func matchDomain(pattern, host string) bool {
	pattern = strings.ToLower(strings.TrimSpace(pattern))
	if pattern == host {
		return true
	}
	if strings.HasPrefix(pattern, "*.") {
		suffix := pattern[1:]
		return strings.HasSuffix(host, suffix) || host == pattern[2:]
	}
	return false
}
