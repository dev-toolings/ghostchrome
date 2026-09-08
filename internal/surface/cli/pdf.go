package cli

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/go-rod/rod/lib/proto"
	"github.com/spf13/cobra"
)

var (
	flagPDFOutput    string
	flagPDFLandscape bool
	flagPDFFormat    string
	flagPDFScale     float64
	flagPDFPrintBg   bool
)

// paperSizes is in inches (CDP expects inches).
var paperSizes = map[string][2]float64{
	"A4":     {8.27, 11.69},
	"A3":     {11.69, 16.54},
	"Letter": {8.5, 11},
	"Legal":  {8.5, 14},
}

var pdfCmd = &cobra.Command{
	Use:   "pdf [url]",
	Short: "Export the page as a PDF",
	Long: `Render the page to PDF via Chrome's Page.printToPDF. Works best in
headless mode (ghostchrome's default). Files are written owner-only (0o600).

Examples:
  ghostchrome pdf --connect ws://...
  ghostchrome pdf https://en.wikipedia.org/wiki/Web_browser --output wiki.pdf
  ghostchrome pdf https://x --landscape --format Letter
  ghostchrome pdf https://y --scale 0.8 --print-background`,
	Args: cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		targetURL := ""
		if len(args) > 0 {
			targetURL = args[0]
		}

		paper, ok := paperSizes[flagPDFFormat]
		if !ok {
			exitErr("pdf", fmt.Errorf("unknown --format %q (use A4, A3, Letter, Legal)", flagPDFFormat))
		}
		if flagPDFScale <= 0 {
			flagPDFScale = 1.0
		}

		outPath := flagPDFOutput
		if outPath == "" {
			dir, err := defaultPDFDir()
			if err != nil {
				exitErr("pdf", err)
			}
			outPath = filepath.Join(dir, fmt.Sprintf("ghostchrome-%d.pdf", time.Now().UnixMilli()))
		}

		b, page := openPage()
		defer b.Close()

		navigateIfRequested(page, targetURL, "load")
		if targetURL == "" {
			if info, err := page.Info(); err == nil && info != nil {
				targetURL = info.URL
			}
		}

		req := proto.PagePrintToPDF{
			Landscape:       flagPDFLandscape,
			PrintBackground: flagPDFPrintBg,
			PaperWidth:      &paper[0],
			PaperHeight:     &paper[1],
			Scale:           &flagPDFScale,
		}
		res, err := req.Call(page)
		if err != nil {
			exitErr("pdf", err)
		}

		data, err := base64.StdEncoding.DecodeString(string(res.Data))
		if err != nil {
			// Rod's Data is already []byte base64-decoded in some versions; try raw.
			data = []byte(res.Data)
		}
		if err := os.WriteFile(outPath, data, 0o600); err != nil {
			exitErr("pdf", err)
		}

		type pdfResult struct {
			Action    string `json:"action"`
			URL       string `json:"url"`
			Path      string `json:"path"`
			SizeBytes int    `json:"size_bytes"`
			Format    string `json:"format"`
			Landscape bool   `json:"landscape,omitempty"`
		}
		text := fmt.Sprintf("PDF saved to %s (%d bytes, %s%s)", outPath, len(data), flagPDFFormat, landscapeSuffix(flagPDFLandscape))
		output(&pdfResult{
			Action:    "pdf",
			URL:       targetURL,
			Path:      outPath,
			SizeBytes: len(data),
			Format:    flagPDFFormat,
			Landscape: flagPDFLandscape,
		}, text)
	},
}

func defaultPDFDir() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		base = os.TempDir()
	}
	dir := filepath.Join(base, "ghostchrome", "pdfs")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

func landscapeSuffix(landscape bool) string {
	if landscape {
		return " landscape"
	}
	return ""
}

func init() {
	pdfCmd.Flags().StringVar(&flagPDFOutput, "output", "", "Output file path (default: $XDG_CACHE_HOME/ghostchrome/pdfs/*.pdf)")
	pdfCmd.Flags().StringVar(&flagPDFOutput, "filename", "", "Output file path (Playwright CLI-compatible alias for --output)")
	pdfCmd.Flags().BoolVar(&flagPDFLandscape, "landscape", false, "Landscape orientation")
	pdfCmd.Flags().StringVar(&flagPDFFormat, "format", "A4", "Paper format: A4, A3, Letter, Legal")
	pdfCmd.Flags().Float64Var(&flagPDFScale, "scale", 1.0, "Scale (0.1 to 2.0)")
	pdfCmd.Flags().BoolVar(&flagPDFPrintBg, "print-background", false, "Include CSS background colors and images")
	rootCmd.AddCommand(pdfCmd)
}
