package cli

import (
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/dev-toolings/ghostchrome/engine"
	"github.com/dev-toolings/ghostchrome/internal/core/pagesetup"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

type playwrightCLIConfig struct {
	Browser *struct {
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
	} `json:"browser"`
	OutputDir     string                  `json:"outputDir"`
	OutputMode    string                  `json:"outputMode"`
	OutputMaxSize int                     `json:"outputMaxSize"`
	Console       *struct{ Level string } `json:"console"`
	Network       *struct {
		AllowedOrigins []string `json:"allowedOrigins"`
		BlockedOrigins []string `json:"blockedOrigins"`
	} `json:"network"`
	Timeouts *struct {
		Action     int `json:"action"`
		Navigation int `json:"navigation"`
		Expect     int `json:"expect"`
	} `json:"timeouts"`
	Extension                   *bool             `json:"extension"`
	SaveVideo                   any               `json:"saveVideo"`
	SaveSession                 *bool             `json:"saveSession"`
	SharedBrowserContext        *bool             `json:"sharedBrowserContext"`
	Snapshot                    any               `json:"snapshot"`
	ImageResponses              string            `json:"imageResponses"`
	Secrets                     map[string]string `json:"secrets"`
	TestIDAttribute             string            `json:"testIdAttribute"`
	AllowUnrestrictedFileAccess *bool             `json:"allowUnrestrictedFileAccess"`
	Codegen                     string            `json:"codegen"`
}

type loadedConfigState struct {
	Path        string
	Config      playwrightCLIConfig
	Unsupported []string
}

var loadedPlaywrightConfig *loadedConfigState
var playwrightUnsupportedFields []string

func applyPlaywrightConfig(cmd *cobra.Command) error {
	flagAutoVideoSize = ""
	flagAutoVideoSource = ""
	flagConfigViewportW = 0
	flagConfigViewportH = 0
	flagConfigUserAgent = ""
	flagConfigStorageState = ""
	flagConfigLocale = ""
	flagConfigPermissions = nil
	flagConfigServiceWorkers = ""
	flagConfigInitScripts = nil
	flagConfigExecutablePath = ""
	flagConfigLaunchArgs = nil
	flagConfigOutputDir = ""
	flagConfigDevice = ""
	flagConfigIgnoreHTTPSErr = false
	flagConfigCDPHeaders = nil
	flagConfigCDPTimeoutMS = 0
	flagProxyBypass = ""
	playwrightUnsupportedFields = nil
	path, explicit, err := resolvePlaywrightConfigPath()
	if err != nil {
		return err
	}
	if path == "" {
		unsupported := []string{}
		applyAutoVideoConfig(&playwrightCLIConfig{}, &unsupported)
		applyPlaywrightEnvConfig(cmd, &unsupported)
		applyPermissionsEnv(&unsupported)
		applyServiceWorkersEnv(&unsupported)
		playwrightUnsupportedFields = unsupported
		loadedPlaywrightConfig = nil
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if explicit {
			return fmt.Errorf("load config %s: %w", path, err)
		}
		loadedPlaywrightConfig = nil
		return nil
	}
	var cfg playwrightCLIConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return fmt.Errorf("parse config %s: %w", path, err)
	}
	state := &loadedConfigState{Path: path, Config: cfg}
	applyConfigValues(cmd, &cfg, filepath.Dir(path), &state.Unsupported)
	applyPlaywrightEnvConfig(cmd, &state.Unsupported)
	applyPermissionsEnv(&state.Unsupported)
	applyServiceWorkersEnv(&state.Unsupported)
	playwrightUnsupportedFields = state.Unsupported
	loadedPlaywrightConfig = state
	return nil
}

func resolvePlaywrightConfigPath() (path string, explicit bool, err error) {
	if flagConfig != "" {
		abs, err := filepath.Abs(flagConfig)
		return abs, true, err
	}
	if env := strings.TrimSpace(os.Getenv("PLAYWRIGHT_MCP_CONFIG")); env != "" {
		abs, err := filepath.Abs(env)
		return abs, true, err
	}
	path = filepath.Join(".playwright", "cli.config.json")
	if _, err := os.Stat(path); err == nil {
		abs, err := filepath.Abs(path)
		return abs, false, err
	}
	return "", false, nil
}

