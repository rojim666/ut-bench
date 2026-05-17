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

type captureLLM struct {
	prompt string
}

func (c *captureLLM) Analyze(ctx context.Context, req LLMRequest) (contracts.LLMAnalysisResult, error) {
	c.prompt = req.Prompt
	return contracts.LLMAnalysisResult{
		Model:     "fake",
		Status:    "ok",
		Summary:   "ok",
		RawOutput: `{"summary":"ok","findings":[],"recommendations":[]}`,
	}, nil
}

func TestClassifyWorkspaceChange(t *testing.T) {
	tests := []struct {
		name       string
		sourcePath string
		changes    []string
		wantSource bool
		wantPaths  []string
		wantTest   bool
		wantNoise  int
	}{
		{
			name:       "source file change detected",
			sourcePath: "datasets/go/sample.go",
			changes:    []string{"sample.go"},
			wantSource: true,
			wantPaths:  []string{"sample.go"},
		},
		{
			name:       "test file change detected",
			sourcePath: "datasets/go/sample.go",
			changes:    []string{"generated_test.go"},
			wantTest:   true,
		},
		{
			name:       "test file with _test suffix detected",
			sourcePath: "datasets/go/sample.go",
			changes:    []string{"sample_test.go"},
			wantTest:   true,
		},
		{
			name:       "test file with test_ prefix not misclassified as source",
			sourcePath: "datasets/python/sample.py",
			changes:    []string{"test_sample.py"},
			wantTest:   true,
		},
		{
			name:       "java test file detected",
			sourcePath: "datasets/java/Sample.java",
			changes:    []string{"SampleTest.java"},
			wantTest:   true,
		},
		{
			name:       "source and test both changed",
			sourcePath: "datasets/go/sample.go",
			changes:    []string{"sample.go", "generated_test.go"},
			wantSource: true,
			wantPaths:  []string{"sample.go"},
			wantTest:   true,
		},
		{
			name:       "runtime noise excluded",
			sourcePath: "datasets/python/sample.py",
			changes:    []string{".pytest_cache/v/cache/lastfailed", "__pycache__/sample.cpython-311.pyc"},
			wantNoise:  2,
		},
		{
			name:       "source with path prefix matched",
			sourcePath: "datasets/go/pkg/sample.go",
			changes:    []string{"pkg/sample.go"},
			wantSource: true,
			wantPaths:  []string{"pkg/sample.go"},
		},
		{
			name:       "case insensitive source match",
			sourcePath: "datasets/go/Sample.go",
			changes:    []string{"sample.go"},
			wantSource: true,
			wantPaths:  []string{"sample.go"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			subject := contracts.AnalysisSubject{SourcePath: tt.sourcePath}
			for _, c := range tt.changes {
				classifyWorkspaceChange(&subject, c)
			}
			if subject.ModifiedSource != tt.wantSource {
				t.Errorf("ModifiedSource = %v, want %v", subject.ModifiedSource, tt.wantSource)
			}
			if tt.wantPaths != nil {
				got := strings.Join(subject.ModifiedSourcePaths, ",")
				want := strings.Join(tt.wantPaths, ",")
				if got != want {
					t.Errorf("ModifiedSourcePaths = %q, want %q", got, want)
				}
			}
			if subject.HasTestWrite != tt.wantTest {
				t.Errorf("HasTestWrite = %v, want %v", subject.HasTestWrite, tt.wantTest)
			}
			if subject.RuntimeNoiseCount != tt.wantNoise {
				t.Errorf("RuntimeNoiseCount = %d, want %d", subject.RuntimeNoiseCount, tt.wantNoise)
			}
		})
	}
}

