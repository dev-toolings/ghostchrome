package engine

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

// RuleMatchType controls how the match field is interpreted.
type RuleMatchType string

const (
	RuleMatchGlob  RuleMatchType = "glob"
	RuleMatchRegex RuleMatchType = "regex"
	RuleMatchExact RuleMatchType = "exact"
)

// RuleSpec is the JSON schema for one entry in the rules file.
type RuleSpec struct {
	Match       string            `json:"match"`
	MatchType   RuleMatchType     `json:"matchType"`
	Method      string            `json:"method"`
	Status      int               `json:"status"`
	ContentType string            `json:"contentType"`
	Headers     map[string]string `json:"headers"`
	Body        string            `json:"body"`
	BodyFile    string            `json:"bodyFile"`
}

// RulesFile is the top-level JSON structure.
type RulesFile struct {
	Rules []RuleSpec `json:"rules"`
}

// compiledRule is a RuleSpec with the pattern pre-compiled and the body loaded.
type compiledRule struct {
	method      string
	status      int
	contentType string
	headers     map[string]string
	body        []byte
	matchFn     func(url string) bool
}

// LoadRules parses path, validates each rule, loads bodyFile contents, and
// compiles match patterns. baseDir is used to resolve relative bodyFile paths.
func LoadRules(rulesPath, baseDir string) ([]compiledRule, error) {
	data, err := os.ReadFile(rulesPath)
	if err != nil {
		return nil, fmt.Errorf("rules: read %q: %w", rulesPath, err)
	}

	var rf RulesFile
	if err := json.Unmarshal(data, &rf); err != nil {
		return nil, fmt.Errorf("rules: parse %q: %w", rulesPath, err)
	}

	out := make([]compiledRule, 0, len(rf.Rules))
	for i, r := range rf.Rules {
		if r.Match == "" {
			return nil, fmt.Errorf("rules[%d]: match is required", i)
		}

		matchFn, err := compileMatch(r.Match, r.MatchType)
		if err != nil {
			return nil, fmt.Errorf("rules[%d]: %w", i, err)
		}

		body, err := resolveBody(r, baseDir, i)
		if err != nil {
			return nil, err
		}

		status := r.Status
		if status == 0 {
			status = 200
		}
		ct := r.ContentType
		if ct == "" {
			ct = detectContentType(body, r.Match)
		}

		out = append(out, compiledRule{
			method:      strings.ToUpper(r.Method),
			status:      status,
			contentType: ct,
			headers:     r.Headers,
			body:        body,
			matchFn:     matchFn,
		})
	}
	return out, nil
}

// MatchRule returns the first compiledRule whose pattern and method match url+method.
// method may be empty to skip method filtering.
func MatchRule(rules []compiledRule, url, method string) (*compiledRule, bool) {
	for i := range rules {
		r := &rules[i]
		if r.method != "" && r.method != strings.ToUpper(method) {
			continue
		}
		if r.matchFn(url) {
			return r, true
		}
	}
	return nil, false
}

func compileMatch(pattern string, mt RuleMatchType) (func(string) bool, error) {
	switch mt {
	case "", RuleMatchGlob:
		// Validate at compile time so LoadRules fails fast on bad patterns.
		if _, err := path.Match(pattern, ""); err != nil {
			return nil, fmt.Errorf("invalid glob %q: %w", pattern, err)
		}
		return func(url string) bool {
			ok, _ := path.Match(pattern, url)
			return ok
		}, nil

	case RuleMatchRegex:
		re, err := regexp.Compile(pattern)
		if err != nil {
			return nil, fmt.Errorf("invalid regex %q: %w", pattern, err)
		}
		return func(url string) bool { return re.MatchString(url) }, nil

	case RuleMatchExact:
		return func(url string) bool { return url == pattern }, nil

	default:
		return nil, fmt.Errorf("unknown matchType %q (want glob|regex|exact)", mt)
	}
}

func resolveBody(r RuleSpec, baseDir string, idx int) ([]byte, error) {
	if r.BodyFile != "" {
		p := r.BodyFile
		if !filepath.IsAbs(p) {
			p = filepath.Join(baseDir, p)
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return nil, fmt.Errorf("rules[%d]: bodyFile %q: %w", idx, r.BodyFile, err)
		}
		return data, nil
	}
	return []byte(r.Body), nil
}
