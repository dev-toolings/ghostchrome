package engine

import (
	"strings"
	"testing"

	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/launcher/flags"
)

func TestNewLauncherProxyDoesNotIgnoreCertErrors(t *testing.T) {
	const proxy = "http://127.0.0.1:8080"

	t.Run("proxy alone keeps TLS verification", func(t *testing.T) {
		l := NewLauncher(LauncherOpts{Proxy: proxy})
		if !l.Has(flags.ProxyServer) {
			t.Fatal("expected --proxy-server to be set")
		}
		if got := l.Get(flags.ProxyServer); got != proxy {
			t.Fatalf("proxy-server = %q, want %q", got, proxy)
		}
		assertNoIgnoreCertificateErrors(t, l)
	})

	t.Run("proxy plus IgnoreCertErrors sets the Chromium flag", func(t *testing.T) {
		l := NewLauncher(LauncherOpts{Proxy: proxy, IgnoreCertErrors: true})
		if !l.Has("ignore-certificate-errors") {
			t.Fatal("expected --ignore-certificate-errors when IgnoreCertErrors is true")
		}
	})

	t.Run("IgnoreCertErrors without proxy still works", func(t *testing.T) {
		l := NewLauncher(LauncherOpts{IgnoreCertErrors: true})
		if l.Has(flags.ProxyServer) {
			t.Fatal("did not expect --proxy-server")
		}
		if !l.Has("ignore-certificate-errors") {
			t.Fatal("expected --ignore-certificate-errors when IgnoreCertErrors is true")
		}
	})

	t.Run("empty opts do not skip TLS", func(t *testing.T) {
		l := NewLauncher(LauncherOpts{})
		assertNoIgnoreCertificateErrors(t, l)
	})
}

func assertNoIgnoreCertificateErrors(t *testing.T, l *launcher.Launcher) {
	t.Helper()
	if l.Has("ignore-certificate-errors") {
		t.Fatal("proxy must not set --ignore-certificate-errors unless IgnoreCertErrors is true")
	}
	for _, arg := range l.FormatArgs() {
		name, _, ok := splitChromiumArg(arg)
		if ok && name == "ignore-certificate-errors" {
			t.Fatalf("FormatArgs included %q", arg)
		}
		if strings.Contains(arg, "ignore-certificate-errors") {
			t.Fatalf("FormatArgs included %q", arg)
		}
	}
}
