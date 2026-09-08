package cli

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/dev-toolings/ghostchrome/internal/compat/playwright"
	"github.com/dev-toolings/ghostchrome/internal/core/engine"
	"github.com/spf13/cobra"
)

type loadedConfigState struct {
	Path        string
	Config      playwright.Config
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
		applyAutoVideoConfig(&playwright.Config{}, &unsupported)
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
	var cfg playwright.Config
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
	if env := strings.TrimSpace(os.Getenv("PLAYWRIGHT_CLI_SESSION")); env != "" && !playwright.FlagChanged(cmd, "session") && flagSession == "" {
		flagSession = env
	}
	if env := strings.TrimSpace(os.Getenv("PLAYWRIGHT_MCP_HEADLESS")); env != "" && !playwright.FlagChanged(cmd, "headless") && !playwright.FlagChanged(cmd, "headed") {
		if value, ok := playwright.ParseEnvBool(env); ok {
			flagHeadless = value
		} else {
			*unsupported = append(*unsupported, "PLAYWRIGHT_MCP_HEADLESS")
		}
	}
	if env := strings.TrimSpace(os.Getenv("PLAYWRIGHT_MCP_CDP_ENDPOINT")); env != "" && !playwright.FlagChanged(cmd, "connect") {
		flagConnect = env
	}
	if env := strings.TrimSpace(os.Getenv("PLAYWRIGHT_MCP_ISOLATED")); env != "" {
		if value, ok := playwright.ParseEnvBool(env); !ok {
			*unsupported = append(*unsupported, "PLAYWRIGHT_MCP_ISOLATED")
		} else if !value {
			*unsupported = append(*unsupported, "PLAYWRIGHT_MCP_ISOLATED=false")
		}
	}
	if env := strings.TrimSpace(os.Getenv("PLAYWRIGHT_MCP_USER_DATA_DIR")); env != "" && !playwright.FlagChanged(cmd, "user-profile") {
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
		if width, height, ok := playwright.ParseViewportSize(env); ok {
			flagConfigViewportW = width
			flagConfigViewportH = height
		} else {
			*unsupported = append(*unsupported, "PLAYWRIGHT_MCP_VIEWPORT_SIZE")
		}
	}
	if env := os.Getenv("PLAYWRIGHT_MCP_USER_AGENT"); env != "" {
		flagConfigUserAgent = env
	}
	if env := strings.TrimSpace(os.Getenv("PLAYWRIGHT_MCP_TIMEOUT_NAVIGATION")); env != "" && !playwright.FlagChanged(cmd, "timeout") {
		if ms, err := strconv.Atoi(env); err == nil && ms > 0 {
			flagTimeout = int(math.Ceil(float64(ms) / 1000.0))
		} else {
			*unsupported = append(*unsupported, "PLAYWRIGHT_MCP_TIMEOUT_NAVIGATION")
		}
	}
	if env := strings.TrimSpace(os.Getenv("PLAYWRIGHT_MCP_PROXY_SERVER")); env != "" && !playwright.FlagChanged(cmd, "proxy") {
		flagProxy = env
	}
	if env := strings.TrimSpace(os.Getenv("PLAYWRIGHT_MCP_PROXY_BYPASS")); env != "" && !playwright.FlagChanged(cmd, "proxy-bypass") {
		flagProxyBypass = env
	}
	if env := strings.TrimSpace(os.Getenv("PLAYWRIGHT_MCP_IGNORE_HTTPS_ERRORS")); env != "" {
		if value, ok := playwright.ParseEnvBool(env); ok {
			flagConfigIgnoreHTTPSErr = value
		} else {
			*unsupported = append(*unsupported, "PLAYWRIGHT_MCP_IGNORE_HTTPS_ERRORS")
		}
	}
	if env := strings.TrimSpace(os.Getenv("PLAYWRIGHT_MCP_INIT_SCRIPT")); env != "" {
		flagConfigInitScripts = append(flagConfigInitScripts, playwright.SplitCSV(env)...)
	}
	if env := strings.TrimSpace(os.Getenv("PLAYWRIGHT_MCP_OUTPUT_DIR")); env != "" {
		flagConfigOutputDir = env
	}
	if env := strings.TrimSpace(os.Getenv("PLAYWRIGHT_MCP_OUTPUT_MAX_SIZE")); env != "" && !playwright.FlagChanged(cmd, "output-max-size") {
		if size, err := strconv.Atoi(env); err == nil && size >= 0 {
			flagOutputMaxSize = size
		} else {
			*unsupported = append(*unsupported, "PLAYWRIGHT_MCP_OUTPUT_MAX_SIZE")
		}
	}
	if env := strings.TrimSpace(os.Getenv("PLAYWRIGHT_MCP_CONSOLE_LEVEL")); env != "" && !playwright.FlagChanged(cmd, "level") {
		if level, ok := playwright.NormalizeConsoleConfigLevel(env); ok {
			flagConsoleLevel = level
		} else {
			*unsupported = append(*unsupported, "PLAYWRIGHT_MCP_CONSOLE_LEVEL="+env)
		}
	}
	if env := strings.TrimSpace(os.Getenv("PLAYWRIGHT_MCP_BROWSER")); env != "" && !playwright.IsSupportedBrowserName(env) {
		*unsupported = append(*unsupported, "PLAYWRIGHT_MCP_BROWSER="+env)
	}
}

