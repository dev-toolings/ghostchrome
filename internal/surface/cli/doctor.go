package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/dev-toolings/ghostchrome/engine"
	"github.com/spf13/cobra"
)

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Diagnose ghostchrome setup (Chrome, profiles, extensions, connectivity)",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		type check struct {
			Name   string `json:"name"`
			Status string `json:"status"`
			Detail string `json:"detail,omitempty"`
		}
		var checks []check

		add := func(name, status, detail string) {
			checks = append(checks, check{Name: name, Status: status, Detail: detail})
		}

		// OS info
		add("os", "ok", fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH))

		// Chrome binary
		chromePath := engine.FindSystemChromeBinary()
		if chromePath != "" {
			version := chromeVersion(chromePath)
			add("chrome", "ok", fmt.Sprintf("%s (%s)", chromePath, version))
		} else {
			add("chrome", "warn", "no system Chrome found — will download bundled Chromium on first run")
		}

		// Profiles directory
		home, _ := os.UserHomeDir()
		profilesDir := filepath.Join(home, ".ghostchrome", "profiles")
		if entries, err := os.ReadDir(profilesDir); err == nil {
			var names []string
			for _, e := range entries {
				if e.IsDir() {
					names = append(names, e.Name())
				}
			}
			if len(names) > 0 {
				add("profiles", "ok", fmt.Sprintf("%d profiles: %s", len(names), strings.Join(names, ", ")))
			} else {
				add("profiles", "ok", "no profiles (use --user-profile to create)")
			}
		} else {
			add("profiles", "ok", "profiles directory not yet created")
		}

		// Extensions
		extDir := filepath.Join(home, ".ghostchrome", "extensions")
		if entries, err := os.ReadDir(extDir); err == nil {
			var names []string
			for _, e := range entries {
				if e.IsDir() {
					names = append(names, e.Name())
				}
			}
			add("extensions", "ok", fmt.Sprintf("%d bundled: %s", len(names), strings.Join(names, ", ")))
		} else {
			add("extensions", "ok", "no bundled extensions")
		}

		// Init scripts
		initScripts, err := engine.ListInitScripts()
		if err == nil && len(initScripts) > 0 {
			add("init-scripts", "ok", fmt.Sprintf("%d scripts: %s", len(initScripts), strings.Join(initScripts, ", ")))
		} else {
			add("init-scripts", "ok", "none installed")
		}

		// CDP connectivity (try auto-discover)
		if ws, err := engine.DiscoverCDP(nil, 500*time.Millisecond); err == nil {
			add("cdp-auto", "ok", ws)
		} else {
			add("cdp-auto", "info", "no running Chrome with remote debugging found")
		}

		// Proxy
		if p := os.Getenv("GHOSTCHROME_PROXY"); p != "" {
			add("proxy", "ok", p)
		}

		// Vault key
		if os.Getenv("GHOSTCHROME_VAULT_KEY") != "" {
			add("vault", "ok", "GHOSTCHROME_VAULT_KEY is set")
		} else {
			add("vault", "info", "GHOSTCHROME_VAULT_KEY not set (encrypted storage unavailable)")
		}

		// Text output
		var sb strings.Builder
		sb.WriteString("[doctor] ghostchrome diagnostics\n")
		allOK := true
		for _, c := range checks {
			icon := "✓"
			switch c.Status {
			case "warn":
				icon = "⚠"
				allOK = false
			case "fail":
				icon = "✗"
				allOK = false
			case "info":
				icon = "ℹ"
			}
			line := fmt.Sprintf("  %s %-15s %s", icon, c.Name, c.Detail)
			sb.WriteString(line + "\n")
		}
		if allOK {
			sb.WriteString("\n  All checks passed.")
		}
		output(checks, strings.TrimRight(sb.String(), "\n"))
	},
}

func chromeVersion(path string) string {
	out, err := exec.Command(path, "--version").Output()
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(out))
}

func init() {
	rootCmd.AddCommand(doctorCmd)
}
