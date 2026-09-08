package interact

import (
	"fmt"
	"net/url"

	"github.com/dev-toolings/ghostchrome/engine"
	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

// ClipboardRead returns the current clipboard text content.
func ClipboardRead(page *rod.Page) (string, error) {
	if err := engine.ActivePolicy.AllowAction("clipboard"); err != nil {
		return "", err
	}
	if err := grantClipboardPermission(page); err != nil {
		return "", err
	}
	res, err := page.Eval(`async () => await navigator.clipboard.readText()`)
	if err != nil {
		return "", fmt.Errorf("clipboard read: %w", err)
	}
	return res.Value.Str(), nil
}

// ClipboardWrite sets the clipboard text content.
func ClipboardWrite(page *rod.Page, text string) error {
	if err := engine.ActivePolicy.AllowAction("clipboard"); err != nil {
		return err
	}
	if err := grantClipboardPermission(page); err != nil {
		return err
	}
	_, err := page.Eval(`async (text) => { await navigator.clipboard.writeText(text); }`, text)
	if err != nil {
		return fmt.Errorf("clipboard write: %w", err)
	}
	return nil
}

func grantClipboardPermission(page *rod.Page) error {
	info, err := page.Info()
	if err != nil {
		return fmt.Errorf("page info: %w", err)
	}
	origin := pageOrigin(info.URL)
	if origin == "" {
		return fmt.Errorf("cannot determine page origin for clipboard permission")
	}
	err = proto.BrowserGrantPermissions{
		Permissions: []proto.BrowserPermissionType{"clipboardReadWrite", "clipboardSanitizedWrite"},
		Origin:      origin,
	}.Call(page.Browser())
	if err != nil {
		return fmt.Errorf("grant clipboard permission: %w", err)
	}
	return nil
}

func pageOrigin(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return u.Scheme + "://" + u.Host
}
