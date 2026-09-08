package cli

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/dev-toolings/ghostchrome/internal/core/engine"
	"github.com/spf13/cobra"
)

var (
	flagAttachCDP       string
	flagAttachEndpoint  string
	flagAttachExtension string
)

var attachCmd = &cobra.Command{
	Use:   "attach [session-name]",
	Short: "Attach to an existing browser",
	Long: `Attach to an existing Chromium browser via Chrome DevTools Protocol.
Supported: --cdp=<channel>, --cdp=http(s)://host:port, and --cdp=ws(s)://...
Channel attach scans local CDP debug ports for an already-running Chrome/Edge
browser with remote debugging enabled.
Also supports attach <session-name> for an already-registered ghostchrome
session. Playwright test debug sessions are not ghostchrome sessions and remain
unsupported unless they expose a CDP endpoint.
Unsupported by ghostchrome: Playwright server endpoints and extension attach.`,
	Args: cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		if flagAttachEndpoint != "" {
			unsupportedAttach("endpoint", flagAttachEndpoint)
		}
		if flagAttachExtension != "" {
			unsupportedAttach("extension", flagAttachExtension)
		}
		if len(args) > 0 {
			if flagAttachCDP != "" {
				exitErr("attach", fmt.Errorf("provide either <session-name> or --cdp, not both"))
			}
			entry, err := engine.ResolveSession(args[0])
			if err != nil {
				exitErr("attach", err)
			}
			name := flagSession
			if name == "" {
				name = engine.DefaultSessionName
			}
			if name == entry.Name {
				type attachResult struct {
					Session       string `json:"session"`
					SourceSession string `json:"source_session,omitempty"`
					WSURL         string `json:"ws_url"`
				}
				output(attachResult{Session: entry.Name, SourceSession: entry.Name, WSURL: entry.WSURL},
					fmt.Sprintf("attached %s to session %s", entry.Name, entry.Name))
				return
			}
			attached, err := engine.AttachSession(name, entry.WSURL)
			if err != nil {
				exitErr("attach", err)
			}
			type attachResult struct {
				Session       string `json:"session"`
				SourceSession string `json:"source_session,omitempty"`
				WSURL         string `json:"ws_url"`
			}
			output(attachResult{Session: attached.Name, SourceSession: entry.Name, WSURL: attached.WSURL},
				fmt.Sprintf("attached %s to session %s", attached.Name, entry.Name))
			return
		}
		if flagAttachCDP == "" {
			exitErr("attach", fmt.Errorf("missing --cdp endpoint"))
		}
		var (
			ws  string
			err error
		)
		timeout := time.Duration(flagTimeout) * time.Second
		if strings.Contains(flagAttachCDP, "://") {
			ws, err = engine.ResolveCDPEndpoint(flagAttachCDP, timeout)
		} else {
			ws, err = engine.DiscoverCDPChannel(flagAttachCDP, nil, timeout)
		}
		if err != nil {
			exitErr("attach", err)
		}
		name := flagSession
		if name == "" {
			name = engine.DefaultSessionName
		}
		entry, err := engine.AttachSession(name, ws)
		if err != nil {
			exitErr("attach", err)
		}

		type attachResult struct {
			Session string `json:"session"`
			WSURL   string `json:"ws_url"`
		}
		output(attachResult{Session: entry.Name, WSURL: entry.WSURL},
			fmt.Sprintf("attached %s to %s", entry.Name, entry.WSURL))
	},
}

type unsupportedAttachResult struct {
	Supported   bool   `json:"supported"`
	Mode        string `json:"mode"`
	Value       string `json:"value,omitempty"`
	Reason      string `json:"reason"`
	Alternative string `json:"alternative"`
}

func buildUnsupportedAttachResult(mode string, value string) unsupportedAttachResult {
	result := unsupportedAttachResult{
		Supported: false,
		Mode:      mode,
		Value:     value,
	}
	switch mode {
	case "endpoint":
		result.Reason = "Playwright server endpoints require the Playwright protocol; ghostchrome currently attaches through Chrome DevTools Protocol only"
		result.Alternative = "Use `ghostchrome attach --cdp=<ws-or-http-endpoint>` for Chromium CDP, or use playwright-cli for Playwright server endpoints."
	case "extension":
		result.Reason = "Playwright extension attach requires the Playwright browser extension bridge; ghostchrome does not implement that bridge"
		result.Alternative = "Use `ghostchrome attach --cdp=<channel-or-endpoint>` when remote debugging is enabled, or use playwright-cli for extension attach."
	default:
		result.Reason = "attach mode is not supported"
		result.Alternative = "Use `ghostchrome attach --cdp=<channel-or-endpoint>`."
	}
	return result
}

func unsupportedAttach(mode string, value string) {
	result := buildUnsupportedAttachResult(mode, value)
	output(result, fmt.Sprintf("attach --%s unsupported: %s", mode, result.Reason))
	os.Exit(2)
}

func init() {
	attachCmd.Flags().StringVar(&flagAttachCDP, "cdp", "", "Chrome DevTools Protocol endpoint or channel: chrome, chrome-beta, chrome-dev, chrome-canary, msedge, msedge-beta, msedge-dev, msedge-canary, http(s)://host:port, or ws(s)://...")
	attachCmd.Flags().StringVar(&flagAttachEndpoint, "endpoint", "", "Unsupported Playwright server endpoint")
	attachCmd.Flags().StringVar(&flagAttachExtension, "extension", "", "Unsupported Playwright extension channel")
	attachCmd.Flags().Lookup("extension").NoOptDefVal = "chrome"
	rootCmd.AddCommand(attachCmd)
	commandGroups["attach"] = "session"
}
