package pagesetup

import (
	"fmt"
	"strings"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

var playwrightPermissionMap = map[string][]proto.BrowserPermissionType{
	"geolocation":       {proto.BrowserPermissionTypeGeolocation},
	"notifications":     {proto.BrowserPermissionTypeNotifications},
	"camera":            {proto.BrowserPermissionTypeVideoCapture},
	"microphone":        {proto.BrowserPermissionTypeAudioCapture},
	"clipboard-read":    {proto.BrowserPermissionTypeClipboardReadWrite},
	"clipboard-write":   {proto.BrowserPermissionTypeClipboardSanitizedWrite},
	"midi":              {proto.BrowserPermissionTypeMidi},
	"midi-sysex":        {proto.BrowserPermissionTypeMidiSysex},
	"payment-handler":   {proto.BrowserPermissionTypePaymentHandler},
	"background-sync":   {proto.BrowserPermissionTypeBackgroundSync},
	"storage-access":    {proto.BrowserPermissionTypeStorageAccess},
	"local-fonts":       {proto.BrowserPermissionTypeLocalFonts},
	"window-management": {proto.BrowserPermissionTypeWindowManagement},
	"screen-wake-lock":  {proto.BrowserPermissionTypeWakeLockScreen},
}

// MapPlaywrightPermissions converts Playwright permission names to CDP
// Browser.grantPermissions values. Unknown permissions are returned so callers
// can report them instead of silently overclaiming parity.
func MapPlaywrightPermissions(names []string) (mapped []proto.BrowserPermissionType, unsupported []string) {
	seen := map[proto.BrowserPermissionType]bool{}
	for _, raw := range names {
		name := strings.ToLower(strings.TrimSpace(raw))
		if name == "" {
			continue
		}
		values, ok := playwrightPermissionMap[name]
		if !ok {
			unsupported = append(unsupported, raw)
			continue
		}
		for _, value := range values {
			if seen[value] {
				continue
			}
			seen[value] = true
			mapped = append(mapped, value)
		}
	}
	return mapped, unsupported
}

// GrantPlaywrightPermissions grants context-wide permissions using CDP. The
// origin is intentionally omitted to match Playwright config semantics.
func GrantPlaywrightPermissions(browser *rod.Browser, names []string) error {
	if browser == nil || len(names) == 0 {
		return nil
	}
	permissions, unsupported := MapPlaywrightPermissions(names)
	if len(unsupported) > 0 {
		return fmt.Errorf("unsupported permissions: %s", strings.Join(unsupported, ", "))
	}
	if len(permissions) == 0 {
		return nil
	}
	if err := (proto.BrowserGrantPermissions{
		Permissions:      permissions,
		BrowserContextID: browser.BrowserContextID,
	}).Call(browser); err != nil {
		return fmt.Errorf("grant permissions: %w", err)
	}
	return nil
}
