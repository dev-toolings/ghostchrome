package engine

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func writeRulesFile(t *testing.T, rf RulesFile) string {
	t.Helper()
	data, err := json.Marshal(rf)
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.CreateTemp(t.TempDir(), "rules*.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(data); err != nil {
		t.Fatal(err)
	}
	f.Close()
	return f.Name()
}

func TestLoadRules_GlobMatch(t *testing.T) {
	rf := RulesFile{Rules: []RuleSpec{
		{
			Match:       "https://api.example.com/users/*",
			MatchType:   RuleMatchGlob,
			Status:      200,
			ContentType: "application/json",
			Body:        `{"users":[]}`,
		},
		{
			Match:     "https://api.example.com/posts/*",
			MatchType: RuleMatchGlob,
			Status:    404,
			Body:      `{"error":"not found"}`,
		},
	}}

	path := writeRulesFile(t, rf)
	rules, err := LoadRules(path, filepath.Dir(path))
	if err != nil {
		t.Fatalf("LoadRules: %v", err)
	}
	if len(rules) != 2 {
		t.Fatalf("expected 2 rules, got %d", len(rules))
	}

	r, ok := MatchRule(rules, "https://api.example.com/users/42", "GET")
	if !ok {
		t.Fatal("expected match for users/42")
	}
	if string(r.body) != `{"users":[]}` {
		t.Errorf("body mismatch: %s", r.body)
	}
	if r.status != 200 {
		t.Errorf("status mismatch: %d", r.status)
	}

	r2, ok2 := MatchRule(rules, "https://api.example.com/posts/1", "GET")
	if !ok2 {
		t.Fatal("expected match for posts/1")
	}
	if r2.status != 404 {
		t.Errorf("status mismatch: %d", r2.status)
	}

	_, noMatch := MatchRule(rules, "https://api.example.com/other/1", "GET")
	if noMatch {
		t.Error("unexpected match for /other/1")
	}
}

func TestLoadRules_RegexMatch(t *testing.T) {
	rf := RulesFile{Rules: []RuleSpec{
		{
			Match:     `^https://api\.example\.com/v\d+/items`,
			MatchType: RuleMatchRegex,
			Status:    200,
			Body:      `{"items":[]}`,
		},
	}}

	path := writeRulesFile(t, rf)
	rules, err := LoadRules(path, filepath.Dir(path))
	if err != nil {
		t.Fatalf("LoadRules: %v", err)
	}

	_, ok := MatchRule(rules, "https://api.example.com/v3/items", "GET")
	if !ok {
		t.Fatal("expected regex match")
	}

	_, noMatch := MatchRule(rules, "https://api.example.com/items", "GET")
	if noMatch {
		t.Error("unexpected regex match for /items (no version)")
	}
}

func TestLoadRules_ExactMatch(t *testing.T) {
	rf := RulesFile{Rules: []RuleSpec{
		{
			Match:     "https://api.example.com/config",
			MatchType: RuleMatchExact,
			Status:    200,
			Body:      `{"feature":true}`,
		},
	}}

	path := writeRulesFile(t, rf)
	rules, err := LoadRules(path, filepath.Dir(path))
	if err != nil {
		t.Fatalf("LoadRules: %v", err)
	}

	_, ok := MatchRule(rules, "https://api.example.com/config", "")
	if !ok {
		t.Fatal("expected exact match")
	}

	_, noMatch := MatchRule(rules, "https://api.example.com/config?x=1", "")
	if noMatch {
		t.Error("unexpected exact match with query string")
	}
}

func TestLoadRules_MethodFilter(t *testing.T) {
	rf := RulesFile{Rules: []RuleSpec{
		{
			Match:     "https://api.example.com/data",
			MatchType: RuleMatchExact,
			Method:    "POST",
			Status:    201,
			Body:      `{"ok":true}`,
		},
	}}

	path := writeRulesFile(t, rf)
	rules, err := LoadRules(path, filepath.Dir(path))
	if err != nil {
		t.Fatalf("LoadRules: %v", err)
	}

	_, ok := MatchRule(rules, "https://api.example.com/data", "POST")
	if !ok {
		t.Fatal("expected POST match")
	}

	_, noMatch := MatchRule(rules, "https://api.example.com/data", "GET")
	if noMatch {
		t.Error("unexpected GET match on POST-only rule")
	}
}

