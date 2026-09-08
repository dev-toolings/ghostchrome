package cli

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/dev-toolings/ghostchrome/internal/core/engine"
	"github.com/spf13/cobra"
)

const findContextLines = 3

var flagFindRegex string

type findMatch struct {
	Line    int      `json:"line"`
	Text    string   `json:"text"`
	Context []string `json:"context"`
}

type findResult struct {
	Query   string      `json:"query"`
	Regex   bool        `json:"regex"`
	Matches []findMatch `json:"matches"`
}

var findCmd = &cobra.Command{
	Use:   "find [text]",
	Short: "Search the current page snapshot for text or a regular expression",
	Long: `Searches the current page snapshot and returns matching nodes with three lines of context.
Plain-text searches are case-insensitive. Provide either text or --regex, not both.

Examples:
  ghostchrome find "Add to cart"
  ghostchrome find --regex '\\$[0-9]+\\.[0-9]{2}'`,
	Args: validateFindArgs,
	Run: func(cmd *cobra.Command, args []string) {
		query := flagFindRegex
		isRegex := query != ""
		if !isRegex {
			query = args[0]
		}

		matcher, err := newFindMatcher(query, isRegex)
		if err != nil {
			exitErr("find", err)
		}

		b, page := openPage()
		defer b.Close()

		// Refresh content so prior skeleton snapshots and asynchronous changes
		// cannot hide text from this search.
		result := snapshotPage(b, page, engine.LevelContent)
		matches := findSnapshotMatches(engine.FormatPlaywrightSnapshot(result), matcher)
		output(findResult{Query: query, Regex: isRegex, Matches: matches}, formatFindMatches(matches))
	},
}

func validateFindArgs(_ *cobra.Command, args []string) error {
	if flagFindRegex != "" && len(args) > 0 {
		return fmt.Errorf("provide either text or --regex, not both")
	}
	if flagFindRegex == "" && len(args) == 0 {
		return fmt.Errorf("provide text or --regex")
	}
	if len(args) > 1 {
		return fmt.Errorf("accepts at most one text argument")
	}
	return nil
}

func newFindMatcher(query string, isRegex bool) (func(string) bool, error) {
	if !isRegex {
		needle := strings.ToLower(query)
		return func(line string) bool { return strings.Contains(strings.ToLower(line), needle) }, nil
	}
	re, err := regexp.Compile(query)
	if err != nil {
		return nil, fmt.Errorf("invalid --regex %q: %w", query, err)
	}
	return re.MatchString, nil
}

func findSnapshotMatches(snapshot string, matches func(string) bool) []findMatch {
	if snapshot == "" {
		return []findMatch{}
	}
	lines := strings.Split(snapshot, "\n")
	result := make([]findMatch, 0)
	for i, line := range lines {
		if !matches(line) {
			continue
		}
		start := max(0, i-findContextLines)
		end := min(len(lines), i+findContextLines+1)
		result = append(result, findMatch{
			Line:    i + 1,
			Text:    line,
			Context: lines[start:end],
		})
	}
	return result
}

func formatFindMatches(matches []findMatch) string {
	if len(matches) == 0 {
		return "No matching nodes."
	}

	var b strings.Builder
	for i, match := range matches {
		if i > 0 {
			b.WriteString("\n--\n")
		}
		for _, line := range match.Context {
			b.WriteString(line)
			b.WriteByte('\n')
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func init() {
	findCmd.Flags().StringVar(&flagFindRegex, "regex", "", "Regular expression to search for (instead of text)")
	rootCmd.AddCommand(findCmd)
}
