package cli

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/dev-toolings/ghostchrome/engine"
	"github.com/spf13/cobra"
)

var (
	flagEmulateDevice      string
	flagEmulateUserAgent   string
	flagEmulateColorScheme string
	flagEmulateTimezone    string
	flagEmulateList        bool
	flagEmulateReset       bool
)

var emulateCmd = &cobra.Command{
	Use:   "emulate",
	Short: "Emulate a device or override UA / color-scheme / timezone",
	Long: `Switch the browser to a device profile (viewport + UA + DPR + touch) or
override individual emulation axes.

CDP emulation overrides only live as long as the CDP websocket that set them,
so ghostchrome persists the requested profile per managed session (the implicit
daemon or "-s <name>") and replays it on every following command. Use
"ghostchrome emulate --reset" to go back to a plain, un-emulated tab.

Emulation is NOT replayed on a Chrome you attached to yourself with
--connect / --connect=auto: mutating a foreign tab is out of policy there. For
those, keep the whole flow in one process with "ghostchrome batch" and an
"emulate ..." verb as the first line.

Device presets (--device):
  iphone-se, iphone-14, iphone-14-pro, iphone-14-pro-max,
  pixel-7, pixel-8-pro, ipad, ipad-pro, desktop, desktop-2k

Per-axis overrides:
  --user-agent "<ua string>"
  --color-scheme dark|light|no-preference
  --timezone Europe/Paris      (IANA tz database name)

Examples:
  ghostchrome emulate --device iphone-14-pro
  ghostchrome emulate --user-agent "Mozilla/5.0 ..." --color-scheme dark
  ghostchrome emulate --reset                        # drop every override
  ghostchrome emulate --list                         # print available presets

  # Single-process flow (works even on a foreign --connect Chrome):
  printf 'emulate device=iphone-14\nnavigate https://m.example.com\nextract\n' | \
    ghostchrome batch -`,
	Run: func(cmd *cobra.Command, args []string) {
		if flagEmulateList {
			output(listDevices(), formatDeviceList())
			return
		}
		if flagEmulateReset {
			if flagEmulateDevice != "" || flagEmulateUserAgent != "" || flagEmulateColorScheme != "" || flagEmulateTimezone != "" {
				exitErr("emulate", fmt.Errorf("--reset cannot be combined with other emulation flags"))
			}
			b, page := openPage()
			defer b.Close()
			if err := engine.ResetEmulation(page); err != nil {
				exitErr("emulate", err)
			}
			if err := b.ClearEmulationState(); err != nil {
				fmt.Fprintf(os.Stderr, "warning: emulation profile not cleared for this session: %v\n", err)
			}
			type emulateResetResult struct {
				Action string `json:"action"`
				Reset  bool   `json:"reset"`
			}
			output(&emulateResetResult{Action: "emulate", Reset: true}, "[emulate] reset: no viewport, touch, UA, color-scheme or timezone override left")
			return
		}
		if flagEmulateDevice == "" && flagEmulateUserAgent == "" && flagEmulateColorScheme == "" && flagEmulateTimezone == "" {
			exitErr("emulate", fmt.Errorf("need --device, --user-agent, --color-scheme, --timezone, --reset, or --list"))
		}

		b, page := openPage()
		defer b.Close()

		applied := map[string]string{}
		state := b.EmulationState()

		if flagEmulateDevice != "" {
			d, ok := engine.DeviceByName(flagEmulateDevice)
			if !ok {
				exitErr("emulate", fmt.Errorf("unknown --device %q (use --list to see presets)", flagEmulateDevice))
			}
			if err := engine.ApplyDevice(page, d); err != nil {
				exitErr("emulate", err)
			}
			deviceState := engine.EmulationFromDevice(d)
			// ApplyDevice leaves the current UA in place for presets that
			// carry none (the desktop ones), so the profile must too.
			if deviceState.UserAgent == "" {
				deviceState.UserAgent = state.UserAgent
			}
			deviceState.ColorScheme = state.ColorScheme
			deviceState.Timezone = state.Timezone
			state = deviceState
			applied["device"] = d.Name
			applied["viewport"] = fmt.Sprintf("%dx%d@%.1fx", d.Width, d.Height, d.DPR)
		}
		if flagEmulateUserAgent != "" {
			if err := engine.ApplyUserAgent(page, flagEmulateUserAgent); err != nil {
				exitErr("emulate", err)
			}
			state.UserAgent = flagEmulateUserAgent
			applied["user-agent"] = flagEmulateUserAgent
		}
		if flagEmulateColorScheme != "" {
			if err := engine.ApplyColorScheme(page, flagEmulateColorScheme); err != nil {
				exitErr("emulate", err)
			}
			state.ColorScheme = flagEmulateColorScheme
			applied["color-scheme"] = flagEmulateColorScheme
		}
		if flagEmulateTimezone != "" {
			if err := engine.ApplyTimezone(page, flagEmulateTimezone); err != nil {
				exitErr("emulate", err)
			}
			state.Timezone = flagEmulateTimezone
			applied["timezone"] = flagEmulateTimezone
		}
		// Persist so the next CLI invocation replays it: the CDP overrides
		// themselves die with this process's DevTools session.
		if err := b.SetEmulationState(state); err != nil {
			fmt.Fprintf(os.Stderr, "warning: emulation not persisted for this session: %v\n", err)
		}

		type emulateResult struct {
			Action  string            `json:"action"`
			Applied map[string]string `json:"applied"`
		}

		var sb strings.Builder
		sb.WriteString("[emulate] applied:")
		keys := make([]string, 0, len(applied))
		for k := range applied {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(&sb, " %s=%s", k, applied[k])
		}
		output(&emulateResult{Action: "emulate", Applied: applied}, sb.String())
	},
}

func listDevices() []engine.Device {
	devices := engine.ListDevices()
	sort.Slice(devices, func(i, j int) bool { return devices[i].Name < devices[j].Name })
	return devices
}

func formatDeviceList() string {
	var sb strings.Builder
	sb.WriteString("[devices]\n")
	for _, d := range listDevices() {
		fmt.Fprintf(&sb, "  %-20s %dx%d @%.1fx %s %s\n",
			d.Name, d.Width, d.Height, d.DPR,
			tag(d.Mobile, "mobile"), tag(d.Touch, "touch"))
	}
	return strings.TrimRight(sb.String(), "\n")
}

func tag(b bool, label string) string {
	if b {
		return label
	}
	return strings.Repeat(" ", len(label))
}

func init() {
	emulateCmd.Flags().StringVar(&flagEmulateDevice, "device", "", "Device preset (see --list)")
	emulateCmd.Flags().StringVar(&flagEmulateUserAgent, "user-agent", "", "Override the user-agent header and navigator.userAgent")
	emulateCmd.Flags().StringVar(&flagEmulateColorScheme, "color-scheme", "", "Emulate prefers-color-scheme: dark, light, no-preference")
	emulateCmd.Flags().StringVar(&flagEmulateTimezone, "timezone", "", "Override the timezone (IANA tz name)")
	emulateCmd.Flags().BoolVar(&flagEmulateReset, "reset", false, "Drop every emulation override (viewport, touch, UA, color-scheme, timezone)")
	emulateCmd.Flags().BoolVar(&flagEmulateList, "list", false, "List available device presets")
	rootCmd.AddCommand(emulateCmd)
}