func applyPlaywrightEnvConfig(cmd *cobra.Command, unsupported *[]string) {
	if env := strings.TrimSpace(os.Getenv("PLAYWRIGHT_CLI_SESSION")); env != "" && !flagChanged(cmd, "session") && flagSession == "" {
		flagSession = env
	}
	if env := strings.TrimSpace(os.Getenv("PLAYWRIGHT_MCP_HEADLESS")); env != "" && !flagChanged(cmd, "headless") && !flagChanged(cmd, "headed") {
		if value, ok := parseEnvBool(env); ok {
			flagHeadless = value
		} else {
			*unsupported = append(*unsupported, "PLAYWRIGHT_MCP_HEADLESS")
		}
	}
	if env := strings.TrimSpace(os.Getenv("PLAYWRIGHT_MCP_CDP_ENDPOINT")); env != "" && !flagChanged(cmd, "connect") {
		flagConnect = env
	}
	if env := strings.TrimSpace(os.Getenv("PLAYWRIGHT_MCP_ISOLATED")); env != "" {
		if value, ok := parseEnvBool(env); !ok {
			*unsupported = append(*unsupported, "PLAYWRIGHT_MCP_ISOLATED")
		} else if !value {
			*unsupported = append(*unsupported, "PLAYWRIGHT_MCP_ISOLATED=false")
		}
	}
	if env := strings.TrimSpace(os.Getenv("PLAYWRIGHT_MCP_USER_DATA_DIR")); env != "" && !flagChanged(cmd, "user-profile") {
		flagUserDataDir = env
	}
	if env := strings.TrimSpace(os.Getenv("PLAYWRIGHT_MCP_EXECUTABLE_PATH")); env != "" {
		flagConfigExecutablePath = env
	}
	if env := strings.TrimSpace(os.Getenv("PLAYWRIGHT_MCP_DEVICE")); env != "" {
		if _, ok := engine.DeviceByName(env); ok {
			flagConfigDevice = env
		} else {
			*unsupported = append(*unsupported, "PLAYWRIGHT_MCP_DEVICE="+env)
		}
	}
	if env := strings.TrimSpace(os.Getenv("PLAYWRIGHT_MCP_STORAGE_STATE")); env != "" {
		flagConfigStorageState = env
	}
	if env := strings.TrimSpace(os.Getenv("PLAYWRIGHT_MCP_VIEWPORT_SIZE")); env != "" {
		if width, height, ok := parseViewportSize(env); ok {
			flagConfigViewportW = width
			flagConfigViewportH = height
		} else {
			*unsupported = append(*unsupported, "PLAYWRIGHT_MCP_VIEWPORT_SIZE")
		}
	}
	if env := os.Getenv("PLAYWRIGHT_MCP_USER_AGENT"); env != "" {
		flagConfigUserAgent = env
	}
	if env := strings.TrimSpace(os.Getenv("PLAYWRIGHT_MCP_TIMEOUT_NAVIGATION")); env != "" && !flagChanged(cmd, "timeout") {
		if ms, err := strconv.Atoi(env); err == nil && ms > 0 {
			flagTimeout = int(math.Ceil(float64(ms) / 1000.0))
		} else {
			*unsupported = append(*unsupported, "PLAYWRIGHT_MCP_TIMEOUT_NAVIGATION")
		}
	}
	if env := strings.TrimSpace(os.Getenv("PLAYWRIGHT_MCP_PROXY_SERVER")); env != "" && !flagChanged(cmd, "proxy") {
		flagProxy = env
	}
	if env := strings.TrimSpace(os.Getenv("PLAYWRIGHT_MCP_PROXY_BYPASS")); env != "" && !flagChanged(cmd, "proxy-bypass") {
		flagProxyBypass = env
	}
	if env := strings.TrimSpace(os.Getenv("PLAYWRIGHT_MCP_IGNORE_HTTPS_ERRORS")); env != "" {
		if value, ok := parseEnvBool(env); ok {
			flagConfigIgnoreHTTPSErr = value
		} else {
			*unsupported = append(*unsupported, "PLAYWRIGHT_MCP_IGNORE_HTTPS_ERRORS")
		}
	}
	if env := strings.TrimSpace(os.Getenv("PLAYWRIGHT_MCP_INIT_SCRIPT")); env != "" {
		flagConfigInitScripts = append(flagConfigInitScripts, splitCSV(env)...)
	}
	if env := strings.TrimSpace(os.Getenv("PLAYWRIGHT_MCP_OUTPUT_DIR")); env != "" {
		flagConfigOutputDir = env
	}
	if env := strings.TrimSpace(os.Getenv("PLAYWRIGHT_MCP_OUTPUT_MAX_SIZE")); env != "" && !flagChanged(cmd, "output-max-size") {
		if size, err := strconv.Atoi(env); err == nil && size >= 0 {
			flagOutputMaxSize = size
		} else {
			*unsupported = append(*unsupported, "PLAYWRIGHT_MCP_OUTPUT_MAX_SIZE")
		}
	}
	if env := strings.TrimSpace(os.Getenv("PLAYWRIGHT_MCP_CONSOLE_LEVEL")); env != "" && !flagChanged(cmd, "level") {
		if level, ok := normalizeConsoleConfigLevel(env); ok {
			flagConsoleLevel = level
		} else {
			*unsupported = append(*unsupported, "PLAYWRIGHT_MCP_CONSOLE_LEVEL="+env)
		}
	}
	if env := strings.TrimSpace(os.Getenv("PLAYWRIGHT_MCP_BROWSER")); env != "" && !isSupportedBrowserName(env) {
		*unsupported = append(*unsupported, "PLAYWRIGHT_MCP_BROWSER="+env)
	}
}

