package storage

import (
	"encoding/json"
	"fmt"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

// StorageState mirrors the Playwright storageState JSON shape so a state file
// produced by ghostchrome can be loaded by Playwright (and vice versa, within
// reasonable limits).
type StorageState struct {
	Cookies []StorageCookie `json:"cookies"`
	Origins []StorageOrigin `json:"origins"`
}

// StorageCookie uses the Playwright-compatible field names (sameSite as string,
// expires as float64 seconds-since-epoch).
type StorageCookie struct {
	Name     string  `json:"name"`
	Value    string  `json:"value"`
	Domain   string  `json:"domain"`
	Path     string  `json:"path"`
	Expires  float64 `json:"expires,omitempty"`
	HTTPOnly bool    `json:"httpOnly,omitempty"`
	Secure   bool    `json:"secure,omitempty"`
	SameSite string  `json:"sameSite,omitempty"`
}

// StorageOrigin groups localStorage entries by origin. sessionStorage is NOT
// persisted: it's per-tab and generally not replayable.
type StorageOrigin struct {
	Origin       string            `json:"origin"`
	LocalStorage []StorageKeyValue `json:"localStorage"`
}

// StorageKeyValue is a single localStorage entry.
type StorageKeyValue struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// SaveStorageState captures cookies (browser-wide) and localStorage for the
// origin of the current page. Callers can concatenate multiple SaveStorageState
// runs if they need multiple origins.
func SaveStorageState(browser *rod.Browser, page *rod.Page) (*StorageState, error) {
	state := &StorageState{}

	cookies, err := browser.GetCookies()
	if err != nil {
		return nil, fmt.Errorf("get cookies: %w", err)
	}
	for _, c := range cookies {
		state.Cookies = append(state.Cookies, StorageCookie{
			Name:     c.Name,
			Value:    c.Value,
			Domain:   c.Domain,
			Path:     c.Path,
			Expires:  float64(c.Expires),
			HTTPOnly: c.HTTPOnly,
			Secure:   c.Secure,
			SameSite: string(c.SameSite),
		})
	}

	if page != nil {
		origin, entries, err := snapshotLocalStorage(page)
		if err != nil {
			return nil, fmt.Errorf("snapshot localStorage: %w", err)
		}
		if origin != "" && len(entries) > 0 {
			state.Origins = append(state.Origins, StorageOrigin{
				Origin:       origin,
				LocalStorage: entries,
			})
		}
	}

	return state, nil
}

// LoadStorageState restores cookies browser-wide and localStorage per origin.
// For each origin referenced, the current page is navigated there briefly so
// the localStorage write lands in the right context.
func LoadStorageState(browser *rod.Browser, page *rod.Page, state *StorageState) error {
	if state == nil {
		return fmt.Errorf("nil storage state")
	}

	if len(state.Cookies) > 0 {
		params := make([]*proto.NetworkCookieParam, 0, len(state.Cookies))
		for _, c := range state.Cookies {
			params = append(params, &proto.NetworkCookieParam{
				Name:     c.Name,
				Value:    c.Value,
				Domain:   c.Domain,
				Path:     c.Path,
				Expires:  proto.TimeSinceEpoch(c.Expires),
				HTTPOnly: c.HTTPOnly,
				Secure:   c.Secure,
				SameSite: proto.NetworkCookieSameSite(c.SameSite),
			})
		}
		if err := browser.SetCookies(params); err != nil {
			return fmt.Errorf("set cookies: %w", err)
		}
	}

	if page != nil {
		for _, o := range state.Origins {
			if err := restoreLocalStorageAt(page, o); err != nil {
				return fmt.Errorf("restore localStorage for %s: %w", o.Origin, err)
			}
		}
	}

	return nil
}

// snapshotLocalStorage reads (origin, entries[]) for the current page.
func snapshotLocalStorage(page *rod.Page) (string, []StorageKeyValue, error) {
	res, err := page.Eval(`() => {
		const out = [];
		for (let i = 0; i < localStorage.length; i++) {
			const k = localStorage.key(i);
			out.push({name: k, value: localStorage.getItem(k)});
		}
		return {origin: location.origin, entries: out};
	}`)
	if err != nil {
		return "", nil, err
	}

	raw, err := res.Value.MarshalJSON()
	if err != nil {
		return "", nil, fmt.Errorf("marshal eval result: %w", err)
	}
	if len(raw) == 0 || string(raw) == "null" {
		return "", nil, nil
	}

	var payload struct {
		Origin  string            `json:"origin"`
		Entries []StorageKeyValue `json:"entries"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return "", nil, fmt.Errorf("decode localStorage payload: %w", err)
	}
	return payload.Origin, payload.Entries, nil
}

// restoreLocalStorageAt navigates to the origin and writes the entries.
// The navigation is cheap (about:blank would not share an origin storage
// partition) but saves and restores the caller's URL.
func restoreLocalStorageAt(page *rod.Page, o StorageOrigin) error {
	if len(o.LocalStorage) == 0 {
		return nil
	}

	info, _ := page.Info()
	current := ""
	if info != nil {
		current = info.URL
	}

	if err := page.Navigate(o.Origin); err != nil {
		return fmt.Errorf("navigate to %s: %w", o.Origin, err)
	}
	if err := page.WaitLoad(); err != nil {
		return err
	}

	if _, err := page.Eval(`(entries) => {
		for (const e of entries) {
			try { localStorage.setItem(e.name, e.value); } catch (ex) {}
		}
	}`, o.LocalStorage); err != nil {
		return err
	}

	if current != "" && current != o.Origin && current != o.Origin+"/" {
		if err := page.Navigate(current); err != nil {
			return fmt.Errorf("restore navigation to %s: %w", current, err)
		}
	}
	return nil
}
