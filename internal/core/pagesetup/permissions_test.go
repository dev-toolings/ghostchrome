package pagesetup

import (
	"reflect"
	"testing"

	"github.com/go-rod/rod/lib/proto"
)

func TestMapPlaywrightPermissions(t *testing.T) {
	mapped, unsupported := MapPlaywrightPermissions([]string{
		"geolocation",
		"clipboard-read",
		"clipboard-write",
		"unknown",
	})
	want := []proto.BrowserPermissionType{
		proto.BrowserPermissionTypeGeolocation,
		proto.BrowserPermissionTypeClipboardReadWrite,
		proto.BrowserPermissionTypeClipboardSanitizedWrite,
	}
	if !reflect.DeepEqual(mapped, want) {
		t.Fatalf("mapped = %#v, want %#v", mapped, want)
	}
	if !reflect.DeepEqual(unsupported, []string{"unknown"}) {
		t.Fatalf("unsupported = %#v", unsupported)
	}
}

func TestMapPlaywrightPermissionsDedupesCDPValues(t *testing.T) {
	mapped, unsupported := MapPlaywrightPermissions([]string{"clipboard-read", "clipboard-read"})
	if len(unsupported) != 0 {
		t.Fatalf("unsupported = %#v", unsupported)
	}
	if !reflect.DeepEqual(mapped, []proto.BrowserPermissionType{proto.BrowserPermissionTypeClipboardReadWrite}) {
		t.Fatalf("mapped = %#v", mapped)
	}
}
