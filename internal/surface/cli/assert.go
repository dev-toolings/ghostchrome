package cli

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/dev-toolings/ghostchrome/engine"
	"github.com/go-rod/rod"
	"github.com/spf13/cobra"
)

// assertFail prints a failure line and exits 1 — all assert subcommands share
// this so CI/agents see a consistent format.
func assertFail(name string, details string) {
	fmt.Fprintf(os.Stderr, "FAIL [assert:%s] %s\n", name, details)
	exitNow(1)
}

func assertPass(name string, details string) {
	if details == "" {
		fmt.Printf("OK [assert:%s]\n", name)
	} else {
		fmt.Printf("OK [assert:%s] %s\n", name, details)
	}
}

var assertCmd = &cobra.Command{
	Use:   "assert",
	Short: "Assertion verbs with exit codes (0 = pass, 1 = fail)",
	Long: `Fast validation primitives for frontend flows. Each subcommand exits 0 on
success and 1 on failure, with a PASS/FAIL line on stdout/stderr respectively.

Chain in shell:
  ghostchrome click @3 && \
  ghostchrome assert url-matches "/dashboard" && \
  ghostchrome assert text "Welcome"`,
}

var (
	flagAssertRegex  bool
	flagAssertMin    int
	flagAssertMax    int
	flagAssertEquals int
)

var assertTextCmd = &cobra.Command{
	Use:   "text <substring>",
	Short: "Assert that the page body contains the given text (substring)",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		needle := args[0]

		b, page := openPage()
		defer b.Close()

		body, err := readBodyText(page)
		if err != nil {
			assertFail("text", err.Error())
		}

		if flagAssertRegex {
			re, err := regexp.Compile(needle)
			if err != nil {
				assertFail("text", fmt.Sprintf("invalid regex: %v", err))
			}
			if !re.MatchString(body) {
				assertFail("text", fmt.Sprintf("body does not match /%s/", needle))
			}
		} else if !strings.Contains(strings.ToLower(body), strings.ToLower(needle)) {
			assertFail("text", fmt.Sprintf("body does not contain %q", needle))
		}

		assertPass("text", fmt.Sprintf("%q", needle))
	},
}

var assertSelectorVisibleCmd = &cobra.Command{
	Use:   "selector-visible <css>",
	Short: "Assert that at least one element matching the selector is visible",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		selector := args[0]

		b, page := openPage()
		defer b.Close()

		els, err := page.Elements(selector)
		if err != nil {
			assertFail("selector-visible", err.Error())
		}
		if len(els) == 0 {
			assertFail("selector-visible", fmt.Sprintf("no element matches %q", selector))
		}
		for _, el := range els {
			visible, _ := el.Visible()
			if visible {
				assertPass("selector-visible", selector)
				return
			}
		}
		assertFail("selector-visible", fmt.Sprintf("elements found but none visible for %q", selector))
	},
}

var assertURLCmd = &cobra.Command{
	Use:   "url-matches <pattern>",
	Short: "Assert that the current URL contains / matches the pattern",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		pattern := args[0]

		b, page := openPage()
		defer b.Close()

		info, err := page.Info()
		if err != nil {
			assertFail("url-matches", err.Error())
		}

		ok := false
		if flagAssertRegex {
			re, err := regexp.Compile(pattern)
			if err != nil {
				assertFail("url-matches", fmt.Sprintf("invalid regex: %v", err))
			}
			ok = re.MatchString(info.URL)
		} else {
			ok = strings.Contains(info.URL, pattern)
		}
		if !ok {
			assertFail("url-matches", fmt.Sprintf("url=%q does not match %q", info.URL, pattern))
		}
		assertPass("url-matches", info.URL)
	},
}

var assertTitleCmd = &cobra.Command{
	Use:   "title-matches <pattern>",
	Short: "Assert that the page title contains / matches the pattern",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		pattern := args[0]

		b, page := openPage()
		defer b.Close()

		info, err := page.Info()
		if err != nil {
			assertFail("title-matches", err.Error())
		}

		ok := false
		if flagAssertRegex {
			re, err := regexp.Compile(pattern)
			if err != nil {
				assertFail("title-matches", fmt.Sprintf("invalid regex: %v", err))
			}
			ok = re.MatchString(info.Title)
		} else {
			ok = strings.Contains(strings.ToLower(info.Title), strings.ToLower(pattern))
		}
		if !ok {
			assertFail("title-matches", fmt.Sprintf("title=%q does not match %q", info.Title, pattern))
		}
		assertPass("title-matches", info.Title)
	},
}

