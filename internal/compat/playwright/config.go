// Package playwright holds the Playwright-CLI compatibility contract that is
// independent of ghostchrome's own command surface: the shape of a
// Playwright CLI config file and the rules that turn its raw values into
// ghostchrome settings.
//
// Applying those values to the CLI flags is deliberately NOT here. That step
// writes internal/surface/cli's flag globals and stays with the cobra tree.
package playwright

import (
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/dev-toolings/ghostchrome/internal/core/pagesetup"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

type Config struct {
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

func ParseEnvBool(raw string) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on":
		return true, true
	case "0", "false", "no", "off":
		return false, true
	default:
		return false, false
	}
}

func ParseViewportSize(raw string) (width int, height int, ok bool) {
	parts := strings.Split(strings.ToLower(strings.TrimSpace(raw)), "x")
	if len(parts) != 2 {
		return 0, 0, false
	}
	width, errW := strconv.Atoi(strings.TrimSpace(parts[0]))
	height, errH := strconv.Atoi(strings.TrimSpace(parts[1]))
	return width, height, errW == nil && errH == nil && width > 0 && height > 0
}

func SplitCSV(raw string) []string {
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

func StringListFromConfigValue(value any) ([]string, bool) {
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

func NormalizeServiceWorkersMode(raw string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "allow":
		return "allow", true
	case "block":
		return "block", true
	default:
		return "", false
	}
}

func NormalizeServiceWorkersEnv(raw string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on", "block":
		return "block", true
	case "0", "false", "no", "off", "allow":
		return "allow", true
	default:
		return "", false
	}
}

func NormalizeCDPHeaders(raw map[string]string) (map[string]string, []string) {
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

func NormalizeChromiumLaunchArg(arg string) (string, bool) {
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

func NormalizeConsoleConfigLevel(raw string) (string, bool) {
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

func ProxyURLWithAuth(server string, username string, password string) string {
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

func ResolveConfigPermissions(permissions []string) (mapped []string, unknown []string) {
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

func AppendUnsupportedTopLevel(cfg *Config, unsupported *[]string) {
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

func ParseSaveVideoSize(value any) (string, bool) {
	switch v := value.(type) {
	case bool:
		return "", false
	case string:
		return NormaliseVideoSize(v)
	case map[string]any:
		width, wok := NumberLike(v["width"])
		height, hok := NumberLike(v["height"])
		if wok && hok && width > 0 && height > 0 {
			return fmt.Sprintf("%dx%d", width, height), true
		}
	}
	return "", false
}

func NormaliseVideoSize(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", false
	}
	parts := strings.Split(value, "x")
	if len(parts) != 2 {
		return "", false
	}
	width, wok := PositiveInt(parts[0])
	height, hok := PositiveInt(parts[1])
	if !wok || !hok {
		return "", false
	}
	return fmt.Sprintf("%dx%d", width, height), true
}

func PositiveInt(value string) (int, bool) {
	var n int
	if _, err := fmt.Sscanf(strings.TrimSpace(value), "%d", &n); err != nil {
		return 0, false
	}
	return n, n > 0
}

func NumberLike(value any) (int, bool) {
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

func FlagChanged(cmd *cobra.Command, name string) bool {
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

func ResolveConfigPath(baseDir string, path string) string {
	path = strings.TrimSpace(path)
	if path == "" || filepath.IsAbs(path) || baseDir == "" {
		return path
	}
	return filepath.Join(baseDir, path)
}

func IsSupportedBrowserName(name string) bool {
	switch strings.ToLower(name) {
	case "", "chrome", "chromium":
		return true
	default:
		return false
	}
}
