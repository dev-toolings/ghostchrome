package cli

// The setup command tree is flag parsing and output only: every installation,
// transport and diagnostic decision lives in internal/setup.

import (
	"errors"
	"fmt"
	"strings"

	"github.com/dev-toolings/ghostchrome/internal/setup"
	"github.com/spf13/cobra"
)

var (
	setupModeFlag          string
	setupClientsFlag       string
	setupSwitchTo          string
	setupSwitchYes         bool
	setupUninstallYes      bool
	setupPurgeData         bool
	setupDoctorStrict      bool
	setupInstructionsWrite bool
)

// jsonOutput reports whether the global output flags selected JSON.
func jsonOutput() bool {
	return flagFormat == "json" || flagJSON
}

func setupCommandError(action string, err error) error {
	return fmt.Errorf("setup %s: %w", action, err)
}

var setupCmd = &cobra.Command{
	Use:   "setup",
	Short: "Install exactly one Ghostchrome transport and its global skill",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if strings.TrimSpace(setupModeFlag) == "" {
			return errors.New("--mode is required on first setup; choose cli or mcp")
		}
		mode, err := setup.ParseMode(setupModeFlag)
		if err != nil {
			return setupCommandError("install", err)
		}
		clients, err := setup.ParseClients(setupClientsFlag)
		if err != nil {
			return setupCommandError("install", err)
		}
		manifest, err := setup.Install(mode, clients, false)
		if err != nil {
			return setupCommandError("install", err)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "ghostchrome setup installed in %s mode\n  binary: %s\n", manifest.Mode, manifest.Binary)
		return nil
	},
}

var setupStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show the active Ghostchrome setup mode",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		status, err := setup.GatherStatus()
		if err != nil {
			return setupCommandError("status", err)
		}
		return setup.PrintStatus(cmd.OutOrStdout(), status, jsonOutput())
	},
}

var setupDoctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Validate the selected artifact, skill, clients, Chrome and CDP",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		checks, err := setup.RunDoctor(setupDoctorStrict)
		if err != nil {
			return setupCommandError("doctor", err)
		}
		return setup.PrintDoctor(cmd.OutOrStdout(), checks, setupDoctorStrict, jsonOutput())
	},
}

var setupSwitchCmd = &cobra.Command{
	Use:   "switch",
	Short: "Explicitly switch between CLI and MCP mode",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !setupSwitchYes {
			return errors.New("refusing to switch setup mode without --yes")
		}
		mode, err := setup.ParseMode(setupSwitchTo)
		if err != nil {
			return setupCommandError("switch", err)
		}
		home, err := setup.Home()
		if err != nil {
			return setupCommandError("switch", err)
		}
		paths := setup.PathsForHome(home)
		manifest, err := setup.ReadManifest(paths)
		if err != nil {
			return setupCommandError("switch", err)
		}
		clients := []string{"claude", "codex", "grok"}
		if manifest != nil && len(manifest.Clients) > 0 {
			clients = manifest.Clients
		} else if strings.TrimSpace(setupClientsFlag) != "" && setupClientsFlag != setup.DefaultClients {
			clients, err = setup.ParseClients(setupClientsFlag)
			if err != nil {
				return setupCommandError("switch", err)
			}
		}
		if manifest == nil {
			legacy, legacyErr := setup.LegacyMCPPresent(paths, clients)
			if legacyErr != nil {
				return setupCommandError("switch", legacyErr)
			}
			if !legacy {
				return errors.New("no existing Ghostchrome setup to switch; use `ghostchrome setup --mode cli|mcp`")
			}
		}
		newManifest, err := setup.Install(mode, clients, true)
		if err != nil {
			return setupCommandError("switch", err)
		}
		checks, doctorErr := setup.RunDoctor(true)
		if doctorErr != nil {
			return setupCommandError("switch", fmt.Errorf("post-switch doctor: %w", doctorErr))
		}
		for _, check := range checks {
			if check.Status == "fail" {
				return setupCommandError("switch", fmt.Errorf("post-switch doctor failed: %s", check.Detail))
			}
		}
		fmt.Fprintf(cmd.OutOrStdout(), "ghostchrome setup switched to %s mode\n  binary: %s\n", newManifest.Mode, newManifest.Binary)
		return nil
	},
}

var setupUninstallCmd = &cobra.Command{
	Use:   "uninstall",
	Short: "Remove the managed transport, skill and registrations",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !setupUninstallYes {
			return errors.New("refusing to uninstall without --yes")
		}
		return setup.Uninstall(true, setupPurgeData, cmd.OutOrStdout(), cmd.ErrOrStderr())
	},
}

func init() {
	setupCmd.Flags().StringVar(&setupModeFlag, "mode", "", "Installation mode: cli or mcp")
	setupCmd.Flags().StringVar(&setupClientsFlag, "clients", setup.DefaultClients, "Comma-separated global clients: claude,codex,grok")
	setupDoctorCmd.Flags().BoolVar(&setupDoctorStrict, "strict", false, "Exit non-zero when a hard check fails")
	setupSwitchCmd.Flags().StringVar(&setupSwitchTo, "to", "", "Target installation mode: cli or mcp")
	setupSwitchCmd.Flags().BoolVar(&setupSwitchYes, "yes", false, "Confirm the explicit transport switch")
	setupUninstallCmd.Flags().BoolVar(&setupUninstallYes, "yes", false, "Confirm removal of managed setup files")
	setupUninstallCmd.Flags().BoolVar(&setupPurgeData, "purge-data", false, "Also remove browser profiles and Ghostchrome data")
	setupCmd.AddCommand(setupStatusCmd, setupDoctorCmd, setupSwitchCmd, setupUninstallCmd)
	rootCmd.AddCommand(setupCmd)
	commandGroups["setup"] = "util"
}
