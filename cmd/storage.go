package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dev-toolings/ghostchrome/internal/core/storage"
	"github.com/dev-toolings/ghostchrome/internal/core/vault"
	"github.com/spf13/cobra"
)

var (
	flagStorageOutput  string
	flagStorageEncrypt bool
)

var storageCmd = &cobra.Command{
	Use:   "storage",
	Short: "Save or load browser storage state (cookies + localStorage)",
	Long: `Export or restore the authentication state of the browser so an agent
can reuse a logged-in session across runs. The JSON format mirrors Playwright's
storageState so files are portable between the two tools.

Examples:
  # Save current state after logging in manually via --connect + --headless=false
  ghostchrome storage save --output state.json --connect ws://...

  # Restore state into a fresh browser session
  ghostchrome storage load state.json --connect ws://...

Default path (when --output is omitted): $XDG_CACHE_HOME/ghostchrome/state/last.json`,
}

var storageSaveCmd = &cobra.Command{
	Use:   "save",
	Short: "Save cookies + localStorage of the current page",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		b, page := openPage()
		defer b.Close()

		state, err := storage.SaveStorageState(b.RodBrowser(), page)
		if err != nil {
			exitErr("storage save", err)
		}

		path := flagStorageOutput
		if path == "" {
			dir, derr := defaultStorageDir()
			if derr != nil {
				exitErr("storage save", derr)
			}
			path = filepath.Join(dir, "last.json")
		}

		if flagStorageEncrypt || strings.HasSuffix(path, ".enc") {
			v, err := vault.NewFromEnv()
			if err != nil {
				exitErr("storage save", fmt.Errorf("--encrypt requires GHOSTCHROME_VAULT_KEY env var: %w", err))
			}
			if !strings.HasSuffix(path, ".enc") {
				path += ".enc"
			}
			if err := v.SaveFile(path, state); err != nil {
				exitErr("storage save", err)
			}
		} else {
			data, err := json.MarshalIndent(state, "", "  ")
			if err != nil {
				exitErr("storage save", err)
			}
			if err := os.WriteFile(path, data, 0o600); err != nil {
				exitErr("storage save", err)
			}
		}

		type saveResult struct {
			Action  string `json:"action"`
			Path    string `json:"path"`
			Cookies int    `json:"cookies"`
			Origins int    `json:"origins"`
		}
		text := fmt.Sprintf("Storage saved to %s (%d cookies, %d origins)", path, len(state.Cookies), len(state.Origins))
		output(&saveResult{Action: "save", Path: path, Cookies: len(state.Cookies), Origins: len(state.Origins)}, text)
	},
}

var storageLoadCmd = &cobra.Command{
	Use:   "load <state.json>",
	Short: "Restore cookies + localStorage from a state file",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		path := args[0]
		state, err := readStorageStateFile(path)
		if err != nil {
			exitErr("storage load", err)
		}

		b, page := openPage()
		defer b.Close()

		if err := storage.LoadStorageState(b.RodBrowser(), page, state); err != nil {
			exitErr("storage load", err)
		}

		type loadResult struct {
			Action  string `json:"action"`
			Path    string `json:"path"`
			Cookies int    `json:"cookies"`
			Origins int    `json:"origins"`
		}
		text := fmt.Sprintf("Storage loaded from %s (%d cookies, %d origins)", path, len(state.Cookies), len(state.Origins))
		output(&loadResult{Action: "load", Path: path, Cookies: len(state.Cookies), Origins: len(state.Origins)}, text)
	},
}

func readStorageStateFile(path string) (*storage.StorageState, error) {
	var state storage.StorageState
	if strings.HasSuffix(path, ".enc") {
		v, err := vault.NewFromEnv()
		if err != nil {
			return nil, fmt.Errorf("encrypted file requires GHOSTCHROME_VAULT_KEY env var: %w", err)
		}
		if err := v.LoadFile(path, &state); err != nil {
			return nil, err
		}
		return &state, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &state, nil
}

func defaultStorageDir() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		base = os.TempDir()
	}
	dir := filepath.Join(base, "ghostchrome", "state")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

func init() {
	storageSaveCmd.Flags().StringVar(&flagStorageOutput, "output", "", "Output file path (default: $XDG_CACHE_HOME/ghostchrome/state/last.json)")
	storageSaveCmd.Flags().BoolVar(&flagStorageEncrypt, "encrypt", false, "Encrypt the output with AES-256-GCM (requires GHOSTCHROME_VAULT_KEY env var)")
	storageCmd.AddCommand(storageSaveCmd, storageLoadCmd)
	rootCmd.AddCommand(storageCmd)
}
