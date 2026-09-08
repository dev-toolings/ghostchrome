package report

import (
	"math"
	"strings"
	"testing"
)

func TestBuildReportAndScoreboard(t *testing.T) {
	suite := Suite{
		Name: "agent-bakeoff",
		Tasks: []Task{
			{ID: "inspect-home", Title: "Inspect home"},
			{ID: "find-price", Title: "Find price"},
		},
		Runs: []Run{
			{TaskID: "inspect-home", Agent: "codex", Browser: "ghostchrome", Success: true, DurationMs: 1800, ToolCalls: 2, InputTokens: 900, OutputTokens: 200},
			{TaskID: "find-price", Agent: "codex", Browser: "ghostchrome", Success: true, DurationMs: 3200, ToolCalls: 4, InputTokens: 1300, OutputTokens: 250},
			{TaskID: "inspect-home", Agent: "codex", Browser: "playwright", Success: true, DurationMs: 2600, ToolCalls: 3, InputTokens: 1700, OutputTokens: 500},
			{TaskID: "find-price", Agent: "codex", Browser: "playwright", Success: false, DurationMs: 5400, ToolCalls: 6, InputTokens: 2200, OutputTokens: 400},
			{TaskID: "inspect-home", Agent: "claude-code", Browser: "ghostchrome", Success: true, DurationMs: 2100, ToolCalls: 2, InputChars: 4400, OutputChars: 1200},
			{TaskID: "find-price", Agent: "claude-code", Browser: "ghostchrome", Success: true, DurationMs: 3500, ToolCalls: 4, InputChars: 5200, OutputChars: 1600},
			{TaskID: "inspect-home", Agent: "claude-code", Browser: "playwright", Success: true, DurationMs: 2900, ToolCalls: 3, InputTokens: 1800, OutputTokens: 450},
			{TaskID: "find-price", Agent: "claude-code", Browser: "playwright", Success: true, DurationMs: 4700, ToolCalls: 5, InputTokens: 2100, OutputTokens: 550},
		},
	}

	report, err := Build(suite)
	if err != nil {
		t.Fatalf("build report: %v", err)
	}

	if got, want := report.Overview.CompetitorCount, 4; got != want {
		t.Fatalf("competitor count = %d, want %d", got, want)
	}

	if report.Competitors[0].Key != "codex + ghostchrome" {
		t.Fatalf("top competitor = %q, want codex + ghostchrome", report.Competitors[0].Key)
	}

	for _, task := range report.TaskSummaries {
		for _, competitor := range task.Competitors {
			if math.IsNaN(competitor.Score) {
				t.Fatalf("task %s competitor %s has NaN score", task.TaskID, competitor.Key)
			}
		}
	}

	md := Markdown(report)
	if !strings.Contains(md, "## Overall Scoreboard") {
		t.Fatalf("markdown missing scoreboard:\n%s", md)
	}
	if !strings.Contains(md, "codex + ghostchrome") {
		t.Fatalf("markdown missing competitor row:\n%s", md)
	}
}

func TestResolveTokensFallsBackToChars(t *testing.T) {
	total, source, err := resolveTokens(Run{
		RunID:       "estimate",
		InputChars:  401,
		OutputChars: 399,
	})
	if err != nil {
		t.Fatalf("resolve tokens: %v", err)
	}

	if total != 201 {
		t.Fatalf("total tokens = %d, want 201", total)
	}
	if source != tokenSourceEstimated {
		t.Fatalf("source = %q, want estimated", source)
	}
}
