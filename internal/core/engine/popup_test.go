package engine

import "testing"

func TestRefMayOpenPopup(t *testing.T) {
	t.Parallel()
	snap := &PageSnapshot{Refs: map[string]RefSnapshot{
		"@1": {Role: "button", Name: "Go"},
		"@2": {Role: "link", Name: "Docs", Href: "https://example.com/docs"},
		"@3": {Role: "link", Name: "Popup", Href: "javascript:window.open('/x')"},
		"@4": {Role: "link", Name: "Help (opens in a new tab)", Href: "https://example.com/help"},
		"@5": {Role: "link", Name: "Open in new window", Href: "https://example.com/w"},
	}}
	if RefMayOpenPopup(snap, "@1") {
		t.Fatal("buttons must not scan tabs")
	}
	if RefMayOpenPopup(snap, "@2") {
		t.Fatal("ordinary same-tab links must not scan tabs")
	}
	if !RefMayOpenPopup(snap, "@3") {
		t.Fatal("javascript:window.open must scan tabs")
	}
	if !RefMayOpenPopup(snap, "@4") {
		t.Fatal("opens-in-a-new-tab name must scan tabs")
	}
	if !RefMayOpenPopup(snap, "@5") {
		t.Fatal("open-in-new name must scan tabs")
	}
	if RefMayOpenPopup(nil, "@1") {
		t.Fatal("nil snapshot must not scan tabs")
	}
}

func TestPopupWaitAfterClickUsesLiveHint(t *testing.T) {
	t.Parallel()
	snap := &PageSnapshot{Refs: map[string]RefSnapshot{
		"@1": {Role: "link", Name: "Docs", Href: "https://example.com/docs"},
	}}
	if popupWaitAfterClick(nil, snap, "@1") != 0 {
		t.Fatal("nil page must not wait")
	}
}
