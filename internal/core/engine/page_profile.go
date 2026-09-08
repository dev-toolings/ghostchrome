package engine

import (
	"fmt"
	"runtime"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
	"github.com/ysmood/gson"
)

const (
	defaultAcceptLanguage = "fr-FR,fr;q=0.9,en-US;q=0.8,en;q=0.7"
	chromeMajor           = "135.0.0.0"
)

// uaProfile returns a (User-Agent, Platform) pair coherent with runtime.GOOS.
// Forcing a macOS UA on a Linux host produces JS/HTTP fingerprint mismatches
// that anti-bot stacks (DataDome, Cloudflare) flag instantly.
func uaProfile() (string, string) {
	switch runtime.GOOS {
	case "darwin":
		return "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/" + chromeMajor + " Safari/537.36", "MacIntel"
	case "windows":
		return "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/" + chromeMajor + " Safari/537.36", "Win32"
	default:
		// linux + everything else
		return "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/" + chromeMajor + " Safari/537.36", "Linux x86_64"
	}
}

// ApplyDefaultPageProfile installs the baseline browser profile used by normal
// pages. Stealth mode can still layer stronger anti-detection overrides later.
//
// Caller responsibility: do NOT call this on a page belonging to a connected
// (foreign) Chrome unless the user explicitly opted in via --apply-profile.
// Mutating an existing user tab's UA / viewport breaks the runtime policy
// stated in CLAUDE.md.
func ApplyDefaultPageProfile(page *rod.Page) error {
	if page == nil {
		return nil
	}
	ua, platform := uaProfile()
	if err := (proto.NetworkEnable{}).Call(page); err != nil {
		return fmt.Errorf("network enable: %w", err)
	}
	if err := (proto.NetworkSetUserAgentOverride{
		UserAgent:      ua,
		AcceptLanguage: defaultAcceptLanguage,
		Platform:       platform,
	}).Call(page); err != nil {
		return fmt.Errorf("user-agent override: %w", err)
	}
	// NOTE — `Upgrade-Insecure-Requests` is intentionally NOT set here. The
	// spec says that header MUST only be sent by the user agent on top-level
	// document navigations (HTML / frame fetches); Chrome already sends it
	// automatically in those cases. Pushing it through Network.setExtraHTTPHeaders
	// adds it to *every* XHR/fetch as well, which forces a CORS preflight on
	// `upgrade-insecure-requests` that most public APIs (Algolia, Elastic,
	// many GraphQL backends) do NOT include in Access-Control-Allow-Headers.
	// Result: OPTIONS 200, then POST aborted client-side with ERR_FAILED, no
	// listings ever render. This was the actual cause behind the "capcar
	// CloudFront 403 / anti-bot" misdiagnoses.
	if err := (proto.NetworkSetExtraHTTPHeaders{
		Headers: proto.NetworkHeaders{
			"Accept-Language": gson.New(defaultAcceptLanguage),
			"DNT":             gson.New("1"),
		},
	}).Call(page); err != nil {
		return fmt.Errorf("extra headers: %w", err)
	}

	sw, sh := 1440, 900
	if err := (proto.EmulationSetDeviceMetricsOverride{
		Width:             1440,
		Height:            900,
		DeviceScaleFactor: 1,
		Mobile:            false,
		ScreenWidth:       &sw,
		ScreenHeight:      &sh,
	}).Call(page); err != nil {
		return fmt.Errorf("device metrics: %w", err)
	}
	return nil
}
