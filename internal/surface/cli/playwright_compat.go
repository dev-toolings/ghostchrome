package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dev-toolings/ghostchrome/internal/compat/playwright"
	"github.com/dev-toolings/ghostchrome/internal/core/engine"
	"github.com/go-rod/rod/lib/proto"
	"github.com/spf13/cobra"
)

var (
	flagOpenBrowser    string
	flagOpenPersistent bool
	flagOpenProfileDir string
	flagOpenMobile     bool
	flagOpenDevice     string
)

var openCmd = &cobra.Command{
	Use:   "open [url]",
	Short: "Open a browser, optionally navigate to a URL",
	Long: `Open a browser and return the current page state.
If a URL is provided, navigates first. This is the Playwright CLI-compatible
entrypoint for ghostchrome's existing browser/session model.`,
	Args: cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		restore, err := applyOpenCompatFlags(cmd)
		if err != nil {
			exitErr("open", err)
		}
		defer restore()

		targetURL := ""
		if len(args) > 0 {
			targetURL = args[0]
		}

		b, page := openPage()
		defer b.Close()
		if flagConfigDevice != "" {
			if device, ok := engine.DeviceByName(flagConfigDevice); ok {
				if err := b.SetEmulationState(engine.EmulationFromDevice(device)); err != nil {
					exitErr("open --device", err)
				}
			}
		}

		info := navigateIfRequested(page, targetURL, "load")
		if info == nil {
			pageInfo, err := page.Info()
			if err != nil {
				exitErr("open", err)
			}
			info = &engine.PageInfo{
				URL:   pageInfo.URL,
				Title: pageInfo.Title,
			}
		}

		result := snapshotPage(b, page, engine.LevelSkeleton)
		text := formatPlaywrightPageStateOutput(info, result)
		type openResult struct {
			Page     *engine.PageInfo         `json:"page"`
			Snapshot *engine.ExtractionResult `json:"snapshot"`
		}
		output(&openResult{Page: info, Snapshot: result}, text)
	},
}

func applyOpenCompatFlags(cmd *cobra.Command) (func(), error) {
	oldUserProfile := flagUserProfile
	oldUserDataDir := flagUserDataDir
	oldConfigDevice := flagConfigDevice
	restore := func() {
		flagUserProfile = oldUserProfile
		flagUserDataDir = oldUserDataDir
		flagConfigDevice = oldConfigDevice
	}

	browser := flagOpenBrowser
	if !playwright.FlagChanged(cmd, "browser") && loadedPlaywrightConfig != nil && loadedPlaywrightConfig.Config.Browser != nil {
		if configured := loadedPlaywrightConfig.Config.Browser.BrowserName; configured != "" {
			browser = configured
		} else if launch := loadedPlaywrightConfig.Config.Browser.LaunchOptions; launch != nil && launch.Channel != "" {
			browser = launch.Channel
		}
	}

	switch strings.ToLower(browser) {
	case "", "chrome", "chromium":
	case "firefox", "webkit", "msedge":
		return restore, fmt.Errorf("--browser=%s is not supported by ghostchrome's CDP/Rod launcher yet", browser)
	default:
		return restore, fmt.Errorf("unknown --browser %q: use chrome, chromium, firefox, webkit, or msedge", browser)
	}

	if flagOpenProfileDir != "" {
		dir, err := filepath.Abs(flagOpenProfileDir)
		if err != nil {
			return restore, err
		}
		flagUserDataDir = dir
		flagUserProfile = ""
	} else if flagOpenPersistent && flagUserProfile == "" && flagUserDataDir == "" {
		flagUserProfile = "default"
	}
	if flagOpenMobile && flagOpenDevice != "" {
		restore()
		return restore, fmt.Errorf("use either --mobile or --device, not both")
	}
	if flagOpenMobile {
		flagConfigDevice = "pixel-7"
	}
	if flagOpenDevice != "" {
		deviceName := normalizeOpenDeviceName(flagOpenDevice)
		if _, ok := engine.DeviceByName(deviceName); !ok {
			restore()
			return restore, fmt.Errorf("unknown --device %q (use `ghostchrome emulate --list`)", flagOpenDevice)
		}
		flagConfigDevice = deviceName
	}

	return restore, nil
}