func parseEnvBool(raw string) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on":
		return true, true
	case "0", "false", "no", "off":
		return false, true
	default:
		return false, false
	}
}

func parseViewportSize(raw string) (width int, height int, ok bool) {
	parts := strings.Split(strings.ToLower(strings.TrimSpace(raw)), "x")
	if len(parts) != 2 {
		return 0, 0, false
	}
	width, errW := strconv.Atoi(strings.TrimSpace(parts[0]))
	height, errH := strconv.Atoi(strings.TrimSpace(parts[1]))
	return width, height, errW == nil && errH == nil && width > 0 && height > 0
}

func applyConfigValues(cmd *cobra.Command, cfg *playwrightCLIConfig, configDir string, unsupported *[]string) {
	appendUnsupportedTopLevel(cfg, unsupported)
	applyAutoVideoConfig(cfg, unsupported)
	if cfg.OutputDir != "" {
		flagConfigOutputDir = resolveConfigPath(configDir, cfg.OutputDir)
	}
	if cfg.OutputMaxSize > 0 && !flagChanged(cmd, "output-max-size") {
		flagOutputMaxSize = cfg.OutputMaxSize
	} else if cfg.OutputMaxSize < 0 {
		*unsupported = append(*unsupported, "outputMaxSize")
	}
	if cfg.Console != nil && cfg.Console.Level != "" && !flagChanged(cmd, "level") {
		if level, ok := normalizeConsoleConfigLevel(cfg.Console.Level); ok {
			flagConsoleLevel = level
		} else {
			*unsupported = append(*unsupported, "console.level="+cfg.Console.Level)
		}
	}
	if cfg.Timeouts != nil && cfg.Timeouts.Navigation > 0 && !flagChanged(cmd, "timeout") {
		flagTimeout = int(math.Ceil(float64(cfg.Timeouts.Navigation) / 1000.0))
	}
	if cfg.Browser == nil {
		return
	}
	browser := cfg.Browser
	if browser.BrowserName != "" && !isSupportedBrowserName(browser.BrowserName) {
		*unsupported = append(*unsupported, "browser.browserName="+browser.BrowserName)
	}
	if browser.CDPEndpoint != "" && !flagChanged(cmd, "connect") {
		flagConnect = browser.CDPEndpoint
	}
	if browser.Isolated != nil && !*browser.Isolated {
		*unsupported = append(*unsupported, "browser.isolated=false")
	}
	if len(browser.CDPHeaders) > 0 {
		headers, invalid := normalizeCDPHeaders(browser.CDPHeaders)
		if len(headers) > 0 {
			flagConfigCDPHeaders = headers
		}
		for _, key := range invalid {
			*unsupported = append(*unsupported, "browser.cdpHeaders="+key)
		}
	}
	if browser.CDPTimeout > 0 {
		flagConfigCDPTimeoutMS = browser.CDPTimeout
	} else if browser.CDPTimeout < 0 {
		*unsupported = append(*unsupported, "browser.cdpTimeout")
	}
	if browser.RemoteEndpoint != "" {
		*unsupported = append(*unsupported, "browser.remoteEndpoint")
	}
	if browser.UserDataDir != "" && !flagChanged(cmd, "user-profile") {
		flagUserDataDir = browser.UserDataDir
	}
	if browser.InitPage != nil {
		*unsupported = append(*unsupported, "browser.initPage")
	}
	if browser.InitScript != nil {
		if scripts, ok := stringListFromConfigValue(browser.InitScript); ok {
			for _, script := range scripts {
				flagConfigInitScripts = append(flagConfigInitScripts, resolveConfigPath(configDir, script))
			}
		} else {
			*unsupported = append(*unsupported, "browser.initScript")
		}
	}
	if browser.LaunchOptions != nil {
		launch := browser.LaunchOptions
		if launch.Headless != nil && !flagChanged(cmd, "headless") && !flagChanged(cmd, "headed") {
			flagHeadless = *launch.Headless
		}
		if launch.Proxy != nil {
			if launch.Proxy.Server != "" && !flagChanged(cmd, "proxy") {
				flagProxy = proxyURLWithAuth(launch.Proxy.Server, launch.Proxy.Username, launch.Proxy.Password)
			}
			if launch.Proxy.Bypass != "" && !flagChanged(cmd, "proxy-bypass") {
				flagProxyBypass = launch.Proxy.Bypass
			}
		}
		if launch.Channel != "" && !isSupportedBrowserName(launch.Channel) {
			*unsupported = append(*unsupported, "browser.launchOptions.channel="+launch.Channel)
		}
		if launch.ExecutablePath != "" {
			flagConfigExecutablePath = resolveConfigPath(configDir, launch.ExecutablePath)
		}
		if len(launch.Args) > 0 {
			for _, arg := range launch.Args {
				if normalized, ok := normalizeChromiumLaunchArg(arg); ok {
					flagConfigLaunchArgs = append(flagConfigLaunchArgs, normalized)
				} else {
					*unsupported = append(*unsupported, "browser.launchOptions.args="+arg)
				}
			}
		}
	}
	if browser.ContextOptions != nil {
		ctx := browser.ContextOptions
		if ctx.Viewport != nil {
			if ctx.Viewport.Width > 0 && ctx.Viewport.Height > 0 {
				flagConfigViewportW = ctx.Viewport.Width
				flagConfigViewportH = ctx.Viewport.Height
			} else {
				*unsupported = append(*unsupported, "browser.contextOptions.viewport")
			}
		}
		if ctx.Locale != "" {
			flagConfigLocale = ctx.Locale
		}
		if ctx.UserAgent != "" {
			flagConfigUserAgent = ctx.UserAgent
		}
		if ctx.StorageState != "" {
			flagConfigStorageState = resolveConfigPath(configDir, ctx.StorageState)
		}
		if len(ctx.Permissions) > 0 {
			mapped, unknown := resolveConfigPermissions(ctx.Permissions)
			flagConfigPermissions = append(flagConfigPermissions, mapped...)
			for _, permission := range unknown {
				*unsupported = append(*unsupported, "browser.contextOptions.permissions="+permission)
			}
		}
		if ctx.ServiceWorkers != "" {
			if mode, ok := normalizeServiceWorkersMode(ctx.ServiceWorkers); ok {
				flagConfigServiceWorkers = mode
			} else {
				*unsupported = append(*unsupported, "browser.contextOptions.serviceWorkers="+ctx.ServiceWorkers)
			}
		}
	}
}

