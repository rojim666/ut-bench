package analyzer

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go-ut-bench/internal/contracts"
)

type invalidReportInsightLLM struct{}

func (invalidReportInsightLLM) Analyze(ctx context.Context, req LLMRequest) (contracts.LLMAnalysisResult, error) {
	return contracts.LLMAnalysisResult{
		Model:     "fake",
		Status:    "ok",
		RawOutput: "{not-json",
	}, nil
}

func TestReportInsightsFromReportSummaryWithoutTrajectory(t *testing.T) {
	root := t.TempDir()
	runID := "run-report-summary"
	writeReportInsightFixture(t, root, runID, contracts.ReportPayload{
		SchemaVersion: contracts.SchemaVersion,
		RunID:         runID,
		Summary: contracts.ReportSummary{
			TotalSamples:        2,
			CompilePassRate:     0.5,
			SampleTestPassRate:  0.5,
			AvgLineCoverage:     0.4,
			AvgMutationScore:    0.2,
			AvgAssertionDensity: 1,
		},
	})

	report, err := NewService().Analyze(context.Background(), Options{RunID: runID, OutputRoot: root, RuleEnabled: true, Force: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.ReportInsights) == 0 {
		t.Fatalf("report insights missing")
	}
	if report.ReportInsightStatus.Status != "ok" {
		t.Fatalf("report insight status = %s", report.ReportInsightStatus.Status)
	}
	if !hasReportEvidenceKind(report, "report_summary") {
		t.Fatalf("report summary evidence missing: %+v", report.EvidenceIndex)
	}
}

func TestReportInsightsSkillUpliftCreatesEvolutionItem(t *testing.T) {
	root := t.TempDir()
	runID := "run-skill-uplift"
	writeReportInsightFixture(t, root, runID, contracts.ReportPayload{
		SchemaVersion: contracts.SchemaVersion,
		RunID:         runID,
		Summary: contracts.ReportSummary{
			CompilePassRate:    0.8,
			SampleTestPassRate: 0.7,
			AvgLineCoverage:    0.6,
			AvgMutationScore:   0.5,
		},
		SkillUplifts: []contracts.SkillUpliftRow{{
			SubjectID:          "codex__m__qta-ut",
			BaselineSubjectID:  "codex__m__no_skill",
			Framework:          "codex",
			Model:              "m",
			Skill:              "qta-ut",
			Language:           "go",
			LineCoverageDelta:  0.22,
			MutationScoreDelta: 0.18,
		}},
	})

	report, err := NewService().Analyze(context.Background(), Options{RunID: runID, OutputRoot: root, RuleEnabled: true, Force: true})
	if err != nil {
		t.Fatal(err)
	}
	if !hasReportInsightCategory(report, "skill_uplift") {
		t.Fatalf("skill uplift insight missing: %+v", report.ReportInsights)
	}
	if report.EvolutionPlan == nil || !hasEvolutionTarget(report.EvolutionPlan, "skill") {
		t.Fatalf("skill evolution item missing: %+v", report.EvolutionPlan)
	}
	if !hasReportEvidenceKind(report, "skill_uplift") {
		t.Fatalf("skill uplift evidence missing")
	}
}

func TestReportInsightsHighTokenLowROICreatesEfficiencyInsight(t *testing.T) {
	root := t.TempDir()
	runID := "run-efficiency"
	writeReportInsightFixture(t, root, runID, contracts.ReportPayload{
		SchemaVersion: contracts.SchemaVersion,
		RunID:         runID,
		Summary: contracts.ReportSummary{
			CompilePassRate:    0.8,
			SampleTestPassRate: 0.8,
			AvgLineCoverage:    0.7,
			AvgMutationScore:   0.6,
		},
		Dimensions: contracts.Dimensions{ByModel: []contracts.ModelDim{
			{Model: "cheap-good", SubjectID: "cheap-good", CompositeScore: 0.8, AvgTotalTokens: 1000},
			{Model: "expensive-flat", SubjectID: "expensive-flat", CompositeScore: 0.45, AvgTotalTokens: 8000},
		}},
		EfficiencyStats: contracts.EfficiencyStats{
			CostEstimate: contracts.CostEstimate{TotalTokens: 9000},
		},
	})

	report, err := NewService().Analyze(context.Background(), Options{RunID: runID, OutputRoot: root, RuleEnabled: true, Force: true})
	if err != nil {
		t.Fatal(err)
	}
	if !hasReportInsightCategory(report, "efficiency") {
		t.Fatalf("efficiency insight missing: %+v", report.ReportInsights)
	}
	if report.EvolutionPlan == nil || !hasEvolutionTarget(report.EvolutionPlan, "agent_config") {
		t.Fatalf("agent_config evolution item missing: %+v", report.EvolutionPlan)
	}
}

func TestReportInsightsJavaLowMutationLanguageGap(t *testing.T) {
	root := t.TempDir()
	runID := "run-java-gap"
	writeReportInsightFixture(t, root, runID, contracts.ReportPayload{
		SchemaVersion: contracts.SchemaVersion,
		RunID:         runID,
		Summary: contracts.ReportSummary{
			CompilePassRate:    0.8,
			SampleTestPassRate: 0.7,
			AvgLineCoverage:    0.7,
			AvgMutationScore:   0.5,
		},
		Dimensions: contracts.Dimensions{ByLanguage: []contracts.LanguageDim{
			{Language: "python", CompilePassRate: 1, AvgTestPassRate: 0.9, AvgLineCoverage: 0.8, AvgMutationScore: 0.75},
			{Language: "java", CompilePassRate: 0.9, AvgTestPassRate: 0.8, AvgLineCoverage: 0.9, AvgMutationScore: 0.2},
		}},
	})

	report, err := NewService().Analyze(context.Background(), Options{RunID: runID, OutputRoot: root, RuleEnabled: true, Force: true})
	if err != nil {
		t.Fatal(err)
	}
	if !hasReportInsightCategory(report, "language_gap") {
		t.Fatalf("language gap insight missing: %+v", report.ReportInsights)
	}
	foundJava := false
	for _, insight := range report.ReportInsights {
		if insight.Category == "language_gap" && strings.Contains(strings.ToLower(insight.Title+insight.Detail), "java") {
			foundJava = true
		}
	}
	if !foundJava {
		t.Fatalf("java language gap missing: %+v", report.ReportInsights)
	}
}

func TestReportInsightLLMParseFailureKeepsRulesAndDegrades(t *testing.T) {
	root := t.TempDir()
	runID := "run-report-llm-degraded"
	writeReportInsightFixture(t, root, runID, contracts.ReportPayload{
		SchemaVersion: contracts.SchemaVersion,
		RunID:         runID,
		Summary: contracts.ReportSummary{
			CompilePassRate:    0.5,
			SampleTestPassRate: 0.4,
			AvgLineCoverage:    0.4,
			AvgMutationScore:   0.2,
		},
	})

	report, err := NewServiceWithLLM(invalidReportInsightLLM{}).Analyze(context.Background(), Options{RunID: runID, OutputRoot: root, RuleEnabled: true, LLMEnabled: true, Force: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.ReportInsights) == 0 {
		t.Fatalf("rule report insights should be retained")
	}
	if report.ReportInsightStatus.Status != "degraded" {
		t.Fatalf("report insight status = %s, want degraded", report.ReportInsightStatus.Status)
	}
}

func writeReportInsightFixture(t *testing.T, root, runID string, payload contracts.ReportPayload) {
	t.Helper()
	runDir := filepath.Join(root, "runs", runID)
	eval := contracts.EvaluationResultSet{
		SchemaVersion:  contracts.SchemaVersion,
		RunID:          runID,
		EvaluatedAtUTC: time.Now().UTC(),
		Results: []contracts.EvaluationResult{{
			RunID:         runID,
			Model:         "model-a",
			SubjectID:     "agent-a",
			Language:      "go",
			SampleID:      "sample-a",
			CompilePass:   true,
			TestPass:      boolPtrForReportInsightTest(true),
			LineCoverage:  floatPtrForReportInsightTest(0.5),
			MutationScore: floatPtrForReportInsightTest(0.3),
		}},
	}
	if err := contracts.WriteJSON(filepath.Join(runDir, "evaluation", "evaluation_result.json"), eval); err != nil {
		t.Fatal(err)
	}
	payload.RunID = runID
	if payload.GeneratedAtUTC.IsZero() {
		payload.GeneratedAtUTC = time.Now().UTC()
	}
	if err := contracts.WriteJSON(filepath.Join(runDir, "report", "report_summary.json"), payload); err != nil {
		t.Fatal(err)
	}
}

func hasReportInsightCategory(report *contracts.AnalysisReport, category string) bool {
	for _, item := range report.ReportInsights {
		if item.Category == category {
			return true
		}
	}
	return false
}

func hasEvolutionTarget(plan *contracts.EvolutionPlan, target string) bool {
	for _, item := range plan.Items {
		if item.Target == target {
			return true
		}
	}
	return false
}

func hasReportEvidenceKind(report *contracts.AnalysisReport, kind string) bool {
	for _, item := range report.EvidenceIndex {
		if item.Kind == kind {
			return true
		}
	}
	return false
}

func boolPtrForReportInsightTest(v bool) *bool {
	return &v
}

func floatPtrForReportInsightTest(v float64) *float64 {
	return &v
}
