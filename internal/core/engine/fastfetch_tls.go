package engine

import (
	"fmt"
	fhttp "github.com/bogdanfinn/fhttp"
	tlsclient "github.com/bogdanfinn/tls-client"
	"github.com/bogdanfinn/tls-client/profiles"
	"io"
	"net/http"
	"strings"
	"time"
)

// doChromeTLS uses an in-process Go transport: no Python, curl executable,
// cookie import, or second browser. The normal certificate checks stay enabled.
func doChromeTLS(req *http.Request, opts FastFetchOpts) (*http.Response, error) {
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 8 * time.Second
	}
	options := []tlsclient.HttpClientOption{
		tlsclient.WithClientProfile(profiles.Chrome_146),
		tlsclient.WithTimeoutMilliseconds(int(timeout.Milliseconds())),
		tlsclient.WithCustomRedirectFunc(func(next *fhttp.Request, via []*fhttp.Request) error {
			if len(via) >= 5 {
				return fmt.Errorf("too many redirects")
			}
			if next.URL.Scheme != req.URL.Scheme || next.URL.Host != req.URL.Host {
				return fmt.Errorf("TLS fallback refuses cross-origin redirects")
			}
			return nil
		}),
	}
	if opts.Proxy != "" {
		options = append(options, tlsclient.WithProxyUrl(opts.Proxy))
	}
	client, err := tlsclient.NewHttpClient(tlsclient.NewNoopLogger(), options...)
	if err != nil {
		return nil, fmt.Errorf("TLS client configuration failed")
	}
	target, err := fhttp.NewRequestWithContext(req.Context(), http.MethodGet, req.URL.String(), nil)
	if err != nil {
		client.CloseIdleConnections()
		return nil, err
	}

	for key, values := range req.Header {
		target.Header[strings.ToLower(key)] = append([]string(nil), values...)
	}
	if opts.UserAgent == "" && req.Header.Get("User-Agent") == fastFetchUA() {
		target.Header["user-agent"] = []string{"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/146.0.0.0 Safari/537.36"}
		target.Header["sec-ch-ua"] = []string{`"Chromium";v="146", "Not-A.Brand";v="24", "Google Chrome";v="146"`}
		target.Header["sec-ch-ua-mobile"] = []string{"?0"}
		target.Header["sec-ch-ua-platform"] = []string{`"macOS"`}
	}
	target.Header[fhttp.HeaderOrderKey] = []string{"sec-ch-ua", "sec-ch-ua-mobile", "sec-ch-ua-platform", "upgrade-insecure-requests", "user-agent", "accept", "sec-fetch-site", "sec-fetch-mode", "sec-fetch-user", "sec-fetch-dest", "accept-encoding", "accept-language", "priority"}
	if target.Header["priority"] == nil {
		target.Header["priority"] = []string{"u=0, i"}
	}

	response, err := client.Do(target)
	if err != nil {
		client.CloseIdleConnections()
		return nil, fmt.Errorf("TLS request failed")
	}
	if response.Uncompressed {
		for key := range response.Header {
			if strings.EqualFold(key, "Content-Encoding") || strings.EqualFold(key, "Content-Length") {
				delete(response.Header, key)
			}
		}
	}
	final := req.Clone(req.Context())
	final.URL = response.Request.URL
	return &http.Response{StatusCode: response.StatusCode, Header: http.Header(response.Header), Request: final,
		Body: &tlsResponseBody{ReadCloser: response.Body, closeIdle: client.CloseIdleConnections}}, nil
}

type tlsResponseBody struct {
	io.ReadCloser
	closeIdle func()
}

func (b *tlsResponseBody) Close() error { err := b.ReadCloser.Close(); b.closeIdle(); return err }