func applyPermissionsEnv(unsupported *[]string) {
	raw := strings.TrimSpace(os.Getenv("PLAYWRIGHT_MCP_GRANT_PERMISSIONS"))
	if raw == "" {
		return
	}
	permissions := splitCSV(raw)
	mapped, unknown := resolveConfigPermissions(permissions)
	flagConfigPermissions = append(flagConfigPermissions, mapped...)
	for _, permission := range unknown {
		*unsupported = append(*unsupported, "PLAYWRIGHT_MCP_GRANT_PERMISSIONS="+permission)
	}
}

func splitCSV(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func stringListFromConfigValue(value any) ([]string, bool) {
	switch v := value.(type) {
	case string:
		v = strings.TrimSpace(v)
		if v == "" {
			return nil, false
		}
		return []string{v}, true
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			s, ok := item.(string)
			if !ok {
				return nil, false
			}
			s = strings.TrimSpace(s)
			if s != "" {
				out = append(out, s)
			}
		}
		if len(out) == 0 {
			return nil, false
		}
		return out, true
	case []string:
		out := make([]string, 0, len(v))
		for _, item := range v {
			item = strings.TrimSpace(item)
			if item != "" {
				out = append(out, item)
			}
		}
		if len(out) == 0 {
			return nil, false
		}
		return out, true
	default:
		return nil, false
	}
}

