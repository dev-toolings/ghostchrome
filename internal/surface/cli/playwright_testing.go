package cli

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/dev-toolings/ghostchrome/internal/core/engine"
	"github.com/go-rod/rod"
	"github.com/spf13/cobra"
)

var (
	verifyElementLocator LocatorFlags
	verifyValueLocator   LocatorFlags
	generateLocatorFlags LocatorFlags
	flagTestingURL       string
)

var verifyElementVisibleCmd = &cobra.Command{
	Use:   "verify-element-visible [role] [name]",
	Short: "Assert element is visible by role and name",
	Long: `Assert an element is visible by semantic locator.

Use positional role/name, or the shared --by-role / --by-name / --by-label /
--by-text flags. This maps Playwright CLI's testing capability to
ghostchrome's current accessibility/text locator engine.`,
	Args: cobra.MaximumNArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		loc := verifyElementLocator.ToLocator()
		if len(args) > 0 {
			loc.Role = args[0]
		}
		if len(args) > 1 {
			loc.Name = args[1]
		}
		if loc.IsEmpty() {
			assertFail("verify-element-visible", "provide role/name or one of --by-role / --by-name / --by-label / --by-text")
		}

		b, page := openPage()
		defer b.Close()
		navigateIfRequested(page, flagTestingURL, "load")

		if _, err := engine.WaitForLocator(page, loc, engine.StateVisible, 0); err != nil {
			assertFail("verify-element-visible", err.Error())
		}
		assertPass("verify-element-visible", locatorSummary(loc))
	},
}

var verifyTextVisibleCmd = &cobra.Command{
	Use:   "verify-text-visible <text>",
	Short: "Assert text is visible",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		text := args[0]

		b, page := openPage()
		defer b.Close()
		navigateIfRequested(page, flagTestingURL, "load")

		body, err := readBodyText(page)
		if err != nil {
			assertFail("verify-text-visible", err.Error())
		}
		if !strings.Contains(strings.ToLower(body), strings.ToLower(text)) {
			assertFail("verify-text-visible", fmt.Sprintf("visible text does not contain %q", text))
		}
		assertPass("verify-text-visible", strconv.Quote(text))
	},
}

var verifyListVisibleCmd = &cobra.Command{
	Use:   "verify-list-visible <item> [item...]",
	Short: "Assert a visible list contains all items",
	Args:  cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		b, page := openPage()
		defer b.Close()
		navigateIfRequested(page, flagTestingURL, "load")

		ok, err := visibleListContains(page, args)
		if err != nil {
			assertFail("verify-list-visible", err.Error())
		}
		if !ok {
			assertFail("verify-list-visible", fmt.Sprintf("no visible list contains all items: %v", args))
		}
		assertPass("verify-list-visible", strings.Join(args, ", "))
	},
}

var verifyValueCmd = &cobra.Command{
	Use:   "verify-value [target] <expected>",
	Short: "Assert form field value",
	Long: `Assert a form field value.

Target can be an @ref/eN ref, a CSS selector, or omitted when --by-role /
--by-name / --by-label / --by-text locators are provided.`,
	Args: cobra.RangeArgs(1, 2),
	Run: func(cmd *cobra.Command, args []string) {
		target := ""
		expected := args[0]
		if len(args) == 2 {
			target = args[0]
			expected = args[1]
		}

		b, page := openPage()
		defer b.Close()
		navigateIfRequested(page, flagTestingURL, "load")

		el, err := resolveVerifyValueElement(b, page, target, verifyValueLocator)
		if err != nil {
			assertFail("verify-value", err.Error())
		}
		got, err := elementValue(el)
		if err != nil {
			assertFail("verify-value", err.Error())
		}
		if got != expected {
			assertFail("verify-value", fmt.Sprintf("value=%q, want %q", got, expected))
		}
		assertPass("verify-value", fmt.Sprintf("%q", expected))
	},
}

var generateLocatorCmd = &cobra.Command{
	Use:   "generate-locator [ref|text]",
	Short: "Generate Playwright locator for test code",
	Long: `Generate a Playwright locator suggestion for test code.

When given @ref/eN, ghostchrome reads the current accessibility snapshot and
generates getByRole when role/name are available. With --by-* flags, it renders
the corresponding Playwright locator directly. With plain text, it emits
getByText.`,
	Args: cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		locator, err := generatePlaywrightLocator(nil, nil, args, generateLocatorFlags)
		if err == errGenerateLocatorNeedsPage {
			b, page := openPage()
			defer b.Close()
			navigateIfRequested(page, flagTestingURL, "load")
			locator, err = generatePlaywrightLocator(b, page, args, generateLocatorFlags)
		}
		if err != nil {
			exitErr("generate-locator", err)
		}
		output(map[string]string{"locator": locator}, locator)
	},
}