func TestLoadRules_BodyFile(t *testing.T) {
	dir := t.TempDir()
	fixtureContent := `{"loaded":true}`
	fixturePath := filepath.Join(dir, "fixture.json")
	if err := os.WriteFile(fixturePath, []byte(fixtureContent), 0o644); err != nil {
		t.Fatal(err)
	}

	rf := RulesFile{Rules: []RuleSpec{
		{
			Match:     "https://api.example.com/fixture",
			MatchType: RuleMatchExact,
			Status:    200,
			BodyFile:  fixturePath,
		},
	}}

	p := writeRulesFile(t, rf)
	rules, err := LoadRules(p, filepath.Dir(p))
	if err != nil {
		t.Fatalf("LoadRules: %v", err)
	}

	r, ok := MatchRule(rules, "https://api.example.com/fixture", "GET")
	if !ok {
		t.Fatal("expected match")
	}
	if string(r.body) != fixtureContent {
		t.Errorf("bodyFile content mismatch: got %q", r.body)
	}
}

func TestLoadRules_BodyFileWinsOverBody(t *testing.T) {
	dir := t.TempDir()
	fileContent := `{"from":"file"}`
	fixturePath := filepath.Join(dir, "override.json")
	if err := os.WriteFile(fixturePath, []byte(fileContent), 0o644); err != nil {
		t.Fatal(err)
	}

	rf := RulesFile{Rules: []RuleSpec{
		{
			Match:     "https://api.example.com/override",
			MatchType: RuleMatchExact,
			Status:    200,
			Body:      `{"from":"inline"}`,
			BodyFile:  fixturePath,
		},
	}}

	p := writeRulesFile(t, rf)
	rules, err := LoadRules(p, filepath.Dir(p))
	if err != nil {
		t.Fatalf("LoadRules: %v", err)
	}

	r, ok := MatchRule(rules, "https://api.example.com/override", "GET")
	if !ok {
		t.Fatal("expected match")
	}
	if string(r.body) != fileContent {
		t.Errorf("expected bodyFile to win, got %q", r.body)
	}
}

func TestLoadRules_InvalidRegexFails(t *testing.T) {
	rf := RulesFile{Rules: []RuleSpec{
		{Match: "[invalid", MatchType: RuleMatchRegex},
	}}
	p := writeRulesFile(t, rf)
	_, err := LoadRules(p, filepath.Dir(p))
	if err == nil {
		t.Fatal("expected error for invalid regex")
	}
}

func TestLoadRules_MissingMatchFails(t *testing.T) {
	rf := RulesFile{Rules: []RuleSpec{
		{Match: "", Status: 200},
	}}
	p := writeRulesFile(t, rf)
	_, err := LoadRules(p, filepath.Dir(p))
	if err == nil {
		t.Fatal("expected error for empty match")
	}
}

func TestLoadRules_MissingBodyFileFails(t *testing.T) {
	rf := RulesFile{Rules: []RuleSpec{
		{Match: "https://x.com/", MatchType: RuleMatchExact, BodyFile: "/nonexistent/path.json"},
	}}
	p := writeRulesFile(t, rf)
	_, err := LoadRules(p, filepath.Dir(p))
	if err == nil {
		t.Fatal("expected error for missing bodyFile")
	}
}

func TestLoadRules_CustomHeaders(t *testing.T) {
	rf := RulesFile{Rules: []RuleSpec{
		{
			Match:     "https://api.example.com/hdr",
			MatchType: RuleMatchExact,
			Status:    200,
			Body:      "ok",
			Headers:   map[string]string{"X-Mocked": "1", "Cache-Control": "no-store"},
		},
	}}

	p := writeRulesFile(t, rf)
	rules, err := LoadRules(p, filepath.Dir(p))
	if err != nil {
		t.Fatalf("LoadRules: %v", err)
	}

	r, ok := MatchRule(rules, "https://api.example.com/hdr", "GET")
	if !ok {
		t.Fatal("expected match")
	}
	if r.headers["X-Mocked"] != "1" {
		t.Errorf("X-Mocked header mismatch: %v", r.headers)
	}
	if r.headers["Cache-Control"] != "no-store" {
		t.Errorf("Cache-Control header mismatch: %v", r.headers)
	}
}