func applyServiceWorkersEnv(unsupported *[]string) {
	raw := strings.TrimSpace(os.Getenv("PLAYWRIGHT_MCP_BLOCK_SERVICE_WORKERS"))
	if raw == "" {
		return
	}
	if mode, ok := normalizeServiceWorkersEnv(raw); ok {
		flagConfigServiceWorkers = mode
		return
	}
	*unsupported = append(*unsupported, "PLAYWRIGHT_MCP_BLOCK_SERVICE_WORKERS="+raw)
}

func normalizeServiceWorkersMode(raw string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "allow":
		return "allow", true
	case "block":
		return "block", true
	default:
		return "", false
	}
}

func normalizeServiceWorkersEnv(raw string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on", "block":
		return "block", true
	case "0", "false", "no", "off", "allow":
		return "allow", true
	default:
		return "", false
	}
}

func normalizeCDPHeaders(raw map[string]string) (map[string]string, []string) {
	out := map[string]string{}
	invalid := []string{}
	for key, value := range raw {
		name := strings.TrimSpace(key)
		if name == "" {
			invalid = append(invalid, key)
			continue
		}
		out[name] = value
	}
	if len(out) == 0 {
		out = nil
	}
	return out, invalid
}

func normalizeChromiumLaunchArg(arg string) (string, bool) {
	arg = strings.TrimSpace(arg)
	if !strings.HasPrefix(arg, "--") || arg == "--" {
		return "", false
	}
	nameValue := strings.TrimPrefix(arg, "--")
	if nameValue == "" || strings.HasPrefix(nameValue, "-") {
		return "", false
	}
	return "--" + nameValue, true
}

func normalizeConsoleConfigLevel(raw string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "error":
		return "error", true
	case "warning", "warn":
		return "warning", true
	case "info":
		return "info", true
	case "debug":
		return "debug", true
	default:
		return "", false
	}
}

func proxyURLWithAuth(server string, username string, password string) string {
	if username == "" && password == "" {
		return server
	}
	u, err := url.Parse(server)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return server
	}
	if password != "" {
		u.User = url.UserPassword(username, password)
	} else {
		u.User = url.User(username)
	}
	return u.String()
}

func resolveConfigPermissions(permissions []string) (mapped []string, unknown []string) {
	_, unsupported := pagesetup.MapPlaywrightPermissions(permissions)
	unsupportedSet := map[string]bool{}
	for _, permission := range unsupported {
		unsupportedSet[strings.TrimSpace(permission)] = true
	}
	for _, permission := range permissions {
		permission = strings.TrimSpace(permission)
		if permission == "" {
			continue
		}
		if unsupportedSet[permission] {
			unknown = append(unknown, permission)
			continue
		}
		mapped = append(mapped, permission)
	}
	return mapped, unknown
}

func appendUnsupportedTopLevel(cfg *playwrightCLIConfig, unsupported *[]string) {
	if cfg.OutputMode != "" {
		*unsupported = append(*unsupported, "outputMode")
	}
	if cfg.Network != nil {
		*unsupported = append(*unsupported, "network")
	}
	if cfg.Extension != nil {
		*unsupported = append(*unsupported, "extension")
	}
	if cfg.SaveSession != nil {
		*unsupported = append(*unsupported, "saveSession")
	}
	if cfg.SharedBrowserContext != nil {
		*unsupported = append(*unsupported, "sharedBrowserContext")
	}
	if cfg.Snapshot != nil {
		*unsupported = append(*unsupported, "snapshot")
	}
	if cfg.ImageResponses != "" {
		*unsupported = append(*unsupported, "imageResponses")
	}
	if cfg.TestIDAttribute != "" {
		*unsupported = append(*unsupported, "testIdAttribute")
	}
	if cfg.AllowUnrestrictedFileAccess != nil {
		*unsupported = append(*unsupported, "allowUnrestrictedFileAccess")
	}
	if cfg.Codegen != "" {
		*unsupported = append(*unsupported, "codegen")
	}
}

