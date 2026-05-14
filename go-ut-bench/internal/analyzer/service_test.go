package analyzer

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go-ut-bench/internal/contracts"
	"go-ut-bench/internal/runner"
)

type fakeLLM struct{}

func (fakeLLM) Analyze(ctx context.Context, req LLMRequest) (contracts.LLMAnalysisResult, error) {
	return contracts.LLMAnalysisResult{
		Model:     "fake",
		Status:    "ok",
		Summary:   "建议强化运行前后验证。",
		RawOutput: `{"summary":"建议强化运行前后验证","findings":[],"recommendations":[]}`,
		Findings: []contracts.AnalysisFinding{{
			Severity:       "P2",
			Category:       "skill",
			Title:          "LLM 发现测试策略可优化",
			Detail:         "当前 evidence 显示 agent 没有稳定执行测试命令。",
			Recommendation: "在 skill 中加入生成后本地验证步骤。",
			Evidence:       []contracts.EvidenceRef{{EvidenceID: "ev-001"}},
		}},
		Recommendations: []contracts.AnalysisRecommendation{{
			Priority:       "P2",
			Category:       "skill",
			Target:         "prompt",
			Title:          "补充本地验证要求",
			Detail:         "要求生成测试后执行最小验证命令。",
			ExpectedImpact: "降低未运行测试导致的无效输出。",
			Risk:           "可能增加生成耗时。",
			Evidence:       []contracts.EvidenceRef{{EvidenceID: "ev-001"}},
		}},
	}, nil
}