func normalizeOpenDeviceName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	name = strings.NewReplacer(" ", "-", "_", "-").Replace(name)
	return name
}

var stateSaveCmd = &cobra.Command{
	Use:   "state-save [filename]",
	Short: "Save browser state (Playwright CLI-compatible alias)",
	Args:  cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		if len(args) > 0 {
			flagStorageOutput = args[0]
		}
		storageSaveCmd.Run(cmd, nil)
	},
}

var stateLoadCmd = &cobra.Command{
	Use:   "state-load <filename>",
	Short: "Load browser state (Playwright CLI-compatible alias)",
	Args:  cobra.ExactArgs(1),
	Run:   storageLoadCmd.Run,
}

var listCompatCmd = &cobra.Command{
	Use:   "list",
	Short: "List sessions (Playwright CLI-compatible alias)",
	Args:  cobra.NoArgs,
	Run:   sessionsListCmd.Run,
}

var closeCompatCmd = &cobra.Command{
	Use:   "close [name]",
	Short: "Close the current page or a named session",
	Args:  cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		if len(args) == 0 {
			if !hasReusableBrowserContext() {
				exitErr("close", fmt.Errorf("no reusable browser context found; pass --connect, -s/--session, PLAYWRIGHT_CLI_SESSION, or a session name"))
			}
			closeActiveTab("close")
			return
		}
		name := args[0]
		sessionsStopCmd.Run(cmd, []string{name})
	},
}

var closeAllCompatCmd = &cobra.Command{
	Use:   "close-all",
	Short: "Close all registered sessions",
	Args:  cobra.NoArgs,
	Run:   sessionsKillAllCmd.Run,
}

var killAllCompatCmd = &cobra.Command{
	Use:   "kill-all",
	Short: "Force close all registered sessions",
	Args:  cobra.NoArgs,
	Run:   sessionsKillAllCmd.Run,
}

var deleteDataCompatCmd = &cobra.Command{
	Use:   "delete-data [name]",
	Short: "Delete stored session/profile data",
	Args:  cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		name := resolveCompatSessionName(args)
		if err := engine.StopSession(name); err != nil && !errors.Is(err, engine.ErrSessionNotFound) {
			exitErr("delete-data", err)
		} else if err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "warning: session %q not running\n", name)
		}
		if err := engine.RemoveProfile(name); err != nil {
			exitErr("delete-data", err)
		}
		output(map[string]string{"deleted": name}, fmt.Sprintf("deleted data for %q", name))
	},
}

var networkStateSetCmd = &cobra.Command{
	Use:   "network-state-set <online|offline>",
	Short: "Set browser network state",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		b, page := openPage()
		defer b.Close()

		var req proto.NetworkEmulateNetworkConditions
		switch args[0] {
		case "online":
			req = proto.NetworkEmulateNetworkConditions{
				Offline:            false,
				Latency:            0,
				DownloadThroughput: -1,
				UploadThroughput:   -1,
			}
		case "offline":
			req = proto.NetworkEmulateNetworkConditions{
				Offline:            true,
				Latency:            0,
				DownloadThroughput: 0,
				UploadThroughput:   0,
			}
		default:
			exitErr("network-state-set", fmt.Errorf("expected online or offline, got %q", args[0]))
		}
		if err := req.Call(page); err != nil {
			exitErr("network-state-set", err)
		}
		output(map[string]string{"network_state": args[0]}, "network state: "+args[0])
	},
}

var dialogAcceptCompatCmd = &cobra.Command{
	Use:   "dialog-accept [prompt]",
	Short: "Accept the next dialog (Playwright CLI-compatible alias)",
	Args:  cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		prompt := ""
		if len(args) > 0 {
			prompt = args[0]
		}
		runDialogHandler(true, prompt)
	},
}

var dialogDismissCompatCmd = &cobra.Command{
	Use:   "dialog-dismiss",
	Short: "Dismiss the next dialog (Playwright CLI-compatible alias)",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		runDialogHandler(false, "")
	},
}

var tabListCmd = &cobra.Command{
	Use:   "tab-list",
	Short: "List tabs (Playwright CLI-compatible alias)",
	Args:  cobra.NoArgs,
	Run:   tabsCmd.Run,
}

