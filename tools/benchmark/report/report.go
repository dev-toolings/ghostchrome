package report

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"sort"
	"strings"
	"time"
)

const charsPerToken = 4.0

type Suite struct {
	Name        string       `json:"name"`
	Description string       `json:"description,omitempty"`
	Weights     ScoreWeights `json:"weights,omitempty"`
	Tasks       []Task       `json:"tasks,omitempty"`
	Runs        []Run        `json:"runs"`
}

type Task struct {
	ID          string `json:"id"`
	Title       string `json:"title,omitempty"`
	Group       string `json:"group,omitempty"`
	Description string `json:"description,omitempty"`
}

type ScoreWeights struct {
	Success   float64 `json:"success"`
	Tokens    float64 `json:"tokens"`
	Duration  float64 `json:"duration"`
	ToolCalls float64 `json:"tool_calls"`
}

type Run struct {
	TaskID       string `json:"task_id"`
	Agent        string `json:"agent"`
	Browser      string `json:"browser"`
	RunID        string `json:"run_id,omitempty"`
	Success      bool   `json:"success"`
	DurationMs   int64  `json:"duration_ms"`
	ToolCalls    int    `json:"tool_calls"`
	InputTokens  int    `json:"input_tokens,omitempty"`
	OutputTokens int    `json:"output_tokens,omitempty"`
	InputChars   int    `json:"input_chars,omitempty"`
	OutputChars  int    `json:"output_chars,omitempty"`
	Notes        string `json:"notes,omitempty"`
}

type Report struct {
	SuiteName     string              `json:"suite_name"`
	Description   string              `json:"description,omitempty"`
	GeneratedAt   time.Time           `json:"generated_at"`
	Weights       ScoreWeights        `json:"weights"`
	Tasks         []Task              `json:"tasks,omitempty"`
	Overview      Overview            `json:"overview"`
	Competitors   []CompetitorSummary `json:"competitors"`
	TaskSummaries []TaskSummary       `json:"task_summaries"`
}

type Overview struct {
	RunCount        int `json:"run_count"`
	TaskCount       int `json:"task_count"`
	ExactUsageRuns  int `json:"exact_usage_runs"`
	MixedUsageRuns  int `json:"mixed_usage_runs"`
	EstimatedRuns   int `json:"estimated_usage_runs"`
	InvalidRuns     int `json:"invalid_runs"`
	SuccessfulRuns  int `json:"successful_runs"`
	FailedRuns      int `json:"failed_runs"`
	CompetitorCount int `json:"competitor_count"`
}

type CompetitorSummary struct {
	Key              string       `json:"key"`
	Agent            string       `json:"agent"`
	Browser          string       `json:"browser"`
	Runs             int          `json:"runs"`
	Successes        int          `json:"successes"`
	Failures         int          `json:"failures"`
	SuccessRate      float64      `json:"success_rate"`
	TotalTokens      int          `json:"total_tokens"`
	TokensPerSuccess float64      `json:"tokens_per_success"`
	MedianDurationMs float64      `json:"median_duration_ms"`
	P95DurationMs    float64      `json:"p95_duration_ms"`
	MedianToolCalls  float64      `json:"median_tool_calls"`
	UsageBreakdown   UsageSources `json:"usage_breakdown"`
	Score            float64      `json:"score"`
}

type UsageSources struct {
	Exact     int `json:"exact"`
	Mixed     int `json:"mixed"`
	Estimated int `json:"estimated"`
}

type TaskSummary struct {
	TaskID      string              `json:"task_id"`
	Title       string              `json:"title"`
	Group       string              `json:"group,omitempty"`
	Competitors []CompetitorSummary `json:"competitors"`
}

type tokenSource string

const (
	tokenSourceExact     tokenSource = "exact"
	tokenSourceMixed     tokenSource = "mixed"
	tokenSourceEstimated tokenSource = "estimated"
)

type analyzedRun struct {
	Run
	totalTokens int
	source      tokenSource
}

func DefaultWeights() ScoreWeights {
	return ScoreWeights{
		Success:   50,
		Tokens:    25,
		Duration:  15,
		ToolCalls: 10,
	}
}