func TestAnalyzeRulesAndLLMWritesEvidenceArtifacts(t *testing.T) {
	root := t.TempDir()
	runID := "run-test"
	runDir := filepath.Join(root, "runs", runID)
	traceDir := filepath.Join(runDir, "generated", "metadata", "agent_traces", "opencode", "python")
	if err := os.MkdirAll(traceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	trajectoryPath := filepath.Join(traceDir, "sample.trajectory.json")
	diffPath := filepath.Join(traceDir, "sample.diff.json")
	rawTracePath := filepath.Join(traceDir, "sample.raw_trace.jsonl")
	testPath := filepath.Join(runDir, "generated", "tests", "opencode", "python", "sample.test.py")
	if err := os.MkdirAll(filepath.Dir(testPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(testPath, []byte("def test_x():\n    assert True\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rawTracePath, []byte(strings.Repeat(`{"raw":"trace"}`+"\n", 40)), 0o644); err != nil {
		t.Fatal(err)
	}
	ok := true
	traj := runner.AgentTrajectory{
		SchemaVersion: "trajectory.v0.1.0",
		SubjectID:     "opencode__m__no_skill",
		Framework:     "opencode",
		Model:         "m",
		Skill:         "no_skill",
		SampleID:      "sample",
		Language:      "python",
		StartedAt:     time.Now().UTC(),
		FinishedAt:    time.Now().UTC(),
		Steps: []runner.TrajectoryStep{
			{Index: 1, Kind: "tool_call", Tool: "read", Input: map[string]any{"filePath": "/workspace/sample.py"}, Success: &ok},
			{Index: 2, Kind: "tool_call", Tool: "bash", Input: map[string]any{"command": "pip install pytest"}, Success: &ok},
		},
	}
	if err := contracts.WriteJSON(trajectoryPath, traj); err != nil {
		t.Fatal(err)
	}
	if err := contracts.WriteJSON(diffPath, map[string]any{"changes": []string{".pytest_cache/README.md", "generated_test.py"}}); err != nil {
		t.Fatal(err)
	}
	testPass := true
	cov := 0.5
	mut := 0.4
	totalTokens := 210000
	eval := contracts.EvaluationResultSet{
		SchemaVersion:  contracts.SchemaVersion,
		RunID:          runID,
		EvaluatedAtUTC: time.Now().UTC(),
		Results: []contracts.EvaluationResult{{
			RunID:             runID,
			Model:             "opencode__m__no_skill",
			SubjectID:         "opencode__m__no_skill",
			AgentFramework:    "opencode",
			AgentModel:        "m",
			SkillName:         "no_skill",
			Language:          "python",
			SampleID:          "sample",
			GeneratedTestPath: testPath,
			SourcePath:        "datasets/python/sample.py",
			CompilePass:       true,
			TestPass:          &testPass,
			LineCoverage:      &cov,
			MutationScore:     &mut,
			TotalTokens:       &totalTokens,
			TrajectoryPath:    trajectoryPath,
			RawTracePath:      rawTracePath,
			WorkspaceDiffPath: diffPath,
		}},
	}
	if err := contracts.WriteJSON(filepath.Join(runDir, "evaluation", "evaluation_result.json"), eval); err != nil {
		t.Fatal(err)
	}

	svc := NewServiceWithLLM(fakeLLM{})
	report, err := svc.Analyze(context.Background(), Options{RunID: runID, OutputRoot: root, LLMEnabled: true, Force: true})
	if err != nil {
		t.Fatal(err)
	}
	if report.Summary.ResultCount != 1 {
		t.Fatalf("result count = %d", report.Summary.ResultCount)
	}
	if report.LLMStatus.Status != "ok" {
		t.Fatalf("llm status = %s", report.LLMStatus.Status)
	}
	if report.LLMStatus.EvidenceCount == 0 || report.LLMStatus.EvidenceSubjectCount != 1 {
		t.Fatalf("unexpected evidence status: %+v", report.LLMStatus)
	}
	if len(report.Subjects) != 1 || !report.Subjects[0].HasSourceRead {
		t.Fatalf("source read not detected: %+v", report.Subjects)
	}
	if report.Subjects[0].HasTestExecution {
		t.Fatalf("unexpected test execution detected")
	}
	if !hasFinding(report.Findings, "policy") || !hasFinding(report.Findings, "coverage") || !hasFinding(report.Findings, "skill") {
		t.Fatalf("expected policy, coverage and llm skill findings, got %+v", report.Findings)
	}
	analysisDir := filepath.Join(runDir, "analysis")
	for _, name := range []string{"analysis_report.json", "analysis_report.md", "llm_evidence_bundle.json", "llm_raw_output.txt", "llm_diagnosis.json"} {
		if _, err := os.Stat(filepath.Join(analysisDir, name)); err != nil {
			t.Fatalf("%s not written: %v", name, err)
		}
	}
	rawBundle, err := os.ReadFile(filepath.Join(analysisDir, "llm_evidence_bundle.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(rawBundle), `"raw":"trace"`) {
		t.Fatalf("evidence bundle should not include complete raw trace")
	}
	var bundle LLMEvidenceBundle
	if err := json.Unmarshal(rawBundle, &bundle); err != nil {
		t.Fatal(err)
	}
	if len(bundle.Evidence) == 0 || bundle.Evidence[0].EvidenceID != "ev-001" {
		t.Fatalf("unexpected evidence bundle: %+v", bundle)
	}
}

func TestAnalyzeWithoutLLMStillWritesRules(t *testing.T) {
	root := t.TempDir()
	runID := "run-no-llm"
	runDir := filepath.Join(root, "runs", runID)
	eval := contracts.EvaluationResultSet{
		SchemaVersion:  contracts.SchemaVersion,
		RunID:          runID,
		EvaluatedAtUTC: time.Now().UTC(),
		Results: []contracts.EvaluationResult{{
			Model:        "claudecode__m__no_skill",
			SubjectID:    "claudecode__m__no_skill",
			Language:     "python",
			SampleID:     "sample",
			CompilePass:  false,
			CompileError: "missing import",
		}},
	}
	if err := contracts.WriteJSON(filepath.Join(runDir, "evaluation", "evaluation_result.json"), eval); err != nil {
		t.Fatal(err)
	}
	report, err := NewService().Analyze(context.Background(), Options{RunID: runID, OutputRoot: root, LLMEnabled: false, Force: true})
	if err != nil {
		t.Fatal(err)
	}
	if report.LLMStatus.Status != "disabled" {
		t.Fatalf("llm status = %s", report.LLMStatus.Status)
	}
	if !hasFinding(report.Findings, "compile") {
		t.Fatalf("compile finding missing: %+v", report.Findings)
	}
}

func TestAnalyzeInfersTrajectoryFromReusedDiffPath(t *testing.T) {
	root := t.TempDir()
	runID := "run-reuse"
	prevRunID := "run-prev"
	traceDir := filepath.Join(root, "runs", prevRunID, "generated", "metadata", "agent_traces", "claudecode", "python")
	if err := os.MkdirAll(traceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	diffPath := filepath.Join(traceDir, "sample.diff.json")
	trajPath := filepath.Join(traceDir, "sample.trajectory.json")
	rawPath := filepath.Join(traceDir, "sample.raw_trace.jsonl")
	ok := true
	if err := contracts.WriteJSON(trajPath, runner.AgentTrajectory{
		SchemaVersion: "trajectory.v0.1.0",
		SubjectID:     "claudecode__m__no_skill",
		SampleID:      "sample",
		Language:      "python",
		Steps: []runner.TrajectoryStep{
			{Index: 1, Kind: "tool_call", Tool: "Read", Input: map[string]any{"file_path": "/workspace/sample.py"}, Success: &ok},
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rawPath, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := contracts.WriteJSON(diffPath, map[string]any{"changes": []string{"generated_test.py"}}); err != nil {
		t.Fatal(err)
	}
	eval := contracts.EvaluationResultSet{
		SchemaVersion:  contracts.SchemaVersion,
		RunID:          runID,
		EvaluatedAtUTC: time.Now().UTC(),
		Results: []contracts.EvaluationResult{{
			Model:             "claudecode__m__no_skill",
			SubjectID:         "claudecode__m__no_skill",
			AgentFramework:    "claudecode",
			Language:          "python",
			SampleID:          "sample",
			GeneratedTestPath: filepath.Join(root, "runs", runID, "generated", "tests", "sample.test.py"),
			SourcePath:        "datasets/python/sample.py",
			CompilePass:       true,
			WorkspaceDiffPath: diffPath,
		}},
	}
	if err := contracts.WriteJSON(filepath.Join(root, "runs", runID, "evaluation", "evaluation_result.json"), eval); err != nil {
		t.Fatal(err)
	}
	report, err := NewService().Analyze(context.Background(), Options{RunID: runID, OutputRoot: root, Force: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Subjects) != 1 {
		t.Fatalf("subjects = %d", len(report.Subjects))
	}
	if report.Subjects[0].TraceStepCount != 1 {
		t.Fatalf("trace step count = %d", report.Subjects[0].TraceStepCount)
	}
	if report.Subjects[0].RawTracePath == "" || report.Subjects[0].TrajectoryPath == "" {
		t.Fatalf("paths not inferred: %+v", report.Subjects[0])
	}
}

func TestLLMFindingWithoutValidEvidenceIsDowngraded(t *testing.T) {
	items := normalizeLLMFindings([]contracts.AnalysisFinding{{
		Severity: "P1",
		Category: "skill",
		Title:    "无证据建议",
		Detail:   "模型没有引用有效 evidence。",
		Evidence: []contracts.EvidenceRef{{EvidenceID: "missing"}},
	}}, map[string]contracts.EvidenceRef{"ev-001": {EvidenceID: "ev-001", Kind: "metrics"}})
	if len(items) != 1 {
		t.Fatalf("items = %d", len(items))
	}
	if items[0].Severity != "P3" || items[0].Confidence > 0.35 || len(items[0].Evidence) != 0 {
		t.Fatalf("finding not downgraded: %+v", items[0])
	}
}

func TestSortFindingsPrioritizesLLMSource(t *testing.T) {
	items := []contracts.AnalysisFinding{
		{Severity: "P0", Source: "rule", Category: "compile", Title: "规则"},
		{Severity: "P2", Source: "llm", Category: "skill", Title: "LLM"},
	}
	sortFindings(items)
	if items[0].Source != "llm" {
		t.Fatalf("first source = %s, want llm: %+v", items[0].Source, items)
	}
}

func hasFinding(items []contracts.AnalysisFinding, category string) bool {
	for _, item := range items {
		if item.Category == category {
			return true
		}
	}
	return false
}
