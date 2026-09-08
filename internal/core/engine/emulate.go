package engine

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
	"github.com/ysmood/gson"
)

// maxTouchPoints is what a touch device reports through
// navigator.maxTouchPoints while touch emulation is on.
const maxTouchPoints = 5

// Device describes a hardware profile used by the emulate command.
// Dimensions are CSS pixels; DPR is devicePixelRatio.
type Device struct {
	Name      string
	Width     int
	Height    int
	DPR       float64
	UserAgent string
	Mobile    bool
	Touch     bool
}

var devices = []Device{
	{
		Name: "iphone-se", Width: 375, Height: 667, DPR: 2,
		UserAgent: "Mozilla/5.0 (iPhone; CPU iPhone OS 17_3 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.3 Mobile/15E148 Safari/604.1",
		Mobile:    true, Touch: true,
	},
	{
		Name: "iphone-14", Width: 390, Height: 844, DPR: 3,
		UserAgent: "Mozilla/5.0 (iPhone; CPU iPhone OS 17_3 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.3 Mobile/15E148 Safari/604.1",
		Mobile:    true, Touch: true,
	},
	{
		Name: "iphone-14-pro", Width: 393, Height: 852, DPR: 3,
		UserAgent: "Mozilla/5.0 (iPhone; CPU iPhone OS 17_3 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.3 Mobile/15E148 Safari/604.1",
		Mobile:    true, Touch: true,
	},
	{
		Name: "iphone-14-pro-max", Width: 430, Height: 932, DPR: 3,
		UserAgent: "Mozilla/5.0 (iPhone; CPU iPhone OS 17_3 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.3 Mobile/15E148 Safari/604.1",
		Mobile:    true, Touch: true,
	},
	{
		Name: "pixel-7", Width: 412, Height: 915, DPR: 2.625,
		UserAgent: "Mozilla/5.0 (Linux; Android 14; Pixel 7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Mobile Safari/537.36",
		Mobile:    true, Touch: true,
	},
	{
		Name: "pixel-8-pro", Width: 448, Height: 998, DPR: 3,
		UserAgent: "Mozilla/5.0 (Linux; Android 14; Pixel 8 Pro) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Mobile Safari/537.36",
		Mobile:    true, Touch: true,
	},
	{
		Name: "ipad", Width: 768, Height: 1024, DPR: 2,
		UserAgent: "Mozilla/5.0 (iPad; CPU OS 17_3 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.3 Mobile/15E148 Safari/604.1",
		Mobile:    true, Touch: true,
	},
	{
		Name: "ipad-pro", Width: 1024, Height: 1366, DPR: 2,
		UserAgent: "Mozilla/5.0 (iPad; CPU OS 17_3 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.3 Mobile/15E148 Safari/604.1",
		Mobile:    true, Touch: true,
	},
	{
		Name: "desktop", Width: 1920, Height: 1080, DPR: 1,
		UserAgent: "",
		Mobile:    false, Touch: false,
	},
	{
		Name: "desktop-2k", Width: 2560, Height: 1440, DPR: 1,
		UserAgent: "",
		Mobile:    false, Touch: false,
	},
}

// DeviceByName looks up a Device preset by its canonical name.
func DeviceByName(name string) (Device, bool) {
	for _, d := range devices {
		if d.Name == name {
			return d, true
		}
	}
	return Device{}, false
}

// ListDevices returns a copy of the registered device presets.
func ListDevices() []Device {
	out := make([]Device, len(devices))
	copy(out, devices)
	return out
}

// ApplyDevice applies viewport metrics, UA, and touch emulation from a preset.
func ApplyDevice(page *rod.Page, d Device) error {
	sw, sh := d.Width, d.Height
	err := (proto.EmulationSetDeviceMetricsOverride{
		Width:             d.Width,
		Height:            d.Height,
		DeviceScaleFactor: d.DPR,
		Mobile:            d.Mobile,
		ScreenWidth:       &sw,
		ScreenHeight:      &sh,
	}).Call(page)
	if err != nil {
		return fmt.Errorf("device metrics: %w", err)
	}

	if err := setTouchEmulation(page, d.Touch); err != nil {
		return err
	}

	if d.UserAgent != "" {
		if err := ApplyUserAgent(page, d.UserAgent); err != nil {
			return err
		}
	}
	return nil
}

// EmulationFromDevice converts a preset into the persistable profile that
// ApplyDevice just installed on the page.
func EmulationFromDevice(d Device) EmulationState {
	return EmulationState{
		Device:    d.Name,
		Width:     d.Width,
		Height:    d.Height,
		DPR:       d.DPR,
		Mobile:    d.Mobile,
		Touch:     d.Touch,
		UserAgent: d.UserAgent,
	}
}

