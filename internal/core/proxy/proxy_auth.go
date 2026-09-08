package proxy

import (
	"fmt"
	"net/url"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

// ApplyProxyAuth wires CDP Fetch interception on the page so that basic-auth
// challenges issued by an upstream proxy (e.g. Bright Data, Smartproxy) are
// answered automatically with the credentials embedded in proxyURL.
//
// Chromium ignores user:password embedded in --proxy-server URLs and instead
// surfaces a blocking basic-auth dialog. The work-around is to enable
// Fetch.enable{handleAuthRequests:true} and respond to Fetch.authRequired
// via Fetch.continueWithAuth.
//
// If proxyURL is empty or carries no credentials, this is a silent no-op.
func ApplyProxyAuth(page *rod.Page, proxyURL string) error {
	if proxyURL == "" {
		return nil
	}

	u, err := url.Parse(proxyURL)
	if err != nil {
		return fmt.Errorf("parse proxy url: %w", err)
	}
	if u.User == nil {
		return nil
	}
	username := u.User.Username()
	password, hasPwd := u.User.Password()
	if username == "" && !hasPwd {
		return nil
	}

	if err := (&proto.FetchEnable{
		HandleAuthRequests: true,
		Patterns: []*proto.FetchRequestPattern{
			{URLPattern: "*"},
		},
	}).Call(page); err != nil {
		return fmt.Errorf("fetch.enable: %w", err)
	}

	// EachEvent returns a `wait` function that blocks until a callback returns
	// true. We never want to stop listening, so we run it in a detached
	// goroutine bound to the page's context — when the page closes, Rod tears
	// down the event loop and `wait()` returns.
	go page.EachEvent(
		func(e *proto.FetchAuthRequired) {
			_ = (&proto.FetchContinueWithAuth{
				RequestID: e.RequestID,
				AuthChallengeResponse: &proto.FetchAuthChallengeResponse{
					Response: proto.FetchAuthChallengeResponseResponseProvideCredentials,
					Username: username,
					Password: password,
				},
			}).Call(page)
		},
		func(e *proto.FetchRequestPaused) {
			// Passthrough: Fetch.enable forces us to ack every paused request
			// even when we don't want to modify it.
			_ = (&proto.FetchContinueRequest{
				RequestID: e.RequestID,
			}).Call(page)
		},
	)()

	return nil
}