func TestClassifyWorkspaceChangeSourceCheckBeforeTestCheck(t *testing.T) {
	// 验证源文件名包含 "test" 子串时，优先被识别为源码修改而非测试文件
	subject := contracts.AnalysisSubject{SourcePath: "datasets/python/test_utils.py"}
	classifyWorkspaceChange(&subject, "test_utils.py")
	if !subject.ModifiedSource {
		t.Fatal("file matching source base should be classified as source modification")
	}
	if subject.HasTestWrite {
		t.Fatal("file matching source base should not also be classified as test write")
	}
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

func TestAnalyzeHighlightsSourceModificationAsFailureCause(t *testing.T) {
	root := t.TempDir()
	runID := "run-source-modified"
	runDir := filepath.Join(root, "runs", runID)
	diffPath := filepath.Join(runDir, "generated", "metadata", "agent_traces", "claudecode", "go", "sample.diff.json")
	if err := contracts.WriteJSON(diffPath, map[string]any{"changes": []string{"sample.go", "generated_test.go"}}); err != nil {
		t.Fatal(err)
	}
	eval := contracts.EvaluationResultSet{
		SchemaVersion:  contracts.SchemaVersion,
		RunID:          runID,
		EvaluatedAtUTC: time.Now().UTC(),
		Results: []contracts.EvaluationResult{{
			Model:             "claudecode__m__qta-ut",
			SubjectID:         "claudecode__m__qta-ut",
			Language:          "go",
			SampleID:          "sample",
			SourcePath:        "datasets/go/sample.go",
			CompilePass:       false,
			CompileError:      "found packages main and fixspaces",
			WorkspaceDiffPath: diffPath,
		}},
	}
	if err := contracts.WriteJSON(filepath.Join(runDir, "evaluation", "evaluation_result.json"), eval); err != nil {
		t.Fatal(err)
	}
	report, err := NewService().Analyze(context.Background(), Options{RunID: runID, OutputRoot: root, Force: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Subjects) != 1 || !report.Subjects[0].ModifiedSource {
		t.Fatalf("source modification not detected: %+v", report.Subjects)
	}
	if got := strings.Join(report.Subjects[0].ModifiedSourcePaths, ","); got != "sample.go" {
		t.Fatalf("modified source paths = %q", got)
	}
	var found bool
	for _, finding := range report.Findings {
		if finding.Category == "policy" && strings.Contains(finding.Detail, "源码污染") && strings.Contains(finding.Detail, "sample.go") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("source pollution finding missing: %+v", report.Findings)
	}
	if !strings.Contains(strings.Join(report.Summary.KeyPoints, "\n"), "修改了被测源码") {
		t.Fatalf("summary does not highlight source modification: %+v", report.Summary.KeyPoints)
	}
	if len(report.RootCauses) == 0 || report.RootCauses[0].Category != "policy" {
		t.Fatalf("source modification root cause missing: %+v", report.RootCauses)
	}
	if !strings.Contains(report.RootCauses[0].Detail, "源码污染") {
		t.Fatalf("root cause should explain source pollution: %+v", report.RootCauses[0])
	}
	if len(report.EvidenceIndex) == 0 {
		t.Fatalf("evidence index missing")
	}
}

func TestAnalyzeGroupsRepeatedTraceRootCause(t *testing.T) {
	root := t.TempDir()
	runID := "run-group-trace"
	runDir := filepath.Join(root, "runs", runID)
	ok := true
	results := make([]contracts.EvaluationResult, 0, 2)
	for _, subjectID := range []string{"claudecode__m__qta-ut", "codebuddy__m__qta-ut"} {
		traceDir := filepath.Join(runDir, "generated", "metadata", "agent_traces", subjectID, "python")
		trajPath := filepath.Join(traceDir, "sample.trajectory.json")
		if err := contracts.WriteJSON(trajPath, runner.AgentTrajectory{
			SchemaVersion: "trajectory.v0.1.0",
			SubjectID:     subjectID,
			SampleID:      "sample",
			Language:      "python",
			Steps: []runner.TrajectoryStep{
				{Index: 1, Kind: "tool_call", Tool: "read", Input: map[string]any{"filePath": "/workspace/sample.py"}, Success: &ok},
				{Index: 2, Kind: "tool_call", Tool: "write", Input: map[string]any{"filePath": "/workspace/test_sample.py"}, Success: &ok},
			},
		}); err != nil {
			t.Fatal(err)
		}
		results = append(results, contracts.EvaluationResult{
			Model:          subjectID,
			SubjectID:      subjectID,
			Language:       "python",
			SampleID:       "sample",
			CompilePass:    true,
			TrajectoryPath: trajPath,
		})
	}
	if err := contracts.WriteJSON(filepath.Join(runDir, "evaluation", "evaluation_result.json"), contracts.EvaluationResultSet{
		SchemaVersion:  contracts.SchemaVersion,
		RunID:          runID,
		EvaluatedAtUTC: time.Now().UTC(),
		Results:        results,
	}); err != nil {
		t.Fatal(err)
	}
	report, err := NewService().Analyze(context.Background(), Options{RunID: runID, OutputRoot: root, Force: true})
	if err != nil {
		t.Fatal(err)
	}
	var grouped *contracts.AnalysisRootCause
	for i := range report.RootCauses {
		if report.RootCauses[i].Title == "未观察到本地测试验证" {
			grouped = &report.RootCauses[i]
			break
		}
	}
	if grouped == nil {
		t.Fatalf("grouped trace root cause missing: %+v", report.RootCauses)
	}
	if len(grouped.AffectedSubjects) != 2 {
		t.Fatalf("affected subjects = %d, root cause = %+v", len(grouped.AffectedSubjects), grouped)
	}
	if !strings.Contains(grouped.Detail, "共 2 个对象") {
		t.Fatalf("group detail should summarize repeated cause: %+v", grouped.Detail)
	}
}

func TestAnalyzeSelectedHealthySubjectIncludedInEvidence(t *testing.T) {
	root := t.TempDir()
	runID := "run-selected"
	runDir := filepath.Join(root, "runs", runID)
	ok := true
	cov := 0.95
	mut := 0.9
	eval := contracts.EvaluationResultSet{
		SchemaVersion:  contracts.SchemaVersion,
		RunID:          runID,
		EvaluatedAtUTC: time.Now().UTC(),
		Results: []contracts.EvaluationResult{{
			Model:         "agent-a",
			SubjectID:     "agent-a",
			Language:      "python",
			SampleID:      "sample-a",
			CompilePass:   true,
			TestPass:      &ok,
			LineCoverage:  &cov,
			MutationScore: &mut,
		}, {
			Model:        "agent-b",
			SubjectID:    "agent-b",
			Language:     "python",
			SampleID:     "sample-b",
			CompilePass:  false,
			CompileError: "boom",
		}},
	}
	if err := contracts.WriteJSON(filepath.Join(runDir, "evaluation", "evaluation_result.json"), eval); err != nil {
		t.Fatal(err)
	}
	llm := &captureLLM{}
	report, err := NewServiceWithLLM(llm).Analyze(context.Background(), Options{
		RunID:      runID,
		OutputRoot: root,
		LLMEnabled: true,
		Force:      true,
		SelectedSubjects: []contracts.AnalysisSubjectSelector{{
			SubjectID: "agent-a",
			SampleID:  "sample-a",
			Language:  "python",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Selection.SelectedSubjects) != 1 {
		t.Fatalf("selection missing: %+v", report.Selection)
	}
	var bundle LLMEvidenceBundle
	raw, err := os.ReadFile(filepath.Join(runDir, "analysis", "llm_evidence_bundle.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &bundle); err != nil {
		t.Fatal(err)
	}
	if len(bundle.Subjects) != 1 || bundle.Subjects[0].SubjectID != "agent-a" {
		t.Fatalf("selected subject not focused in bundle: %+v", bundle.Subjects)
	}
	if !strings.Contains(strings.Join(bundle.Subjects[0].SelectedReason, ","), "user_selected") {
		t.Fatalf("selected reason missing: %+v", bundle.Subjects[0].SelectedReason)
	}
}

func TestAnalyzeCompareSelectionPromptAndMissingSubject(t *testing.T) {
	root := t.TempDir()
	runID := "run-compare"
	runDir := filepath.Join(root, "runs", runID)
	eval := contracts.EvaluationResultSet{
		SchemaVersion:  contracts.SchemaVersion,
		RunID:          runID,
		EvaluatedAtUTC: time.Now().UTC(),
		Results: []contracts.EvaluationResult{{
			Model:       "agent-a",
			SubjectID:   "agent-a",
			Language:    "go",
			SampleID:    "sample",
			CompilePass: true,
		}, {
			Model:       "agent-b",
			SubjectID:   "agent-b",
			Language:    "go",
			SampleID:    "sample",
			CompilePass: true,
		}},
	}
	if err := contracts.WriteJSON(filepath.Join(runDir, "evaluation", "evaluation_result.json"), eval); err != nil {
		t.Fatal(err)
	}
	llm := &captureLLM{}
	_, err := NewServiceWithLLM(llm).Analyze(context.Background(), Options{
		RunID:       runID,
		OutputRoot:  root,
		LLMEnabled:  true,
		Force:       true,
		CompareMode: true,
		SelectedSubjects: []contracts.AnalysisSubjectSelector{
			{SubjectID: "agent-a", SampleID: "sample", Language: "go"},
			{SubjectID: "agent-b", SampleID: "sample", Language: "go"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(llm.prompt, "横向对比") {
		t.Fatalf("compare prompt missing: %s", llm.prompt)
	}
	report, err := ReadReport(filepath.Join(runDir, "analysis", "analysis_report.json"))
	if err != nil {
		t.Fatal(err)
	}
	if report.ComparisonSummary == nil || len(report.ComparisonSummary.Differences) != 2 {
		t.Fatalf("comparison summary missing: %+v", report.ComparisonSummary)
	}
	_, err = NewServiceWithLLM(&captureLLM{}).Analyze(context.Background(), Options{
		RunID:      runID,
		OutputRoot: root,
		LLMEnabled: true,
		Force:      true,
		SelectedSubjects: []contracts.AnalysisSubjectSelector{{
			SubjectID: "missing",
			SampleID:  "sample",
			Language:  "go",
		}},
	})
	if err == nil || !strings.Contains(err.Error(), "selected subject not found") {
		t.Fatalf("expected selected subject error, got %v", err)
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