var assertCountCmd = &cobra.Command{
	Use:   "count <selector>",
	Short: "Assert that a selector matches --min/--max/--equals elements",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		selector := args[0]
		if flagAssertMin == 0 && flagAssertMax == 0 && flagAssertEquals == 0 {
			assertFail("count", "set at least one of --min / --max / --equals")
		}

		b, page := openPage()
		defer b.Close()

		n := engine.CountSelector(page, selector)
		if flagAssertEquals > 0 && n != flagAssertEquals {
			assertFail("count", fmt.Sprintf("selector=%q has %d elements, want ==%d", selector, n, flagAssertEquals))
		}
		if flagAssertMin > 0 && n < flagAssertMin {
			assertFail("count", fmt.Sprintf("selector=%q has %d elements, want >=%d", selector, n, flagAssertMin))
		}
		if flagAssertMax > 0 && n > flagAssertMax {
			assertFail("count", fmt.Sprintf("selector=%q has %d elements, want <=%d", selector, n, flagAssertMax))
		}
		assertPass("count", fmt.Sprintf("%s → %d", selector, n))
	},
}

var assertNoConsoleErrorsCmd = &cobra.Command{
	Use:   "no-console-errors <url>",
	Short: "Navigate to URL and assert that no console error fired during load",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		targetURL := args[0]

		b, page := openPage()
		defer b.Close()

		errs, err := engine.CollectErrors(page, targetURL, "load", func(*rod.Page) error { return nil })
		if err != nil {
			assertFail("no-console-errors", err.Error())
		}

		var offending []engine.ErrorEntry
		for _, e := range errs {
			if e.Type == "console" && e.Level == "error" {
				offending = append(offending, e)
			}
		}
		if len(offending) > 0 {
			assertFail("no-console-errors", fmt.Sprintf("%d console error(s), first: %s", len(offending), offending[0].Message))
		}
		assertPass("no-console-errors", targetURL)
	},
}

var assertNoNetwork4xxCmd = &cobra.Command{
	Use:   "no-network-4xx <url>",
	Short: "Navigate to URL and assert that no request returned a 4xx status",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		targetURL := args[0]

		b, page := openPage()
		defer b.Close()

		errs, err := engine.CollectErrors(page, targetURL, "load", func(*rod.Page) error { return nil })
		if err != nil {
			assertFail("no-network-4xx", err.Error())
		}

		var offending []engine.ErrorEntry
		for _, e := range errs {
			if e.Type == "network" && e.Status >= 400 && e.Status < 500 {
				offending = append(offending, e)
			}
		}
		if len(offending) > 0 {
			assertFail("no-network-4xx", fmt.Sprintf("%d 4xx response(s), first: %d %s", len(offending), offending[0].Status, offending[0].Message))
		}
		assertPass("no-network-4xx", targetURL)
	},
}

// readBodyText returns document.body.innerText (plain text). Less brittle than
// the raw HTML since it skips script/style content.
func readBodyText(page *rod.Page) (string, error) {
	res, err := page.Eval(`() => document.body ? document.body.innerText : ""`)
	if err != nil {
		return "", err
	}
	return res.Value.Str(), nil
}

func init() {
	for _, c := range []*cobra.Command{assertTextCmd, assertURLCmd, assertTitleCmd} {
		c.Flags().BoolVar(&flagAssertRegex, "regex", false, "Treat the pattern as a Go regular expression")
	}
	assertCountCmd.Flags().IntVar(&flagAssertMin, "min", 0, "Minimum matches")
	assertCountCmd.Flags().IntVar(&flagAssertMax, "max", 0, "Maximum matches")
	assertCountCmd.Flags().IntVar(&flagAssertEquals, "equals", 0, "Exact match count")

	assertCmd.AddCommand(
		assertTextCmd,
		assertSelectorVisibleCmd,
		assertURLCmd,
		assertTitleCmd,
		assertCountCmd,
		assertNoConsoleErrorsCmd,
		assertNoNetwork4xxCmd,
	)
	rootCmd.AddCommand(assertCmd)
}