var tabNewCmd = &cobra.Command{
	Use:   "tab-new [url]",
	Short: "Open a new tab (Playwright CLI-compatible alias)",
	Args:  cobra.MaximumNArgs(1),
	Run:   tabsNewCmd.Run,
}

var tabSelectCmd = &cobra.Command{
	Use:   "tab-select <index>",
	Short: "Switch tabs (Playwright CLI-compatible alias)",
	Args:  cobra.ExactArgs(1),
	Run:   tabsSwitchCmd.Run,
}

var tabCloseCmd = &cobra.Command{
	Use:   "tab-close [index]",
	Short: "Close a tab (Playwright CLI-compatible alias)",
	Args:  cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		if len(args) > 0 {
			tabsCloseCmd.Run(cmd, args)
			return
		}

		closeActiveTab("tab-close")
	},
}

var mouseMoveCompatCmd = &cobra.Command{
	Use:   "mousemove <x> <y>",
	Short: "Move mouse to coordinates (Playwright CLI-compatible alias)",
	Args:  cobra.ExactArgs(2),
	Run:   mouseMoveCmd.Run,
}

var mouseDownCompatCmd = &cobra.Command{
	Use:   "mousedown [button]",
	Short: "Press mouse button (Playwright CLI-compatible alias)",
	Args:  cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		flagMouseButton = "left"
		if len(args) > 0 {
			flagMouseButton = args[0]
		}
		mouseDownCmd.Run(cmd, nil)
	},
}

var mouseUpCompatCmd = &cobra.Command{
	Use:   "mouseup [button]",
	Short: "Release mouse button (Playwright CLI-compatible alias)",
	Args:  cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		flagMouseButton = "left"
		if len(args) > 0 {
			flagMouseButton = args[0]
		}
		mouseUpCmd.Run(cmd, nil)
	},
}

var mouseWheelCompatCmd = &cobra.Command{
	Use:   "mousewheel <dx> <dy>",
	Short: "Scroll with mouse wheel (Playwright CLI-compatible alias)",
	Args:  cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		flagWheelDeltaX, flagWheelDeltaY = parseCoords(args[0], args[1])
		b, page := openPage()
		defer b.Close()

		if err := page.Mouse.Scroll(flagWheelDeltaX, flagWheelDeltaY, 0); err != nil {
			exitErr("mousewheel", err)
		}
		output(map[string]float64{"delta_x": flagWheelDeltaX, "delta_y": flagWheelDeltaY},
			fmt.Sprintf("[mouse] wheel dx=%.0f dy=%.0f", flagWheelDeltaX, flagWheelDeltaY))
	},
}

var keyDownCompatCmd = &cobra.Command{
	Use:   "keydown <key>",
	Short: "Press and hold a keyboard key (Playwright CLI-compatible alias)",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		b, page := openPage()
		defer b.Close()
		if err := (proto.InputDispatchKeyEvent{Type: proto.InputDispatchKeyEventTypeKeyDown, Key: args[0]}).Call(page); err != nil {
			exitErr("keydown", err)
		}
		output(map[string]string{"action": "keydown", "key": args[0]}, fmt.Sprintf("[keyboard] down %s", args[0]))
	},
}

var keyUpCompatCmd = &cobra.Command{
	Use:   "keyup <key>",
	Short: "Release a keyboard key (Playwright CLI-compatible alias)",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		b, page := openPage()
		defer b.Close()
		if err := (proto.InputDispatchKeyEvent{Type: proto.InputDispatchKeyEventTypeKeyUp, Key: args[0]}).Call(page); err != nil {
			exitErr("keyup", err)
		}
		output(map[string]string{"action": "keyup", "key": args[0]}, fmt.Sprintf("[keyboard] up %s", args[0]))
	},
}