func Build(suite Suite) (Report, error) {
	if len(suite.Runs) == 0 {
		return Report{}, fmt.Errorf("suite has no runs")
	}

	weights := suite.Weights
	if weights == (ScoreWeights{}) {
		weights = DefaultWeights()
	}

	taskByID := map[string]Task{}
	for _, task := range suite.Tasks {
		taskByID[task.ID] = task
	}

	analyzed := make([]analyzedRun, 0, len(suite.Runs))
	overview := Overview{RunCount: len(suite.Runs)}
	taskSet := map[string]struct{}{}

	for _, run := range suite.Runs {
		if strings.TrimSpace(run.TaskID) == "" {
			overview.InvalidRuns++
			continue
		}
		if strings.TrimSpace(run.Agent) == "" || strings.TrimSpace(run.Browser) == "" {
			overview.InvalidRuns++
			continue
		}
		totalTokens, source, err := resolveTokens(run)
		if err != nil {
			overview.InvalidRuns++
			continue
		}

		taskSet[run.TaskID] = struct{}{}
		if run.Success {
			overview.SuccessfulRuns++
		} else {
			overview.FailedRuns++
		}

		switch source {
		case tokenSourceExact:
			overview.ExactUsageRuns++
		case tokenSourceMixed:
			overview.MixedUsageRuns++
		case tokenSourceEstimated:
			overview.EstimatedRuns++
		}

		analyzed = append(analyzed, analyzedRun{
			Run:         run,
			totalTokens: totalTokens,
			source:      source,
		})
	}

	if len(analyzed) == 0 {
		return Report{}, fmt.Errorf("suite has no valid runs")
	}

	overview.TaskCount = len(taskSet)

	byCompetitor := map[string][]analyzedRun{}
	byTask := map[string][]analyzedRun{}
	for _, run := range analyzed {
		key := competitorKey(run.Agent, run.Browser)
		byCompetitor[key] = append(byCompetitor[key], run)
		byTask[run.TaskID] = append(byTask[run.TaskID], run)
	}

	overview.CompetitorCount = len(byCompetitor)

	competitors := make([]CompetitorSummary, 0, len(byCompetitor))
	for _, runs := range byCompetitor {
		competitors = append(competitors, summarizeRuns(runs))
	}
	assignScores(competitors, weights)
	sortCompetitors(competitors)

	taskSummaries := make([]TaskSummary, 0, len(byTask))
	for taskID, runs := range byTask {
		taskMeta := taskByID[taskID]
		summariesByCompetitor := map[string][]analyzedRun{}
		for _, run := range runs {
			key := competitorKey(run.Agent, run.Browser)
			summariesByCompetitor[key] = append(summariesByCompetitor[key], run)
		}

		summaries := make([]CompetitorSummary, 0, len(summariesByCompetitor))
		for _, competitorRuns := range summariesByCompetitor {
			summaries = append(summaries, summarizeRuns(competitorRuns))
		}
		assignScores(summaries, weights)
		sortCompetitors(summaries)

		title := taskMeta.Title
		if title == "" {
			title = taskID
		}
		taskSummaries = append(taskSummaries, TaskSummary{
			TaskID:      taskID,
			Title:       title,
			Group:       taskMeta.Group,
			Competitors: summaries,
		})
	}
	sort.Slice(taskSummaries, func(i, j int) bool {
		return taskSummaries[i].TaskID < taskSummaries[j].TaskID
	})

	return Report{
		SuiteName:     suite.Name,
		Description:   suite.Description,
		GeneratedAt:   time.Now().UTC(),
		Weights:       weights,
		Tasks:         suite.Tasks,
		Overview:      overview,
		Competitors:   competitors,
		TaskSummaries: taskSummaries,
	}, nil
}

func Markdown(rep Report) string {
	var buf bytes.Buffer

	title := rep.SuiteName
	if strings.TrimSpace(title) == "" {
		title = "Benchmark Report"
	}

	fmt.Fprintf(&buf, "# %s\n\n", title)
	if rep.Description != "" {
		fmt.Fprintf(&buf, "%s\n\n", rep.Description)
	}

	fmt.Fprintf(&buf, "- Generated: %s\n", rep.GeneratedAt.Format(time.RFC3339))
	fmt.Fprintf(&buf, "- Runs: %d valid, %d invalid\n", rep.Overview.RunCount-rep.Overview.InvalidRuns, rep.Overview.InvalidRuns)
	fmt.Fprintf(&buf, "- Tasks: %d\n", rep.Overview.TaskCount)
	fmt.Fprintf(&buf, "- Usage sources: %d exact, %d mixed, %d estimated\n\n", rep.Overview.ExactUsageRuns, rep.Overview.MixedUsageRuns, rep.Overview.EstimatedRuns)

	buf.WriteString("## Overall Scoreboard\n\n")
	buf.WriteString("| Competitor | Success | Tokens / success | Median duration | Median tool calls | Score |\n")
	buf.WriteString("|---|---:|---:|---:|---:|---:|\n")
	for _, competitor := range rep.Competitors {
		fmt.Fprintf(&buf, "| %s | %.1f%% | %.0f | %.0fms | %.1f | %.1f |\n",
			competitor.Key,
			competitor.SuccessRate,
			competitor.TokensPerSuccess,
			competitor.MedianDurationMs,
			competitor.MedianToolCalls,
			competitor.Score,
		)
	}

	winnersByAgent := winnersByAgent(rep.Competitors)
	if len(winnersByAgent) > 0 {
		buf.WriteString("\n## Winners By Agent\n\n")
		buf.WriteString("| Agent | Winner | Score |\n")
		buf.WriteString("|---|---|---:|\n")
		agents := make([]string, 0, len(winnersByAgent))
		for agent := range winnersByAgent {
			agents = append(agents, agent)
		}
		sort.Strings(agents)
		for _, agent := range agents {
			winner := winnersByAgent[agent]
			fmt.Fprintf(&buf, "| %s | %s | %.1f |\n", agent, winner.Key, winner.Score)
		}
	}

	if len(rep.TaskSummaries) > 0 {
		buf.WriteString("\n## Per Task\n")
		for _, task := range rep.TaskSummaries {
			fmt.Fprintf(&buf, "\n### %s\n\n", task.Title)
			buf.WriteString("| Competitor | Success | Tokens / success | Median duration | Score |\n")
			buf.WriteString("|---|---:|---:|---:|---:|\n")
			for _, competitor := range task.Competitors {
				fmt.Fprintf(&buf, "| %s | %.1f%% | %.0f | %.0fms | %.1f |\n",
					competitor.Key,
					competitor.SuccessRate,
					competitor.TokensPerSuccess,
					competitor.MedianDurationMs,
					competitor.Score,
				)
			}
		}
	}

	return strings.TrimSpace(buf.String()) + "\n"
}