func resolveVerifyValueElement(b *engine.Browser, page *rod.Page, target string, flags LocatorFlags) (*rod.Element, error) {
	if flags.Any() {
		return engine.ResolveByLocator(page, flags.ToLocator())
	}
	if target == "" {
		return nil, fmt.Errorf("target required unless --by-* locator flags are set")
	}
	if isSnapshotRef(target) {
		snapshot := ensureSnapshot(b, page, "", "load", engine.LevelSkeleton)
		return engine.ResolveRef(page, engine.InternalRef(target), snapshot)
	}
	return page.Element(target)
}

func elementValue(el *rod.Element) (string, error) {
	res, err := el.Eval(`() => {
		if ('value' in this) return String(this.value ?? '');
		return String(this.getAttribute('value') ?? this.textContent ?? '');
	}`)
	if err != nil {
		return "", err
	}
	if res == nil {
		return "", nil
	}
	return res.Value.Str(), nil
}

func visibleListContains(page *rod.Page, items []string) (bool, error) {
	payload, err := json.Marshal(items)
	if err != nil {
		return false, err
	}
	script := fmt.Sprintf(`() => {
		const items = %s.map(item => String(item).toLowerCase());
		const lists = Array.from(document.querySelectorAll('ul, ol, [role="list"]'));
		return lists.some(list => {
			const style = getComputedStyle(list);
			const rect = list.getBoundingClientRect();
			if (style.visibility === 'hidden' || style.display === 'none' || rect.width === 0 || rect.height === 0) return false;
			const text = (list.innerText || list.textContent || '').toLowerCase();
			return items.every(item => text.includes(item));
		});
	}`, string(payload))
	res, err := page.Eval(script)
	if err != nil {
		return false, err
	}
	return res != nil && res.Value.Val() == true, nil
}

var errGenerateLocatorNeedsPage = fmt.Errorf("page required to resolve ref")

func generatePlaywrightLocator(b *engine.Browser, page *rod.Page, args []string, flags LocatorFlags) (string, error) {
	if flags.Any() {
		return locatorFromEngineLocator(flags.ToLocator()), nil
	}
	if len(args) == 0 {
		return "", fmt.Errorf("provide @ref/eN, text, or --by-* locator flags")
	}
	value := args[0]
	if isSnapshotRef(value) {
		if b == nil || page == nil {
			return "", errGenerateLocatorNeedsPage
		}
		result := snapshotPage(b, page, engine.LevelSkeleton)
		ref := engine.InternalRef(value)
		node, ok := result.Refs[ref]
		if !ok {
			return "", fmt.Errorf("ref %s not found in current snapshot", value)
		}
		if node.Role != "" && node.Name != "" {
			return fmt.Sprintf("page.getByRole(%s, { name: %s })", jsString(node.Role), jsString(node.Name)), nil
		}
		if node.Name != "" {
			return fmt.Sprintf("page.getByText(%s)", jsString(node.Name)), nil
		}
		return "", fmt.Errorf("ref %s has no role/name suitable for a Playwright locator", value)
	}
	return fmt.Sprintf("page.getByText(%s)", jsString(value)), nil
}

func locatorFromEngineLocator(loc engine.Locator) string {
	if loc.Text != "" {
		return fmt.Sprintf("page.getByText(%s)", jsString(loc.Text))
	}
	role := loc.Role
	name := loc.Name
	if name == "" {
		name = loc.Label
	}
	if role != "" && name != "" {
		return fmt.Sprintf("page.getByRole(%s, { name: %s })", jsString(role), jsString(name))
	}
	if role != "" {
		return fmt.Sprintf("page.getByRole(%s)", jsString(role))
	}
	if name != "" {
		return fmt.Sprintf("page.getByLabel(%s)", jsString(name))
	}
	return "page.locator('body')"
}

func locatorSummary(loc engine.Locator) string {
	parts := make([]string, 0, 4)
	if loc.Role != "" {
		parts = append(parts, "role="+loc.Role)
	}
	if loc.Name != "" {
		parts = append(parts, "name="+loc.Name)
	}
	if loc.Label != "" {
		parts = append(parts, "label="+loc.Label)
	}
	if loc.Text != "" {
		parts = append(parts, "text="+loc.Text)
	}
	return strings.Join(parts, " ")
}

func jsString(value string) string {
	return strconv.Quote(value)
}

func init() {
	verifyElementLocator.RegisterOn(verifyElementVisibleCmd)
	verifyValueLocator.RegisterOn(verifyValueCmd)
	generateLocatorFlags.RegisterOn(generateLocatorCmd)
	for _, c := range []*cobra.Command{
		verifyElementVisibleCmd,
		verifyTextVisibleCmd,
		verifyListVisibleCmd,
		verifyValueCmd,
		generateLocatorCmd,
	} {
		c.Flags().StringVar(&flagTestingURL, "url", "", "Navigate to this URL before running the testing command")
	}

	rootCmd.AddCommand(
		verifyElementVisibleCmd,
		verifyTextVisibleCmd,
		verifyListVisibleCmd,
		verifyValueCmd,
		generateLocatorCmd,
	)
	commandGroups["verify-element-visible"] = "util"
	commandGroups["verify-text-visible"] = "util"
	commandGroups["verify-list-visible"] = "util"
	commandGroups["verify-value"] = "util"
	commandGroups["generate-locator"] = "util"
}