// ApplyEmulationState replays a persisted emulation profile on a page. It is
// the exact counterpart of what ApplyDevice / SetViewport / ApplyUserAgent
// installed in the session that saved the profile.
func ApplyEmulationState(page *rod.Page, s EmulationState) error {
	if page == nil || s.Empty() {
		return nil
	}
	if s.Width > 0 && s.Height > 0 {
		dpr := s.DPR
		if dpr <= 0 {
			dpr = 1
		}
		sw, sh := s.Width, s.Height
		if err := (proto.EmulationSetDeviceMetricsOverride{
			Width:             s.Width,
			Height:            s.Height,
			DeviceScaleFactor: dpr,
			Mobile:            s.Mobile,
			ScreenWidth:       &sw,
			ScreenHeight:      &sh,
		}).Call(page); err != nil {
			return fmt.Errorf("device metrics: %w", err)
		}
	}
	if s.Touch {
		if err := setTouchEmulation(page, true); err != nil {
			return err
		}
	}
	if s.UserAgent != "" {
		if err := ApplyUserAgent(page, s.UserAgent); err != nil {
			return err
		}
	}
	if s.ColorScheme != "" {
		if err := ApplyColorScheme(page, s.ColorScheme); err != nil {
			return err
		}
	}
	if s.Timezone != "" {
		if err := ApplyTimezone(page, s.Timezone); err != nil {
			return err
		}
	}
	return nil
}

// ApplyEmulationProfile installs an emulation profile authoritatively.
//
// It differs from ApplyEmulationState, which is a replay helper that only ever
// adds overrides: this one also turns touch emulation OFF when the profile has
// Touch=false, so switching from a phone profile back to a desktop one really
// restores pointer:fine instead of leaving navigator.maxTouchPoints at 5.
// Use ResetEmulation to drop the overrides entirely.
func ApplyEmulationProfile(page *rod.Page, s EmulationState) error {
	if page == nil {
		return nil
	}
	if s.Width > 0 && s.Height > 0 {
		dpr := s.DPR
		if dpr <= 0 {
			dpr = 1
		}
		sw, sh := s.Width, s.Height
		if err := (proto.EmulationSetDeviceMetricsOverride{
			Width:             s.Width,
			Height:            s.Height,
			DeviceScaleFactor: dpr,
			Mobile:            s.Mobile,
			ScreenWidth:       &sw,
			ScreenHeight:      &sh,
		}).Call(page); err != nil {
			return fmt.Errorf("device metrics: %w", err)
		}
	}
	if err := setTouchEmulation(page, s.Touch); err != nil {
		return err
	}
	if s.UserAgent != "" {
		if err := ApplyUserAgent(page, s.UserAgent); err != nil {
			return err
		}
	}
	if s.ColorScheme != "" {
		if err := ApplyColorScheme(page, s.ColorScheme); err != nil {
			return err
		}
	}
	if s.Timezone != "" {
		if err := ApplyTimezone(page, s.Timezone); err != nil {
			return err
		}
	}
	return nil
}

// ResetEmulation drops every emulation override on the page and restores the
// browser's own User-Agent. Device metrics fall back to the real window size,
// which is what an un-emulated tab looks like.
func ResetEmulation(page *rod.Page) error {
	if page == nil {
		return nil
	}
	if err := (proto.EmulationClearDeviceMetricsOverride{}).Call(page); err != nil {
		return fmt.Errorf("clear device metrics: %w", err)
	}
	if err := setTouchEmulation(page, false); err != nil {
		return err
	}
	// An empty Features list clears every previously emulated media feature.
	if err := (proto.EmulationSetEmulatedMedia{}).Call(page); err != nil {
		return fmt.Errorf("clear emulated media: %w", err)
	}
	// Empty TimezoneID means "restore the host timezone" per the CDP contract.
	if err := (proto.EmulationSetTimezoneOverride{TimezoneID: ""}).Call(page); err != nil {
		return fmt.Errorf("clear timezone override: %w", err)
	}
	version, err := proto.BrowserGetVersion{}.Call(page)
	if err != nil {
		return fmt.Errorf("browser version: %w", err)
	}
	if version.UserAgent != "" {
		if err := ApplyUserAgent(page, version.UserAgent); err != nil {
			return err
		}
	}
	return nil
}

// setTouchEmulation toggles touch input emulation. MaxTouchPoints is only sent
// when enabling: Chrome rejects the call with "Touch points must be between 1
// and 16" if the field is present while disabling, which used to make every
// non-touch device preset (desktop, desktop-2k) fail outright.
func setTouchEmulation(page *rod.Page, enabled bool) error {
	req := proto.EmulationSetTouchEmulationEnabled{Enabled: enabled}
	if enabled {
		maxTouch := maxTouchPoints
		req.MaxTouchPoints = &maxTouch
	}
	if err := req.Call(page); err != nil {
		return fmt.Errorf("touch emulation: %w", err)
	}
	return nil
}