func applyAutoVideoConfig(cfg *playwrightCLIConfig, unsupported *[]string) {
	if env := os.Getenv("PLAYWRIGHT_MCP_SAVE_VIDEO"); env != "" {
		if size, ok := normaliseVideoSize(env); ok {
			flagAutoVideoSize = size
			flagAutoVideoSource = "PLAYWRIGHT_MCP_SAVE_VIDEO"
		} else {
			*unsupported = append(*unsupported, "PLAYWRIGHT_MCP_SAVE_VIDEO")
		}
	}
	if cfg.SaveVideo == nil {
		return
	}
	if size, ok := parseSaveVideoSize(cfg.SaveVideo); ok {
		if flagAutoVideoSize == "" {
			flagAutoVideoSize = size
			flagAutoVideoSource = "config.saveVideo"
		}
		return
	}
	*unsupported = append(*unsupported, "saveVideo")
}

func parseSaveVideoSize(value any) (string, bool) {
	switch v := value.(type) {
	case bool:
		return "", false
	case string:
		return normaliseVideoSize(v)
	case map[string]any:
		width, wok := numberLike(v["width"])
		height, hok := numberLike(v["height"])
		if wok && hok && width > 0 && height > 0 {
			return fmt.Sprintf("%dx%d", width, height), true
		}
	}
	return "", false
}

func normaliseVideoSize(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", false
	}
	parts := strings.Split(value, "x")
	if len(parts) != 2 {
		return "", false
	}
	width, wok := positiveInt(parts[0])
	height, hok := positiveInt(parts[1])
	if !wok || !hok {
		return "", false
	}
	return fmt.Sprintf("%dx%d", width, height), true
}

func positiveInt(value string) (int, bool) {
	var n int
	if _, err := fmt.Sscanf(strings.TrimSpace(value), "%d", &n); err != nil {
		return 0, false
	}
	return n, n > 0
}

func numberLike(value any) (int, bool) {
	switch v := value.(type) {
	case float64:
		return int(v), v > 0 && math.Trunc(v) == v
	case int:
		return v, v > 0
	case json.Number:
		i, err := v.Int64()
		return int(i), err == nil && i > 0
	default:
		return 0, false
	}
}

func flagChanged(cmd *cobra.Command, name string) bool {
	var rootFlags *pflag.FlagSet
	if root := cmd.Root(); root != nil {
		rootFlags = root.PersistentFlags()
	}
	for _, set := range []*pflag.FlagSet{cmd.Flags(), cmd.InheritedFlags(), rootFlags} {
		if set == nil {
			continue
		}
		if flag := set.Lookup(name); flag != nil && flag.Changed {
			return true
		}
	}
	return false
}

func resolveConfigPath(baseDir string, path string) string {
	path = strings.TrimSpace(path)
	if path == "" || filepath.IsAbs(path) || baseDir == "" {
		return path
	}
	return filepath.Join(baseDir, path)
}

func isSupportedBrowserName(name string) bool {
	switch strings.ToLower(name) {
	case "", "chrome", "chromium":
		return true
	default:
		return false
	}
}

