package engine

import "testing"

func TestIsStaticNetworkEntry(t *testing.T) {
	tests := []struct {
		name         string
		resourceType string
		mimeType     string
		want         bool
	}{
		{name: "image type", resourceType: "Image", mimeType: "image/png", want: true},
		{name: "script type", resourceType: "Script", mimeType: "application/javascript", want: true},
		{name: "stylesheet type", resourceType: "Stylesheet", mimeType: "text/css", want: true},
		{name: "font mime", resourceType: "Other", mimeType: "font/woff2", want: true},
		{name: "json api", resourceType: "Fetch", mimeType: "application/json", want: false},
		{name: "document", resourceType: "Document", mimeType: "text/html", want: false},
		{name: "text/javascript", resourceType: "Other", mimeType: "text/javascript", want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsStaticNetworkEntry(tt.resourceType, tt.mimeType)
			if got != tt.want {
				t.Fatalf("IsStaticNetworkEntry(%q, %q) = %v, want %v", tt.resourceType, tt.mimeType, got, tt.want)
			}
		})
	}
}

func TestCaptureSessionStopIdempotent(t *testing.T) {
	s := &CaptureSession{done: make(chan struct{})}
	close(s.done)
	if _, err := s.Stop(); err != nil {
		t.Fatalf("first stop: %v", err)
	}
	if _, err := s.Stop(); err != nil {
		t.Fatalf("second stop: %v", err)
	}
}