// ApplyUserAgent overrides navigator.userAgent and the HTTP User-Agent header.
func ApplyUserAgent(page *rod.Page, ua string) error {
	return ApplyUserAgentLocale(page, ua, "")
}

// ApplyUserAgentLocale overrides the User-Agent and optionally the browser
// Accept-Language value using the same CDP call Playwright-backed contexts use.
func ApplyUserAgentLocale(page *rod.Page, ua string, locale string) error {
	req := proto.NetworkSetUserAgentOverride{UserAgent: ua}
	if locale != "" {
		acceptLanguage, _, err := localeValues(locale)
		if err != nil {
			return err
		}
		req.AcceptLanguage = acceptLanguage
	}
	if err := req.Call(page); err != nil {
		return fmt.Errorf("user-agent override: %w", err)
	}
	if locale != "" {
		if err := installLocaleScript(page, locale); err != nil {
			return err
		}
	}
	return nil
}

// ApplyLocale emulates Playwright's browser.contextOptions.locale for a page.
// Without a User-Agent override, CDP has no standalone AcceptLanguage command,
// so this applies the HTTP header plus navigator.language(s) for new documents.
func ApplyLocale(page *rod.Page, locale string) error {
	acceptLanguage, _, err := localeValues(locale)
	if err != nil {
		return err
	}
	if err := (proto.NetworkEnable{}).Call(page); err != nil {
		return fmt.Errorf("network enable: %w", err)
	}
	if err := (proto.NetworkSetExtraHTTPHeaders{
		Headers: proto.NetworkHeaders{
			"Accept-Language": gson.New(acceptLanguage),
			"DNT":             gson.New("1"),
		},
	}).Call(page); err != nil {
		return fmt.Errorf("locale headers: %w", err)
	}
	if err := installLocaleScript(page, locale); err != nil {
		return err
	}
	return nil
}

func installLocaleScript(page *rod.Page, locale string) error {
	_, languages, err := localeValues(locale)
	if err != nil {
		return err
	}
	primaryJSON, err := json.Marshal(languages[0])
	if err != nil {
		return fmt.Errorf("locale script: %w", err)
	}
	languagesJSON, err := json.Marshal(languages)
	if err != nil {
		return fmt.Errorf("locale script: %w", err)
	}
	script := fmt.Sprintf(`(() => {
	const primary = %s;
	const languages = %s;
	const define = (name, getter) => {
		try {
			Object.defineProperty(Navigator.prototype, name, { get: getter, configurable: true });
		} catch (_) {}
	};
	define('language', () => primary);
	define('languages', () => languages.slice());
})();`, primaryJSON, languagesJSON)
	if _, err := page.EvalOnNewDocument(script); err != nil {
		return fmt.Errorf("locale init script: %w", err)
	}
	return nil
}

func localeValues(locale string) (acceptLanguage string, languages []string, err error) {
	primary := strings.TrimSpace(strings.ReplaceAll(locale, "_", "-"))
	if primary == "" {
		return "", nil, fmt.Errorf("locale: empty")
	}
	base := primary
	if idx := strings.IndexByte(primary, '-'); idx > 0 {
		base = primary[:idx]
	}
	languages = dedupeStrings([]string{primary, base, "en-US", "en"})
	if len(languages) == 1 {
		return primary, languages, nil
	}
	return fmt.Sprintf("%s,%s;q=0.9,en-US;q=0.8,en;q=0.7", primary, base), languages, nil
}

// ApplyColorScheme emulates prefers-color-scheme. Accepts "dark", "light",
// "no-preference" (case-insensitive).
func ApplyColorScheme(page *rod.Page, scheme string) error {
	normalized := strings.ToLower(strings.TrimSpace(scheme))
	switch normalized {
	case "dark", "light", "no-preference":
	default:
		return fmt.Errorf("color-scheme: expected dark|light|no-preference, got %q", scheme)
	}
	feature := proto.EmulationMediaFeature{
		Name:  "prefers-color-scheme",
		Value: normalized,
	}
	return (proto.EmulationSetEmulatedMedia{Features: []*proto.EmulationMediaFeature{&feature}}).Call(page)
}

// ApplyTimezone overrides the JavaScript Date/Intl timezone.
func ApplyTimezone(page *rod.Page, tz string) error {
	if tz == "" {
		return fmt.Errorf("timezone: empty")
	}
	if err := (proto.EmulationSetTimezoneOverride{TimezoneID: tz}).Call(page); err != nil {
		return fmt.Errorf("timezone override: %w", err)
	}
	return nil
}
