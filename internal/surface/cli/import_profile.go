package cli

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/dev-toolings/ghostchrome/internal/core/engine"
	"github.com/dev-toolings/ghostchrome/internal/core/storage"
	"github.com/spf13/cobra"
)

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

var (
	flagImportFrom  string
	flagImportTo    string
	flagImportForce bool
	flagImportFull  bool
)

var importProfileCmd = &cobra.Command{
	Use:   "import-profile",
	Short: "Clone an existing Chrome profile (with logged-in cookies) into a ghostchrome profile",
	Long: `Imports an existing Chrome profile directory (where you're already logged in
to LinkedIn, Google, etc.) into ~/.ghostchrome/profiles/<name>.

Subsequent ghostchrome commands using --user-profile <name> will run on the
imported session — no need to relogin or open a Chrome window.

The clone uses your system's Google Chrome binary (not the bundled Chromium)
so that cookies encrypted by macOS Keychain remain decryptable.

IMPORTANT: Quit Google Chrome before importing (Cmd+Q) so SQLite databases
are in a consistent state. The import is a snapshot — later changes in your
real Chrome won't sync.

Examples:
  ghostchrome import-profile --to kev
  ghostchrome import-profile --from "$HOME/Library/Application Support/Google/Chrome/Profile 1" --to work`,
	Run: func(cmd *cobra.Command, args []string) {
		from := flagImportFrom
		if from == "" {
			from = filepath.Join(os.Getenv("HOME"), "Library/Application Support/Google/Chrome/Default")
		}
		to := flagImportTo
		if to == "" {
			to = flagUserProfile
		}
		if to == "" {
			exitErr("import-profile", fmt.Errorf("need --to <name> or --user-profile <name>"))
		}

		if _, err := os.Stat(filepath.Join(from, "Cookies")); err != nil {
			exitErr("import-profile", fmt.Errorf("source profile not found at %s — pass --from", from))
		}

		dst, err := engine.ResolveProfileDir(to)
		if err != nil {
			exitErr("import-profile", err)
		}

		dstDefault := filepath.Join(dst, "Default")
		if _, err := os.Stat(filepath.Join(dstDefault, "Cookies")); err == nil && !flagImportForce {
			exitErr("import-profile", fmt.Errorf("destination %s already has cookies — pass --force to overwrite", dstDefault))
		}

		if err := os.MkdirAll(dstDefault, 0700); err != nil {
			exitErr("import-profile", err)
		}

		fmt.Fprintf(os.Stderr, "Cloning auth-relevant data %s → %s\n", from, dstDefault)
		fmt.Fprintln(os.Stderr, "Quit Google Chrome FIRST (Cmd+Q) for a consistent snapshot.")

		// Whitelist: only what is needed for warm authenticated sessions.
		// We skip the Cookies SQLite — Chrome's per-launch boot logic wipes
		// orphan cookies that don't match the running instance's os_crypt
		// key. Instead we decrypt the source Cookies db, dump to JSON, and
		// replay it via CDP Network.setCookies on every openPage().
		srcCookies := filepath.Join(from, "Cookies")
		records, derr := storage.ExportDecryptedCookies(srcCookies)
		if derr != nil {
			fmt.Fprintf(os.Stderr, "warning: cookie decryption skipped (%v) — session may not authenticate\n", derr)
		} else {
			if err := storage.SaveCookiesJSON(dst, records); err != nil {
				exitErr("import-profile", fmt.Errorf("save cookies json: %w", err))
			}
			fmt.Fprintf(os.Stderr, "Exported %d cookies to %s\n", len(records), storage.CookiesJSONFilename)
		}

		filesToCopy := []string{
			"Login Data",
			"Login Data-journal",
			"Login Data For Account",
			"Login Data For Account-journal",
			"Web Data",
			"Web Data-journal",
			"Preferences",
			"Secure Preferences",
			"Trust Tokens",
			"Trust Tokens-journal",
		}
		dirsToCopy := []string{
			"Local Storage",
		}
		if flagImportFull {
			dirsToCopy = append(dirsToCopy, "Session Storage", "IndexedDB")
		}

		copied, skipped := 0, 0
		for _, name := range filesToCopy {
			src := filepath.Join(from, name)
			if _, err := os.Stat(src); err != nil {
				skipped++
				continue
			}
			dst := filepath.Join(dstDefault, name)
			if err := copyFile(src, dst); err != nil {
				exitErr("import-profile", fmt.Errorf("copy %s: %w", name, err))
			}
			copied++
		}
		for _, name := range dirsToCopy {
			src := filepath.Join(from, name)
			if _, err := os.Stat(src); err != nil {
				skipped++
				continue
			}
			dst := filepath.Join(dstDefault, name)
			cmdCp := exec.Command("rsync", "-a", src+"/", dst+"/")
			cmdCp.Stdout = os.Stderr
			cmdCp.Stderr = os.Stderr
			if err := cmdCp.Run(); err != nil {
				exitErr("import-profile", fmt.Errorf("rsync %s: %w", name, err))
			}
			copied++
		}
		fmt.Fprintf(os.Stderr, "Copied %d items, skipped %d missing.\n", copied, skipped)

		// Try to copy the parent Local State (contains os_crypt key info Chrome
		// may need on Linux; on macOS it's optional but harmless).
		parentLocalState := filepath.Join(filepath.Dir(from), "Local State")
		if _, err := os.Stat(parentLocalState); err == nil {
			if data, err := os.ReadFile(parentLocalState); err == nil {
				_ = os.WriteFile(filepath.Join(dst, "Local State"), data, 0600)
			}
		}

		fmt.Fprintf(os.Stderr, "\nDone. Test with:\n  ghostchrome --user-profile %s eval 'document.title' https://www.linkedin.com/feed/\n", to)
	},
}

func init() {
	importProfileCmd.Flags().StringVar(&flagImportFrom, "from", "", "Source Chrome profile dir (default: ~/Library/Application Support/Google/Chrome/Default)")
	importProfileCmd.Flags().StringVar(&flagImportTo, "to", "", "Destination ghostchrome profile name (default: --user-profile)")
	importProfileCmd.Flags().BoolVar(&flagImportForce, "force", false, "Overwrite existing destination")
	importProfileCmd.Flags().BoolVar(&flagImportFull, "full", false, "Include heavy IndexedDB + Session Storage (usually not needed for auth)")
	rootCmd.AddCommand(importProfileCmd)
}
