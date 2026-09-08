package cli

import (
	"fmt"
	"os"

	"github.com/dev-toolings/ghostchrome/engine"
	"github.com/spf13/cobra"
)

// Chrome Web Store IDs for the bundled defaults.
var defaultExtensionCatalog = []struct {
	Slug, Name, ID string
}{
	{"ublock", "uBlock Origin Lite", "ddkjiahejlhfcafbddmgiahcphecmpfh"},
	{"icdc", "I still don't care about cookies", "edibdbjcniadpccecjdfdjjppcpchdlm"},
	{"force-bg", "Force Background Tab", "gidgenkbbabolejbgbpnhbimgjbffefm"},
}

var extensionsCmd = &cobra.Command{
	Use:   "extensions",
	Short: "Manage bundled Chrome extensions used by --default-extensions",
}

var extensionsInstallCmd = &cobra.Command{
	Use:   "install",
	Short: "Show install instructions for the bundled default extensions",
	Long: `Print step-by-step instructions to install the three default extensions
(uBlock Origin Lite, "I still don't care about cookies", Force Background Tab)
under ~/.ghostchrome/extensions/<slug>/, ready for --default-extensions.

ghostchrome does not download CRX files automatically: the Chrome Web Store
update protocol is fragile and undocumented. Use a trusted CRX downloader
(e.g. crxdownloader.com) and unzip the archive into the expected path.`,
	Args: cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		base, err := engine.DefaultExtensionsDir()
		if err != nil {
			exitErr("extensions install", err)
		}
		fmt.Printf("ghostchrome bundled extensions install guide\n\n")
		fmt.Printf("Target directory: %s\n\n", base)
		fmt.Println("Extensions:")
		for _, e := range defaultExtensionCatalog {
			storeURL := fmt.Sprintf("https://chromewebstore.google.com/detail/%s", e.ID)
			fmt.Printf("  - %-32s id=%s\n", e.Name, e.ID)
			fmt.Printf("      web store : %s\n", storeURL)
			fmt.Printf("      target    : %s/%s/\n", base, e.Slug)
		}
		fmt.Println()
		fmt.Println("Steps:")
		fmt.Println("  1. Download each .crx from the Chrome Web Store or via crxdownloader.com")
		fmt.Println("     (paste the extension id above).")
		fmt.Printf("  2. Unzip each archive into %s/{ublock,icdc,force-bg}/\n", base)
		fmt.Println("     (a .crx is a zip with extra header bytes — `unzip file.crx -d <dir>` works).")
		fmt.Printf("  3. Verify: ls %s/*/manifest.json\n", base)
		fmt.Println("  4. Launch with: ghostchrome --default-extensions ...")
		fmt.Println()
		fmt.Println("Note: extensions require Chrome's modern headless mode (HeadlessNew),")
		fmt.Println("which ghostchrome already uses. They are ignored when --connect is set.")
		// Best-effort: create the base dir so the user has a place to drop files.
		if err := os.MkdirAll(base, 0o755); err == nil {
			fmt.Printf("\nCreated %s (empty) — ready for unpacked extensions.\n", base)
		}
	},
}

func init() {
	extensionsCmd.AddCommand(extensionsInstallCmd)
	rootCmd.AddCommand(extensionsCmd)
}
