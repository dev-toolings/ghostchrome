package cli

import "testing"

func TestBuildUnsupportedAttachResultEndpoint(t *testing.T) {
	got := buildUnsupportedAttachResult("endpoint", "ws://localhost:3000")
	if got.Supported {
		t.Fatal("expected unsupported")
	}
	if got.Mode != "endpoint" || got.Value != "ws://localhost:3000" {
		t.Fatalf("unexpected result: %#v", got)
	}
	if got.Reason == "" || got.Alternative == "" {
		t.Fatalf("expected reason and alternative: %#v", got)
	}
}

func TestBuildUnsupportedAttachResultExtension(t *testing.T) {
	got := buildUnsupportedAttachResult("extension", "chrome-canary")
	if got.Supported {
		t.Fatal("expected unsupported")
	}
	if got.Mode != "extension" || got.Value != "chrome-canary" {
		t.Fatalf("unexpected result: %#v", got)
	}
	if got.Reason == "" || got.Alternative == "" {
		t.Fatalf("expected reason and alternative: %#v", got)
	}
}