func init() {
	openCmd.Flags().StringVar(&flagOpenBrowser, "browser", "chrome", "Browser to launch; ghostchrome currently supports chrome/chromium only")
	openCmd.Flags().BoolVar(&flagOpenPersistent, "persistent", false, "Use a persistent browser profile (defaults to ghostchrome profile \"default\")")
	openCmd.Flags().StringVar(&flagOpenProfileDir, "profile", "", "Custom browser profile directory (Playwright CLI-compatible)")
	openCmd.Flags().BoolVar(&flagOpenMobile, "mobile", false, "Emulate a generic mobile device (Pixel 7)")
	openCmd.Flags().StringVar(&flagOpenDevice, "device", "", "Emulate a ghostchrome device preset, e.g. iphone-14")
	stateSaveCmd.Flags().StringVar(&flagStorageOutput, "output", "", "Output file path")
	stateSaveCmd.Flags().BoolVar(&flagStorageEncrypt, "encrypt", false, "Encrypt the output with AES-256-GCM (requires GHOSTCHROME_VAULT_KEY env var)")
	closeCompatCmd.Flags().BoolVar(&flagSessionsPurge, "purge", false, "Also delete the session's on-disk profile")
	closeAllCompatCmd.Flags().BoolVar(&flagSessionsPurge, "purge", false, "Also delete each session's on-disk profile")
	killAllCompatCmd.Flags().BoolVar(&flagSessionsPurge, "purge", false, "Also delete each session's on-disk profile")
	rootCmd.AddCommand(
		openCmd,
		stateSaveCmd,
		stateLoadCmd,
		detachCmd,
		listCompatCmd,
		closeCompatCmd,
		closeAllCompatCmd,
		killAllCompatCmd,
		deleteDataCompatCmd,
		networkStateSetCmd,
		dialogAcceptCompatCmd,
		dialogDismissCompatCmd,
		tabListCmd,
		tabNewCmd,
		tabSelectCmd,
		tabCloseCmd,
		mouseMoveCompatCmd,
		mouseDownCompatCmd,
		mouseUpCompatCmd,
		mouseWheelCompatCmd,
		keyDownCompatCmd,
		keyUpCompatCmd,
	)
	commandGroups["detach"] = "session"
}

func resolveCompatSessionName(args []string) string {
	if len(args) > 0 && args[0] != "" {
		return args[0]
	}
	if flagSession != "" {
		return flagSession
	}
	if env := sessionNameFromEnv(); env != "" {
		return env
	}
	exitErr("session", fmt.Errorf("session name required (pass NAME, use -s/--session, or set PLAYWRIGHT_CLI_SESSION)"))
	return ""
}

type compatUnsupportedResult struct {
	Supported   bool     `json:"supported"`
	Command     string   `json:"command"`
	Args        []string `json:"args,omitempty"`
	Reason      string   `json:"reason"`
	Alternative string   `json:"alternative"`
}

func unsupportedPlaywrightCommand(name string, args []string, reason string, alternative string) {
	result := compatUnsupportedResult{
		Supported:   false,
		Command:     name,
		Args:        args,
		Reason:      reason,
		Alternative: alternative,
	}
	output(result, fmt.Sprintf("%s unsupported: %s", name, reason))
	os.Exit(2)
}

var detachCmd = &cobra.Command{
	Use:   "detach",
	Short: "Detach from the current connected browser context",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		if !hasReusableBrowserContext() {
			exitErr("detach", fmt.Errorf("session 'default' was not attached; use `close` to stop it"))
		}

		b, _ := openPage()
		defer b.Close()

		output(map[string]any{"action": "detach", "attached": b.Connected()}, "detached from the current browser connection")
	},
}

func hasReusableBrowserContext() bool {
	return strings.TrimSpace(flagConnect) != "" ||
		strings.TrimSpace(flagSession) != "" ||
		sessionNameFromEnv() != "" ||
		(!skipImplicitDaemon && func() bool {
			_, ok := engine.DefaultSession()
			return ok
		}())
}

func closeActiveTab(command string) {
	b, _ := openPage()
	defer b.Close()

	browser := b.RodBrowser()
	tabs, err := engine.ListTabs(browser, b.CurrentTargetID())
	if err != nil {
		exitErr(command, err)
	}
	for i, tab := range tabs {
		if !tab.Active {
			continue
		}
		targetID, err := engine.CloseTab(browser, i)
		if err != nil {
			exitErr(command, err)
		}
		if err := b.DeleteSnapshot(targetID); err != nil {
			exitErr(command, err)
		}
		output(map[string]any{"action": "close", "index": i}, fmt.Sprintf("Closed tab %d", i))
		return
	}
	exitErr(command, fmt.Errorf("no active tab found"))
}