func JSON(rep Report) ([]byte, error) {
	sanitized := rep
	for i := range sanitized.Competitors {
		sanitizeCompetitor(&sanitized.Competitors[i])
	}
	for i := range sanitized.TaskSummaries {
		for j := range sanitized.TaskSummaries[i].Competitors {
			sanitizeCompetitor(&sanitized.TaskSummaries[i].Competitors[j])
		}
	}
	return json.MarshalIndent(sanitized, "", "  ")
}

func resolveTokens(run Run) (int, tokenSource, error) {
	var (
		total int
		exact bool
		est   bool
	)

	if run.InputTokens > 0 {
		total += run.InputTokens
		exact = true
	} else if run.InputChars > 0 {
		total += estimateTokens(run.InputChars)
		est = true
	}

	if run.OutputTokens > 0 {
		total += run.OutputTokens
		exact = true
	} else if run.OutputChars > 0 {
		total += estimateTokens(run.OutputChars)
		est = true
	}

	if total == 0 {
		return 0, "", fmt.Errorf("run %q has no token or char usage", run.RunID)
	}

	switch {
	case exact && est:
		return total, tokenSourceMixed, nil
	case exact:
		return total, tokenSourceExact, nil
	default:
		return total, tokenSourceEstimated, nil
	}
}

func estimateTokens(chars int) int {
	return int(math.Ceil(float64(chars) / charsPerToken))
}

func summarizeRuns(runs []analyzedRun) CompetitorSummary {
	first := runs[0]
	summary := CompetitorSummary{
		Key:     competitorKey(first.Agent, first.Browser),
		Agent:   first.Agent,
		Browser: first.Browser,
		Runs:    len(runs),
	}

	durations := make([]float64, 0, len(runs))
	toolCalls := make([]float64, 0, len(runs))

	for _, run := range runs {
		summary.TotalTokens += run.totalTokens
		switch run.source {
		case tokenSourceExact:
			summary.UsageBreakdown.Exact++
		case tokenSourceMixed:
			summary.UsageBreakdown.Mixed++
		case tokenSourceEstimated:
			summary.UsageBreakdown.Estimated++
		}

		if run.Success {
			summary.Successes++
			durations = append(durations, float64(run.DurationMs))
			toolCalls = append(toolCalls, float64(run.ToolCalls))
		}
	}

	summary.Failures = summary.Runs - summary.Successes
	summary.SuccessRate = ratio(summary.Successes, summary.Runs) * 100
	if summary.Successes > 0 {
		summary.TokensPerSuccess = float64(summary.TotalTokens) / float64(summary.Successes)
		summary.MedianDurationMs = median(durations)
		summary.P95DurationMs = percentile(durations, 95)
		summary.MedianToolCalls = median(toolCalls)
	} else {
		summary.TokensPerSuccess = math.Inf(1)
		summary.MedianDurationMs = math.Inf(1)
		summary.P95DurationMs = math.Inf(1)
		summary.MedianToolCalls = math.Inf(1)
	}

	return summary
}

