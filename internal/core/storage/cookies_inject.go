package storage

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

// CookiesJSONFilename is the filename written by import-profile inside the
// ghostchrome profile dir. openPage() reads this on startup and replays the
// cookies into Chrome via CDP Network.setCookies.
const CookiesJSONFilename = ".ghostchrome-cookies.json"

// SaveCookiesJSON writes a portable cookie snapshot inside the profile dir.
func SaveCookiesJSON(profileDir string, cookies []CookieRecord) error {
	if profileDir == "" {
		return fmt.Errorf("profile dir required")
	}
	path := filepath.Join(profileDir, CookiesJSONFilename)
	data, err := json.MarshalIndent(cookies, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// LoadCookiesJSON reads the snapshot if present. Returns nil, nil when the
// file is absent (no import done on this profile).
func LoadCookiesJSON(profileDir string) ([]CookieRecord, error) {
	if profileDir == "" {
		return nil, nil
	}
	path := filepath.Join(profileDir, CookiesJSONFilename)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var cookies []CookieRecord
	if err := json.Unmarshal(data, &cookies); err != nil {
		return nil, err
	}
	return cookies, nil
}

// InjectCookies sets the given cookies on the browser via CDP, one at a
// time. A bad cookie (binary garbage from a misdecoded entry, mismatched
// domain, etc.) doesn't poison the rest of the batch. Returns the number
// of successful injections.
func InjectCookies(page *rod.Page, cookies []CookieRecord) (int, error) {
	if len(cookies) == 0 {
		return 0, nil
	}
	ok := 0
	for _, c := range cookies {
		if !isInjectableValue(c.Value) {
			continue
		}
		// Build a synthetic URL so Chrome is happy with the (domain, path)
		// pair regardless of the current page's origin (we set cookies on
		// about:blank, before any navigation).
		host := c.Domain
		if len(host) > 0 && host[0] == '.' {
			host = host[1:]
		}
		path := c.Path
		if path == "" {
			path = "/"
		}
		scheme := "https"
		if !c.Secure {
			scheme = "http"
		}
		cookieURL := scheme + "://" + host + path
		p := proto.NetworkSetCookie{
			Name:     c.Name,
			Value:    c.Value,
			URL:      cookieURL,
			Domain:   c.Domain,
			Path:     c.Path,
			Secure:   c.Secure,
			HTTPOnly: c.HTTPOnly,
		}
		if c.Expires > 0 {
			p.Expires = proto.TimeSinceEpoch(c.Expires)
		}
		switch c.SameSite {
		case "Strict":
			p.SameSite = proto.NetworkCookieSameSiteStrict
		case "Lax":
			p.SameSite = proto.NetworkCookieSameSiteLax
		case "None":
			p.SameSite = proto.NetworkCookieSameSiteNone
		}
		if _, err := p.Call(page); err == nil {
			ok++
		}
	}
	return ok, nil
}

// isInjectableValue rejects cookie values containing control bytes that
// CDP setCookie refuses (every byte < 0x20 except tab is invalid in HTTP
// cookie syntax). Decrypted-but-still-binary values (SHA-256 integrity
// prefixes that we couldn't strip cleanly) get filtered here.
func isInjectableValue(v string) bool {
	if v == "" {
		return false
	}
	for i := 0; i < len(v); i++ {
		c := v[i]
		if c < 0x20 && c != '\t' {
			return false
		}
		if c == 0x7f {
			return false
		}
	}
	return true
}
