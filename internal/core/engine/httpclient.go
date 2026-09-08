package engine

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"golang.org/x/net/proxy"
)

// HTTPClientOpts configures the proxy-aware http.Client used by the
// no-browser commands (fastfetch, fetchapi). Empty Proxy means direct.
type HTTPClientOpts struct {
	Proxy   string
	Timeout time.Duration
}

// BuildHTTPClient returns an http.Client honoring the supplied proxy URL.
// Supports http://, https:// (HTTP CONNECT) and socks5:// schemes.
// http(s) proxies may carry user:pass; socks5 auth is taken from URL too.
func BuildHTTPClient(opts HTTPClientOpts) (*http.Client, error) {
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 8 * time.Second
	}
	transport := &http.Transport{}

	if opts.Proxy != "" {
		u, err := url.Parse(opts.Proxy)
		if err != nil {
			return nil, fmt.Errorf("httpclient: parse proxy %q: %w", opts.Proxy, err)
		}
		switch u.Scheme {
		case "http", "https", "":
			if u.Scheme == "" {
				u.Scheme = "http"
			}
			transport.Proxy = http.ProxyURL(u)
		case "socks5", "socks5h":
			var auth *proxy.Auth
			if u.User != nil {
				pw, _ := u.User.Password()
				auth = &proxy.Auth{User: u.User.Username(), Password: pw}
			}
			dialer, err := proxy.SOCKS5("tcp", u.Host, auth, proxy.Direct)
			if err != nil {
				return nil, fmt.Errorf("httpclient: socks5 dialer: %w", err)
			}
			transport.Dial = dialer.Dial
		default:
			return nil, fmt.Errorf("httpclient: unsupported proxy scheme %q", u.Scheme)
		}
	}

	return &http.Client{
		Timeout:   timeout,
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("too many redirects")
			}
			return nil
		},
	}, nil
}