func applyConfigValues(cmd *cobra.Command, cfg *playwright.Config, configDir string, unsupported *[]string) {
	playwright.AppendUnsupportedTopLevel(cfg, unsupported)
	applyAutoVideoConfig(cfg, unsupported)
	if cfg.OutputDir != "" {
		flagConfigOutputDir = playwright.ResolveConfigPath(configDir, cfg.OutputDir)
	}
	if cfg.OutputMaxSize > 0 && !playwright.FlagChanged(cmd, "output-max-size") {
		flagOutputMaxSize = cfg.OutputMaxSize
	} else if cfg.OutputMaxSize < 0 {
		*unsupported = append(*unsupported, "outputMaxSize")
	}
	if cfg.Console != nil && cfg.Console.Level != "" && !playwright.FlagChanged(cmd, "level") {
		if level, ok := playwright.NormalizeConsoleConfigLevel(cfg.Console.Level); ok {
			flagConsoleLevel = level
		} else {
			*unsupported = append(*unsupported, "console.level="+cfg.Console.Level)
		}
	}
	if cfg.Timeouts != nil && cfg.Timeouts.Navigation > 0 && !playwright.FlagChanged(cmd, "timeout") {
		flagTimeout = int(math.Ceil(float64(cfg.Timeouts.Navigation) / 1000.0))
	}
	if cfg.Browser == nil {
		return
	}
	browser := cfg.Browser
	if browser.BrowserName != "" && !playwright.IsSupportedBrowserName(browser.BrowserName) {
		*unsupported = append(*unsupported, "browser.browserName="+browser.BrowserName)
	}
	if browser.CDPEndpoint != "" && !playwright.FlagChanged(cmd, "connect") {
		flagConnect = browser.CDPEndpoint
	}
	if browser.Isolated != nil && !*browser.Isolated {
		*unsupported = append(*unsupported, "browser.isolated=false")
	}
	if len(browser.CDPHeaders) > 0 {
		headers, invalid := playwright.NormalizeCDPHeaders(browser.CDPHeaders)
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
	if browser.UserDataDir != "" && !playwright.FlagChanged(cmd, "user-profile") {
		flagUserDataDir = browser.UserDataDir
	}
	if browser.InitPage != nil {
		*unsupported = append(*unsupported, "browser.initPage")
	}
	if browser.InitScript != nil {
		if scripts, ok := playwright.StringListFromConfigValue(browser.InitScript); ok {
			for _, script := range scripts {
				flagConfigInitScripts = append(flagConfigInitScripts, playwright.ResolveConfigPath(configDir, script))
			}
		} else {
			*unsupported = append(*unsupported, "browser.initScript")
		}
	}
	if browser.LaunchOptions != nil {
		launch := browser.LaunchOptions
		if launch.Headless != nil && !playwright.FlagChanged(cmd, "headless") && !playwright.FlagChanged(cmd, "headed") {
			flagHeadless = *launch.Headless
		}
		if launch.Proxy != nil {
			if launch.Proxy.Server != "" && !playwright.FlagChanged(cmd, "proxy") {
				flagProxy = playwright.ProxyURLWithAuth(launch.Proxy.Server, launch.Proxy.Username, launch.Proxy.Password)
			}
			if launch.Proxy.Bypass != "" && !playwright.FlagChanged(cmd, "proxy-bypass") {
				flagProxyBypass = launch.Proxy.Bypass
			}
		}
		if launch.Channel != "" && !playwright.IsSupportedBrowserName(launch.Channel) {
			*unsupported = append(*unsupported, "browser.launchOptions.channel="+launch.Channel)
		}
		if launch.ExecutablePath != "" {
			flagConfigExecutablePath = playwright.ResolveConfigPath(configDir, launch.ExecutablePath)
		}
		if len(launch.Args) > 0 {
			for _, arg := range launch.Args {
				if normalized, ok := playwright.NormalizeChromiumLaunchArg(arg); ok {
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
			flagConfigStorageState = playwright.ResolveConfigPath(configDir, ctx.StorageState)
		}
		if len(ctx.Permissions) > 0 {
			mapped, unknown := playwright.ResolveConfigPermissions(ctx.Permissions)
			flagConfigPermissions = append(flagConfigPermissions, mapped...)
			for _, permission := range unknown {
				*unsupported = append(*unsupported, "browser.contextOptions.permissions="+permission)
			}
		}
		if ctx.ServiceWorkers != "" {
			if mode, ok := playwright.NormalizeServiceWorkersMode(ctx.ServiceWorkers); ok {
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
	permissions := playwright.SplitCSV(raw)
	mapped, unknown := playwright.ResolveConfigPermissions(permissions)
	flagConfigPermissions = append(flagConfigPermissions, mapped...)
	for _, permission := range unknown {
		*unsupported = append(*unsupported, "PLAYWRIGHT_MCP_GRANT_PERMISSIONS="+permission)
	}
}

func applyServiceWorkersEnv(unsupported *[]string) {
	raw := strings.TrimSpace(os.Getenv("PLAYWRIGHT_MCP_BLOCK_SERVICE_WORKERS"))
	if raw == "" {
		return
	}
	if mode, ok := playwright.NormalizeServiceWorkersEnv(raw); ok {
		flagConfigServiceWorkers = mode
		return
	}
	*unsupported = append(*unsupported, "PLAYWRIGHT_MCP_BLOCK_SERVICE_WORKERS="+raw)
}

func applyAutoVideoConfig(cfg *playwright.Config, unsupported *[]string) {
	if env := os.Getenv("PLAYWRIGHT_MCP_SAVE_VIDEO"); env != "" {
		if size, ok := playwright.NormaliseVideoSize(env); ok {
			flagAutoVideoSize = size
			flagAutoVideoSource = "PLAYWRIGHT_MCP_SAVE_VIDEO"
		} else {
			*unsupported = append(*unsupported, "PLAYWRIGHT_MCP_SAVE_VIDEO")
		}
	}
	if cfg.SaveVideo == nil {
		return
	}
	if size, ok := playwright.ParseSaveVideoSize(cfg.SaveVideo); ok {
		if flagAutoVideoSize == "" {
			flagAutoVideoSize = size
			flagAutoVideoSource = "config.saveVideo"
		}
		return
	}
	*unsupported = append(*unsupported, "saveVideo")
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
					out.Browser.Supported = playwright.IsSupportedBrowserName(name)
				} else if launch := loadedPlaywrightConfig.Config.Browser.LaunchOptions; launch != nil && launch.Channel != "" {
					out.Browser.BrowserName = launch.Channel
					out.Browser.Supported = playwright.IsSupportedBrowserName(launch.Channel)
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