func assignScores(summaries []CompetitorSummary, weights ScoreWeights) {
	successes := make([]float64, 0, len(summaries))
	tokens := make([]float64, 0, len(summaries))
	durations := make([]float64, 0, len(summaries))
	steps := make([]float64, 0, len(summaries))
	for _, summary := range summaries {
		successes = append(successes, summary.SuccessRate)
		tokens = append(tokens, summary.TokensPerSuccess)
		durations = append(durations, summary.MedianDurationMs)
		steps = append(steps, summary.MedianToolCalls)
	}

	successMin, successMax := minMax(successes)
	tokenMin, tokenMax := minMaxFinite(tokens)
	durationMin, durationMax := minMaxFinite(durations)
	stepMin, stepMax := minMaxFinite(steps)

	for i := range summaries {
		summaries[i].Score =
			weights.Success*normalizeHigherBetter(summaries[i].SuccessRate, successMin, successMax) +
				weights.Tokens*normalizeLowerBetter(summaries[i].TokensPerSuccess, tokenMin, tokenMax) +
				weights.Duration*normalizeLowerBetter(summaries[i].MedianDurationMs, durationMin, durationMax) +
				weights.ToolCalls*normalizeLowerBetter(summaries[i].MedianToolCalls, stepMin, stepMax)
	}
}

func competitorKey(agent, browser string) string {
	return strings.TrimSpace(agent) + " + " + strings.TrimSpace(browser)
}

func sortCompetitors(summaries []CompetitorSummary) {
	sort.Slice(summaries, func(i, j int) bool {
		if summaries[i].Score == summaries[j].Score {
			return summaries[i].Key < summaries[j].Key
		}
		return summaries[i].Score > summaries[j].Score
	})
}

func winnersByAgent(summaries []CompetitorSummary) map[string]CompetitorSummary {
	result := map[string]CompetitorSummary{}
	for _, summary := range summaries {
		current, ok := result[summary.Agent]
		if !ok || summary.Score > current.Score {
			result[summary.Agent] = summary
		}
	}
	return result
}

func median(values []float64) float64 {
	if len(values) == 0 {
		return math.Inf(1)
	}
	sorted := slices.Clone(values)
	sort.Float64s(sorted)
	mid := len(sorted) / 2
	if len(sorted)%2 == 1 {
		return sorted[mid]
	}
	return (sorted[mid-1] + sorted[mid]) / 2
}

func percentile(values []float64, p float64) float64 {
	if len(values) == 0 {
		return math.Inf(1)
	}
	sorted := slices.Clone(values)
	sort.Float64s(sorted)
	if len(sorted) == 1 {
		return sorted[0]
	}
	rank := (p / 100) * float64(len(sorted)-1)
	lower := int(math.Floor(rank))
	upper := int(math.Ceil(rank))
	if lower == upper {
		return sorted[lower]
	}
	weight := rank - float64(lower)
	return sorted[lower] + (sorted[upper]-sorted[lower])*weight
}

func ratio(num, denom int) float64 {
	if denom == 0 {
		return 0
	}
	return float64(num) / float64(denom)
}

func minMax(values []float64) (float64, float64) {
	if len(values) == 0 {
		return 0, 0
	}
	minVal := values[0]
	maxVal := values[0]
	for _, value := range values[1:] {
		if value < minVal {
			minVal = value
		}
		if value > maxVal {
			maxVal = value
		}
	}
	return minVal, maxVal
}

func minMaxFinite(values []float64) (float64, float64) {
	minVal := math.Inf(1)
	maxVal := math.Inf(-1)
	for _, value := range values {
		if math.IsInf(value, 1) {
			continue
		}
		if value < minVal {
			minVal = value
		}
		if value > maxVal {
			maxVal = value
		}
	}
	if math.IsInf(minVal, 1) {
		return 0, 0
	}
	return minVal, maxVal
}

func normalizeHigherBetter(value, minVal, maxVal float64) float64 {
	if math.IsInf(value, 1) {
		return 0
	}
	if minVal == maxVal {
		return 1
	}
	return (value - minVal) / (maxVal - minVal)
}

func normalizeLowerBetter(value, minVal, maxVal float64) float64 {
	if math.IsInf(value, 1) {
		return 0
	}
	if minVal == maxVal {
		return 1
	}
	return (maxVal - value) / (maxVal - minVal)
}

func sanitizeCompetitor(summary *CompetitorSummary) {
	summary.TokensPerSuccess = sanitizeFloat(summary.TokensPerSuccess)
	summary.MedianDurationMs = sanitizeFloat(summary.MedianDurationMs)
	summary.P95DurationMs = sanitizeFloat(summary.P95DurationMs)
	summary.MedianToolCalls = sanitizeFloat(summary.MedianToolCalls)
	summary.Score = sanitizeFloat(summary.Score)
}

func sanitizeFloat(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	return v
}
