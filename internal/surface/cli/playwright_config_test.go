package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
)

func TestApplyPlaywrightConfigMappedFields(t *testing.T) {
	restore := snapshotConfigGlobals()
	defer restore()

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	body := `{
  "browser": {
    "isolated": true,
    "userDataDir": "./pw-profile",
    "cdpEndpoint": "ws://127.0.0.1:9222/devtools/browser/test",
    "cdpHeaders": { "Authorization": "Bearer test" },
    "cdpTimeout": 1500,
    "launchOptions": {
      "headless": false,
      "executablePath": "./chrome-bin",
      "args": ["--disable-web-security", "--window-size=800,600"],
      "proxy": {
        "server": "http://proxy.test:8080",
        "bypass": "localhost,*.internal",
        "username": "user",
        "password": "pass"
      }
    }
  },
  "console": { "level": "warning" },
  "outputDir": "./artifacts",
	"outputMaxSize": 12345,
  "timeouts": { "navigation": 12500 }
}`
	if err := os.WriteFile(configPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	flagConfig = configPath
	flagHeadless = true
	flagTimeout = 30
	if err := applyPlaywrightConfig(&cobra.Command{Use: "test"}); err != nil {
		t.Fatalf("applyPlaywrightConfig: %v", err)
	}

	if loadedPlaywrightConfig == nil {
		t.Fatal("expected config to be loaded")
	}
	if flagHeadless {
		t.Fatal("expected config headless=false to apply")
	}
	if flagConnect != "ws://127.0.0.1:9222/devtools/browser/test" {
		t.Fatalf("flagConnect = %q", flagConnect)
	}
	if flagConfigCDPHeaders["Authorization"] != "Bearer test" {
		t.Fatalf("flagConfigCDPHeaders = %#v", flagConfigCDPHeaders)
	}
	if flagConfigCDPTimeoutMS != 1500 {
		t.Fatalf("flagConfigCDPTimeoutMS = %d", flagConfigCDPTimeoutMS)
	}
	if flagProxy != "http://user:pass@proxy.test:8080" {
		t.Fatalf("flagProxy = %q", flagProxy)
	}
	if flagProxyBypass != "localhost,*.internal" { //nolint:gosec // G101 false positive: "Bypass" is not a credential
		t.Fatalf("flagProxyBypass = %q", flagProxyBypass)
	}
	if flagConfigExecutablePath != filepath.Join(dir, "chrome-bin") {
		t.Fatalf("flagConfigExecutablePath = %q", flagConfigExecutablePath)
	}
	if flagConfigOutputDir != filepath.Join(dir, "artifacts") {
		t.Fatalf("flagConfigOutputDir = %q", flagConfigOutputDir)
	}
	if flagOutputMaxSize != 12345 {
		t.Fatalf("flagOutputMaxSize = %d", flagOutputMaxSize)
	}
	if flagConsoleLevel != "warning" {
		t.Fatalf("flagConsoleLevel = %q", flagConsoleLevel)
	}
	wantArgs := []string{"--disable-web-security", "--window-size=800,600"}
	if len(flagConfigLaunchArgs) != len(wantArgs) {
		t.Fatalf("flagConfigLaunchArgs = %#v, want %#v", flagConfigLaunchArgs, wantArgs)
	}
	for i := range wantArgs {
		if flagConfigLaunchArgs[i] != wantArgs[i] {
			t.Fatalf("flagConfigLaunchArgs = %#v, want %#v", flagConfigLaunchArgs, wantArgs)
		}
	}
	if flagUserDataDir != "./pw-profile" {
		t.Fatalf("flagUserDataDir = %q", flagUserDataDir)
	}
	if flagTimeout != 13 {
		t.Fatalf("flagTimeout = %d, want 13", flagTimeout)
	}
}

func TestApplyPlaywrightConfigEnvMappedFields(t *testing.T) {
	restore := snapshotConfigGlobals()
	defer restore()
	t.Setenv("PLAYWRIGHT_CLI_SESSION", "pw-work")
	t.Setenv("PLAYWRIGHT_MCP_HEADLESS", "false")
	t.Setenv("PLAYWRIGHT_MCP_CDP_ENDPOINT", "ws://127.0.0.1:9222/devtools/browser/env")
	t.Setenv("PLAYWRIGHT_MCP_ISOLATED", "true")
	t.Setenv("PLAYWRIGHT_MCP_USER_DATA_DIR", "./env-profile")
	t.Setenv("PLAYWRIGHT_MCP_EXECUTABLE_PATH", "/usr/bin/env-chrome")
	t.Setenv("PLAYWRIGHT_MCP_DEVICE", "iphone-14")
	t.Setenv("PLAYWRIGHT_MCP_STORAGE_STATE", "./env-state.json")
	t.Setenv("PLAYWRIGHT_MCP_VIEWPORT_SIZE", "1024x768")
	t.Setenv("PLAYWRIGHT_MCP_USER_AGENT", "EnvAgent/1.0")
	t.Setenv("PLAYWRIGHT_MCP_TIMEOUT_NAVIGATION", "2500")
	t.Setenv("PLAYWRIGHT_MCP_PROXY_SERVER", "http://proxy.env:8080")
	t.Setenv("PLAYWRIGHT_MCP_PROXY_BYPASS", "localhost")
	t.Setenv("PLAYWRIGHT_MCP_IGNORE_HTTPS_ERRORS", "true")
	t.Setenv("PLAYWRIGHT_MCP_OUTPUT_DIR", "/tmp/pw-output")
	t.Setenv("PLAYWRIGHT_MCP_OUTPUT_MAX_SIZE", "54321")
	t.Setenv("PLAYWRIGHT_MCP_CONSOLE_LEVEL", "debug")

	if err := applyPlaywrightConfig(&cobra.Command{Use: "test"}); err != nil {
		t.Fatalf("applyPlaywrightConfig: %v", err)
	}
	if flagHeadless {
		t.Fatal("expected PLAYWRIGHT_MCP_HEADLESS=false to apply")
	}
	if flagSession != "pw-work" {
		t.Fatalf("flagSession = %q", flagSession)
	}
	if flagConnect != "ws://127.0.0.1:9222/devtools/browser/env" {
		t.Fatalf("flagConnect = %q", flagConnect)
	}
	if flagUserDataDir != "./env-profile" {
		t.Fatalf("flagUserDataDir = %q", flagUserDataDir)
	}
	if flagConfigExecutablePath != "/usr/bin/env-chrome" {
		t.Fatalf("flagConfigExecutablePath = %q", flagConfigExecutablePath)
	}
	if flagConfigDevice != "iphone-14" {
		t.Fatalf("flagConfigDevice = %q", flagConfigDevice)
	}
	if flagConfigStorageState != "./env-state.json" {
		t.Fatalf("flagConfigStorageState = %q", flagConfigStorageState)
	}
	if flagConfigViewportW != 1024 || flagConfigViewportH != 768 {
		t.Fatalf("viewport = %dx%d", flagConfigViewportW, flagConfigViewportH)
	}
	if flagConfigUserAgent != "EnvAgent/1.0" {
		t.Fatalf("flagConfigUserAgent = %q", flagConfigUserAgent)
	}
	if flagTimeout != 3 {
		t.Fatalf("flagTimeout = %d", flagTimeout)
	}
	if flagProxy != "http://proxy.env:8080" {
		t.Fatalf("flagProxy = %q", flagProxy)
	}
	if flagProxyBypass != "localhost" {
		t.Fatalf("flagProxyBypass = %q", flagProxyBypass)
	}
	if flagConfigOutputDir != "/tmp/pw-output" {
		t.Fatalf("flagConfigOutputDir = %q", flagConfigOutputDir)
	}
	if flagOutputMaxSize != 54321 {
		t.Fatalf("flagOutputMaxSize = %d", flagOutputMaxSize)
	}
	if flagConsoleLevel != "debug" {
		t.Fatalf("flagConsoleLevel = %q", flagConsoleLevel)
	}
	if !flagConfigIgnoreHTTPSErr {
		t.Fatal("expected PLAYWRIGHT_MCP_IGNORE_HTTPS_ERRORS=true to apply")
	}
}

func TestProxyURLWithAuth(t *testing.T) {
	got := proxyURLWithAuth("http://proxy.test:8080", "user", "pass")
	if got != "http://user:pass@proxy.test:8080" {
		t.Fatalf("proxyURLWithAuth = %q", got)
	}
	if got := proxyURLWithAuth("proxy.test:8080", "user", "pass"); got != "proxy.test:8080" {
		t.Fatalf("proxyURLWithAuth invalid url = %q", got)
	}
}

func TestApplyPlaywrightConfigEnvUnsupportedWithoutConfig(t *testing.T) {
	restore := snapshotConfigGlobals()
	defer restore()
	t.Setenv("PLAYWRIGHT_MCP_BROWSER", "firefox")
	t.Setenv("PLAYWRIGHT_MCP_ISOLATED", "false")
	t.Setenv("PLAYWRIGHT_MCP_VIEWPORT_SIZE", "wide")
	t.Setenv("PLAYWRIGHT_MCP_DEVICE", "iPhone 99")
	t.Setenv("PLAYWRIGHT_MCP_IGNORE_HTTPS_ERRORS", "maybe")
	t.Setenv("PLAYWRIGHT_MCP_CONSOLE_LEVEL", "verbose")

	if err := applyPlaywrightConfig(&cobra.Command{Use: "test"}); err != nil {
		t.Fatalf("applyPlaywrightConfig: %v", err)
	}
	if loadedPlaywrightConfig != nil {
		t.Fatalf("loadedPlaywrightConfig = %#v, want nil", loadedPlaywrightConfig)
	}
	want := map[string]bool{
		"PLAYWRIGHT_MCP_BROWSER=firefox":       false,
		"PLAYWRIGHT_MCP_ISOLATED=false":        false,
		"PLAYWRIGHT_MCP_VIEWPORT_SIZE":         false,
		"PLAYWRIGHT_MCP_DEVICE=iPhone 99":      false,
		"PLAYWRIGHT_MCP_IGNORE_HTTPS_ERRORS":   false,
		"PLAYWRIGHT_MCP_CONSOLE_LEVEL=verbose": false,
	}
	for _, field := range playwrightUnsupportedFields {
		if _, ok := want[field]; ok {
			want[field] = true
		}
	}
	for field, seen := range want {
		if !seen {
			t.Fatalf("missing unsupported field %q in %v", field, playwrightUnsupportedFields)
		}
	}
}

func TestApplyPlaywrightConfigEnvDoesNotOverrideChangedFlags(t *testing.T) {
	restore := snapshotConfigGlobals()
	defer restore()
	t.Setenv("PLAYWRIGHT_MCP_HEADLESS", "false")
	t.Setenv("PLAYWRIGHT_MCP_TIMEOUT_NAVIGATION", "2500")
	t.Setenv("PLAYWRIGHT_MCP_CDP_ENDPOINT", "ws://env")
	t.Setenv("PLAYWRIGHT_MCP_CONSOLE_LEVEL", "debug")
	t.Setenv("PLAYWRIGHT_CLI_SESSION", "env-session")

	flagHeadless = true
	flagTimeout = 42
	flagConnect = "ws://flag"
	flagConsoleLevel = "error"
	flagSession = "flag-session"
	cmd := &cobra.Command{Use: "test"}
	cmd.Flags().Bool("headless", true, "")
	cmd.Flags().Int("timeout", 42, "")
	cmd.Flags().String("connect", "ws://flag", "")
	cmd.Flags().String("level", "error", "")
	cmd.Flags().String("session", "flag-session", "")
	_ = cmd.Flags().Set("headless", "true")
	_ = cmd.Flags().Set("timeout", "42")
	_ = cmd.Flags().Set("connect", "ws://flag")
	_ = cmd.Flags().Set("level", "error")
	_ = cmd.Flags().Set("session", "flag-session")

	if err := applyPlaywrightConfig(cmd); err != nil {
		t.Fatalf("applyPlaywrightConfig: %v", err)
	}
	if !flagHeadless {
		t.Fatal("env should not override changed --headless")
	}
	if flagTimeout != 42 {
		t.Fatalf("flagTimeout = %d", flagTimeout)
	}
	if flagConnect != "ws://flag" {
		t.Fatalf("flagConnect = %q", flagConnect)
	}
	if flagConsoleLevel != "error" {
		t.Fatalf("flagConsoleLevel = %q", flagConsoleLevel)
	}
	if flagSession != "flag-session" {
		t.Fatalf("flagSession = %q", flagSession)
	}
}

func TestSessionNameFromEnvPrefersPlaywrightSession(t *testing.T) {
	t.Setenv("PLAYWRIGHT_CLI_SESSION", "pw")
	t.Setenv("GHOSTCHROME_SESSION", "ghost")
	if got := sessionNameFromEnv(); got != "pw" {
		t.Fatalf("sessionNameFromEnv = %q, want pw", got)
	}
}

func TestImplicitSessionEnabled(t *testing.T) {
	tests := []struct {
		env  string
		want bool
	}{
		{"", true},
		{"0", true},
		{"1", false},
		{"true", false},
		{"TRUE", false},
		{"false", true},
	}
	for _, tt := range tests {
		t.Run(tt.env, func(t *testing.T) {
			t.Setenv("GHOSTCHROME_NO_DAEMON", tt.env)
			if got := implicitSessionEnabled(); got != tt.want {
				t.Fatalf("implicitSessionEnabled() with GHOSTCHROME_NO_DAEMON=%q = %v, want %v", tt.env, got, tt.want)
			}
		})
	}
}

func TestApplyPlaywrightConfigPathEnv(t *testing.T) {
	restore := snapshotConfigGlobals()
	defer restore()

	dir := t.TempDir()
	configPath := filepath.Join(dir, "env-config.json")
	if err := os.WriteFile(configPath, []byte(`{"timeouts":{"navigation":4000}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PLAYWRIGHT_MCP_CONFIG", configPath)

	if err := applyPlaywrightConfig(&cobra.Command{Use: "test"}); err != nil {
		t.Fatalf("applyPlaywrightConfig: %v", err)
	}
	if loadedPlaywrightConfig == nil || loadedPlaywrightConfig.Path != configPath {
		t.Fatalf("loaded config = %#v, want path %q", loadedPlaywrightConfig, configPath)
	}
	if flagTimeout != 4 {
		t.Fatalf("flagTimeout = %d", flagTimeout)
	}
}

func TestApplyPlaywrightConfigSaveVideo(t *testing.T) {
	restore := snapshotConfigGlobals()
	defer restore()

	flagConfig = filepath.Join(t.TempDir(), "config.json")
	body := `{"saveVideo": {"width": 800, "height": 600}}`
	if err := os.WriteFile(flagConfig, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := applyPlaywrightConfig(&cobra.Command{Use: "test"}); err != nil {
		t.Fatalf("applyPlaywrightConfig: %v", err)
	}
	if flagAutoVideoSize != "800x600" {
		t.Fatalf("flagAutoVideoSize = %q", flagAutoVideoSize)
	}
	if flagAutoVideoSource != "config.saveVideo" {
		t.Fatalf("flagAutoVideoSource = %q", flagAutoVideoSource)
	}
}

func TestApplyPlaywrightConfigSaveVideoEnv(t *testing.T) {
	restore := snapshotConfigGlobals()
	defer restore()
	t.Setenv("PLAYWRIGHT_MCP_SAVE_VIDEO", "1024x768")

	if err := applyPlaywrightConfig(&cobra.Command{Use: "test"}); err != nil {
		t.Fatalf("applyPlaywrightConfig: %v", err)
	}
	if flagAutoVideoSize != "1024x768" {
		t.Fatalf("flagAutoVideoSize = %q", flagAutoVideoSize)
	}
	if flagAutoVideoSource != "PLAYWRIGHT_MCP_SAVE_VIDEO" {
		t.Fatalf("flagAutoVideoSource = %q", flagAutoVideoSource)
	}
}

func TestApplyPlaywrightConfigPermissionsEnv(t *testing.T) {
	restore := snapshotConfigGlobals()
	defer restore()
	t.Setenv("PLAYWRIGHT_MCP_GRANT_PERMISSIONS", "geolocation, clipboard-read")

	if err := applyPlaywrightConfig(&cobra.Command{Use: "test"}); err != nil {
		t.Fatalf("applyPlaywrightConfig: %v", err)
	}
	want := []string{"geolocation", "clipboard-read"}
	if len(flagConfigPermissions) != len(want) {
		t.Fatalf("flagConfigPermissions = %#v, want %#v", flagConfigPermissions, want)
	}
	for i := range want {
		if flagConfigPermissions[i] != want[i] {
			t.Fatalf("flagConfigPermissions = %#v, want %#v", flagConfigPermissions, want)
		}
	}
}

func TestApplyPlaywrightConfigServiceWorkersEnv(t *testing.T) {
	restore := snapshotConfigGlobals()
	defer restore()
	t.Setenv("PLAYWRIGHT_MCP_BLOCK_SERVICE_WORKERS", "true")

	if err := applyPlaywrightConfig(&cobra.Command{Use: "test"}); err != nil {
		t.Fatalf("applyPlaywrightConfig: %v", err)
	}
	if flagConfigServiceWorkers != "block" {
		t.Fatalf("flagConfigServiceWorkers = %q", flagConfigServiceWorkers)
	}
}

func TestApplyPlaywrightConfigInitScripts(t *testing.T) {
	restore := snapshotConfigGlobals()
	defer restore()

	dir := t.TempDir()
	flagConfig = filepath.Join(dir, "config.json")
	body := `{"browser": {"initScript": ["./setup.js", "nested/extra.js"]}}`
	if err := os.WriteFile(flagConfig, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := applyPlaywrightConfig(&cobra.Command{Use: "test"}); err != nil {
		t.Fatalf("applyPlaywrightConfig: %v", err)
	}
	want := []string{
		filepath.Join(dir, "setup.js"),
		filepath.Join(dir, "nested", "extra.js"),
	}
	if len(flagConfigInitScripts) != len(want) {
		t.Fatalf("flagConfigInitScripts = %#v, want %#v", flagConfigInitScripts, want)
	}
	for i := range want {
		if flagConfigInitScripts[i] != want[i] {
			t.Fatalf("flagConfigInitScripts = %#v, want %#v", flagConfigInitScripts, want)
		}
	}
	for _, field := range loadedPlaywrightConfig.Unsupported {
		if field == "browser.initScript" {
			t.Fatalf("browser.initScript should be supported now: %v", loadedPlaywrightConfig.Unsupported)
		}
	}
}

func TestApplyPlaywrightConfigInitScriptEnv(t *testing.T) {
	restore := snapshotConfigGlobals()
	defer restore()
	t.Setenv("PLAYWRIGHT_MCP_INIT_SCRIPT", "env-setup.js, nested/env-extra.js")

	if err := applyPlaywrightConfig(&cobra.Command{Use: "test"}); err != nil {
		t.Fatalf("applyPlaywrightConfig: %v", err)
	}
	want := []string{"env-setup.js", "nested/env-extra.js"}
	if len(flagConfigInitScripts) != len(want) {
		t.Fatalf("flagConfigInitScripts = %#v, want %#v", flagConfigInitScripts, want)
	}
	for i := range want {
		if flagConfigInitScripts[i] != want[i] {
			t.Fatalf("flagConfigInitScripts = %#v, want %#v", flagConfigInitScripts, want)
		}
	}
}

func TestApplyPlaywrightConfigContextOptions(t *testing.T) {
	restore := snapshotConfigGlobals()
	defer restore()

	dir := t.TempDir()
	flagConfig = filepath.Join(dir, "config.json")
	body := `{
  "browser": {
    "contextOptions": {
      "viewport": { "width": 1280, "height": 720 },
      "userAgent": "TestAgent/1.0",
      "storageState": "auth/state.json",
      "locale": "fr-FR",
      "permissions": ["geolocation", "clipboard-read"],
      "serviceWorkers": "block"
    }
  }
}`
	if err := os.WriteFile(flagConfig, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := applyPlaywrightConfig(&cobra.Command{Use: "test"}); err != nil {
		t.Fatalf("applyPlaywrightConfig: %v", err)
	}
	if flagConfigViewportW != 1280 || flagConfigViewportH != 720 {
		t.Fatalf("viewport = %dx%d", flagConfigViewportW, flagConfigViewportH)
	}
	if flagConfigUserAgent != "TestAgent/1.0" {
		t.Fatalf("flagConfigUserAgent = %q", flagConfigUserAgent)
	}
	wantStorageState := filepath.Join(dir, "auth", "state.json")
	if flagConfigStorageState != wantStorageState {
		t.Fatalf("flagConfigStorageState = %q, want %q", flagConfigStorageState, wantStorageState)
	}
	if flagConfigLocale != "fr-FR" {
		t.Fatalf("flagConfigLocale = %q", flagConfigLocale)
	}
	if len(flagConfigPermissions) != 2 || flagConfigPermissions[0] != "geolocation" || flagConfigPermissions[1] != "clipboard-read" {
		t.Fatalf("flagConfigPermissions = %#v", flagConfigPermissions)
	}
	if flagConfigServiceWorkers != "block" {
		t.Fatalf("flagConfigServiceWorkers = %q", flagConfigServiceWorkers)
	}
	for _, field := range loadedPlaywrightConfig.Unsupported {
		if field == "browser.contextOptions.viewport" || field == "browser.contextOptions.userAgent" || field == "browser.contextOptions.storageState" || field == "browser.contextOptions.locale" || field == "browser.contextOptions.permissions" || field == "browser.contextOptions.serviceWorkers" {
			t.Fatalf("field should be supported now: %s", field)
		}
	}
}

func TestApplyPlaywrightConfigReportsUnsupportedFields(t *testing.T) {
	restore := snapshotConfigGlobals()
	defer restore()

	flagConfig = filepath.Join(t.TempDir(), "config.json")
	body := `{
  "browser": {
    "browserName": "firefox",
    "isolated": false,
    "cdpHeaders": { "": "bad" },
    "cdpTimeout": -1,
    "initScript": [42],
    "launchOptions": { "args": ["--foo", "plain"] },
    "contextOptions": {
      "locale": "fr-FR",
      "permissions": ["geolocation", "unknown-permission"],
      "serviceWorkers": "maybe"
    }
  },
  "console": { "level": "verbose" }
}`
	if err := os.WriteFile(flagConfig, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := applyPlaywrightConfig(&cobra.Command{Use: "test"}); err != nil {
		t.Fatalf("applyPlaywrightConfig: %v", err)
	}
	if loadedPlaywrightConfig == nil {
		t.Fatal("expected config to be loaded")
	}
	want := map[string]bool{
		"browser.browserName=firefox":                           false,
		"browser.isolated=false":                                false,
		"browser.launchOptions.args=plain":                      false,
		"browser.cdpHeaders=":                                   false,
		"browser.cdpTimeout":                                    false,
		"browser.initScript":                                    false,
		"console.level=verbose":                                 false,
		"browser.contextOptions.permissions=unknown-permission": false,
		"browser.contextOptions.serviceWorkers=maybe":           false,
	}
	for _, field := range loadedPlaywrightConfig.Unsupported {
		if _, ok := want[field]; ok {
			want[field] = true
		}
	}
	for field, seen := range want {
		if !seen {
			t.Fatalf("missing unsupported field %q in %v", field, loadedPlaywrightConfig.Unsupported)
		}
	}
}

func TestApplyOpenCompatFlags(t *testing.T) {
	restore := snapshotConfigGlobals()
	defer restore()

	flagOpenBrowser = "chrome"
	flagOpenPersistent = true
	undo, err := applyOpenCompatFlags(&cobra.Command{Use: "open"})
	if err != nil {
		t.Fatalf("applyOpenCompatFlags: %v", err)
	}
	if flagUserProfile != "default" {
		t.Fatalf("flagUserProfile = %q, want default", flagUserProfile)
	}
	undo()
	if flagUserProfile != "" {
		t.Fatalf("restore flagUserProfile = %q", flagUserProfile)
	}
}

func TestApplyOpenCompatRejectsUnsupportedBrowser(t *testing.T) {
	restore := snapshotConfigGlobals()
	defer restore()

	flagOpenBrowser = "firefox"
	if _, err := applyOpenCompatFlags(&cobra.Command{Use: "open"}); err == nil {
		t.Fatal("expected unsupported browser error")
	}
}

func TestApplyOpenCompatRejectsUnsupportedConfiguredBrowser(t *testing.T) {
	restore := snapshotConfigGlobals()
	defer restore()

	flagOpenBrowser = "chrome"
	loadedPlaywrightConfig = &loadedConfigState{
		Config: playwrightCLIConfig{Browser: &struct {
			BrowserName    string            `json:"browserName"`
			Isolated       *bool             `json:"isolated"`
			UserDataDir    string            `json:"userDataDir"`
			CDPEndpoint    string            `json:"cdpEndpoint"`
			CDPHeaders     map[string]string `json:"cdpHeaders"`
			CDPTimeout     int               `json:"cdpTimeout"`
			RemoteEndpoint string            `json:"remoteEndpoint"`
			InitPage       any               `json:"initPage"`
			InitScript     any               `json:"initScript"`
			LaunchOptions  *struct {
				Channel        string   `json:"channel"`
				Headless       *bool    `json:"headless"`
				ExecutablePath string   `json:"executablePath"`
				Args           []string `json:"args"`
				Proxy          *struct {
					Server   string `json:"server"`
					Bypass   string `json:"bypass"`
					Username string `json:"username"`
					Password string `json:"password"`
				} `json:"proxy"`
			} `json:"launchOptions"`
			ContextOptions *struct {
				Viewport       *struct{ Width, Height int } `json:"viewport"`
				Locale         string                       `json:"locale"`
				UserAgent      string                       `json:"userAgent"`
				StorageState   string                       `json:"storageState"`
				Permissions    []string                     `json:"permissions"`
				ServiceWorkers string                       `json:"serviceWorkers"`
			} `json:"contextOptions"`
		}{BrowserName: "webkit"}},
	}
	if _, err := applyOpenCompatFlags(&cobra.Command{Use: "open"}); err == nil {
		t.Fatal("expected unsupported configured browser error")
	}
}

func snapshotConfigGlobals() func() {
	oldConfig := flagConfig
	oldSession := flagSession
	oldHeadless := flagHeadless
	oldConnect := flagConnect
	oldProxy := flagProxy
	oldProxyBypass := flagProxyBypass
	oldUserDataDir := flagUserDataDir
	oldUserProfile := flagUserProfile
	oldTimeout := flagTimeout
	oldLoaded := loadedPlaywrightConfig
	oldUnsupported := playwrightUnsupportedFields
	oldOpenBrowser := flagOpenBrowser
	oldOpenPersistent := flagOpenPersistent
	oldOpenProfileDir := flagOpenProfileDir
	oldAutoVideoSize := flagAutoVideoSize
	oldAutoVideoSource := flagAutoVideoSource
	oldConsoleLevel := flagConsoleLevel
	oldConfigViewportW := flagConfigViewportW
	oldConfigViewportH := flagConfigViewportH
	oldConfigUserAgent := flagConfigUserAgent
	oldConfigStorageState := flagConfigStorageState
	oldConfigLocale := flagConfigLocale
	oldConfigPermissions := flagConfigPermissions
	oldConfigServiceWorkers := flagConfigServiceWorkers
	oldConfigInitScripts := flagConfigInitScripts
	oldConfigExecutablePath := flagConfigExecutablePath
	oldConfigLaunchArgs := flagConfigLaunchArgs
	oldConfigOutputDir := flagConfigOutputDir
	oldOutputMaxSize := flagOutputMaxSize
	oldConfigDevice := flagConfigDevice
	oldConfigIgnoreHTTPSErr := flagConfigIgnoreHTTPSErr
	oldConfigCDPHeaders := flagConfigCDPHeaders
	oldConfigCDPTimeoutMS := flagConfigCDPTimeoutMS
	return func() {
		flagConfig = oldConfig
		flagSession = oldSession
		flagHeadless = oldHeadless
		flagConnect = oldConnect
		flagProxy = oldProxy
		flagProxyBypass = oldProxyBypass
		flagUserDataDir = oldUserDataDir
		flagUserProfile = oldUserProfile
		flagTimeout = oldTimeout
		loadedPlaywrightConfig = oldLoaded
		playwrightUnsupportedFields = oldUnsupported
		flagOpenBrowser = oldOpenBrowser
		flagOpenPersistent = oldOpenPersistent
		flagOpenProfileDir = oldOpenProfileDir
		flagAutoVideoSize = oldAutoVideoSize
		flagAutoVideoSource = oldAutoVideoSource
		flagConsoleLevel = oldConsoleLevel
		flagConfigViewportW = oldConfigViewportW
		flagConfigViewportH = oldConfigViewportH
		flagConfigUserAgent = oldConfigUserAgent
		flagConfigStorageState = oldConfigStorageState
		flagConfigLocale = oldConfigLocale
		flagConfigPermissions = oldConfigPermissions
		flagConfigServiceWorkers = oldConfigServiceWorkers
		flagConfigInitScripts = oldConfigInitScripts
		flagConfigExecutablePath = oldConfigExecutablePath
		flagConfigLaunchArgs = oldConfigLaunchArgs
		flagConfigOutputDir = oldConfigOutputDir
		flagOutputMaxSize = oldOutputMaxSize
		flagConfigDevice = oldConfigDevice
		flagConfigIgnoreHTTPSErr = oldConfigIgnoreHTTPSErr
		flagConfigCDPHeaders = oldConfigCDPHeaders
		flagConfigCDPTimeoutMS = oldConfigCDPTimeoutMS
	}
}