var configPrintCmd = &cobra.Command{
	Use:   "config-print",
	Short: "Print resolved Playwright CLI-compatible config",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		type browserConfig struct {
			BrowserName string   `json:"browser_name"`
			Supported   bool     `json:"supported"`
			Headless    bool     `json:"headless"`
			Connect     string   `json:"connect,omitempty"`
			UserProfile string   `json:"user_profile,omitempty"`
			UserDataDir string   `json:"user_data_dir,omitempty"`
			Proxy       string   `json:"proxy,omitempty"`
			ProxyBypass string   `json:"proxy_bypass,omitempty"`
			Executable  string   `json:"executable_path,omitempty"`
			LaunchArgs  []string `json:"launch_args,omitempty"`
		}
		type result struct {
			ConfigPath        string         `json:"config_path,omitempty"`
			ConfigLoaded      bool           `json:"config_loaded"`
			Browser           browserConfig  `json:"browser"`
			AutoVideo         string         `json:"auto_video_size,omitempty"`
			AutoVideoSource   string         `json:"auto_video_source,omitempty"`
			Context           map[string]any `json:"context_options,omitempty"`
			TimeoutSeconds    int            `json:"timeout_seconds"`
			Session           string         `json:"session,omitempty"`
			RenderProfile     string         `json:"render_profile"`
			OutputFormat      string         `json:"output_format"`
			UnsupportedFields []string       `json:"unsupported_fields,omitempty"`
			OutputDir         string         `json:"output_dir,omitempty"`
			OutputMaxSize     int            `json:"output_max_size,omitempty"`
			ConsoleLevel      string         `json:"console_level,omitempty"`
		}
		out := result{
			ConfigLoaded: loadedPlaywrightConfig != nil,
			Browser: browserConfig{
				BrowserName: "chromium",
				Supported:   true,
				Headless:    flagHeadless,
				Connect:     flagConnect,
				UserProfile: flagUserProfile,
				UserDataDir: flagUserDataDir,
				Proxy:       flagProxy,
				ProxyBypass: flagProxyBypass,
				Executable:  flagConfigExecutablePath,
				LaunchArgs:  flagConfigLaunchArgs,
			},
			AutoVideo:       flagAutoVideoSize,
			AutoVideoSource: flagAutoVideoSource,
			TimeoutSeconds:  flagTimeout,
			Session:         flagSession,
			RenderProfile:   flagProfile,
			OutputFormat:    flagFormat,
			OutputDir:       flagConfigOutputDir,
			OutputMaxSize:   flagOutputMaxSize,
			ConsoleLevel:    flagConsoleLevel,
		}
		if loadedPlaywrightConfig != nil {
			out.ConfigPath = loadedPlaywrightConfig.Path
			if loadedPlaywrightConfig.Config.Browser != nil {
				if name := loadedPlaywrightConfig.Config.Browser.BrowserName; name != "" {
					out.Browser.BrowserName = name
					out.Browser.Supported = isSupportedBrowserName(name)
				} else if launch := loadedPlaywrightConfig.Config.Browser.LaunchOptions; launch != nil && launch.Channel != "" {
					out.Browser.BrowserName = launch.Channel
					out.Browser.Supported = isSupportedBrowserName(launch.Channel)
				}
			}
		}
		out.UnsupportedFields = playwrightUnsupportedFields
		context := map[string]any{}
		if flagConfigDevice != "" {
			context["device"] = flagConfigDevice
		}
		if flagConfigViewportW > 0 && flagConfigViewportH > 0 {
			context["viewport"] = map[string]int{"width": flagConfigViewportW, "height": flagConfigViewportH}
		}
		if flagConfigUserAgent != "" {
			context["user_agent"] = flagConfigUserAgent
		}
		if flagConfigStorageState != "" {
			context["storage_state"] = flagConfigStorageState
		}
		if flagConfigLocale != "" {
			context["locale"] = flagConfigLocale
		}
		if len(flagConfigPermissions) > 0 {
			context["permissions"] = flagConfigPermissions
		}
		if flagConfigServiceWorkers != "" {
			context["service_workers"] = flagConfigServiceWorkers
		}
		if len(flagConfigInitScripts) > 0 {
			context["init_scripts"] = flagConfigInitScripts
		}
		if len(flagConfigCDPHeaders) > 0 {
			context["cdp_headers"] = flagConfigCDPHeaders
		}
		if flagConfigCDPTimeoutMS > 0 {
			context["cdp_timeout_ms"] = flagConfigCDPTimeoutMS
		}
		if flagConfigIgnoreHTTPSErr {
			context["ignore_https_errors"] = true
		}
		if len(context) > 0 {
			out.Context = context
		}
		text := fmt.Sprintf("config loaded: %t\nbrowser: %s supported=%t headless=%t\nconnect: %s\nprofile: %s\nauto video: %s\nunsupported fields: %s",
			out.ConfigLoaded, out.Browser.BrowserName, out.Browser.Supported, out.Browser.Headless, valueOrDash(out.Browser.Connect),
			valueOrDash(firstNonEmpty(out.Browser.UserDataDir, out.Browser.UserProfile)),
			valueOrDash(out.AutoVideo),
			valueOrDash(strings.Join(out.UnsupportedFields, ", ")))
		output(out, text)
	},
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func valueOrDash(value string) string {
	if value == "" {
		return "-"
	}
	return value
}

func init() {
	rootCmd.AddCommand(configPrintCmd)
	commandGroups["config-print"] = "util"
}
