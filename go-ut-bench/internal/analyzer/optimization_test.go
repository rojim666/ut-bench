package analyzer

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go-ut-bench/internal/contracts"
)

type fakeOptimizationLLM struct {
	raw string
	err error
}

func (f fakeOptimizationLLM) Analyze(ctx context.Context, req LLMRequest) (contracts.LLMAnalysisResult, error) {
	if f.err != nil {
		return contracts.LLMAnalysisResult{}, f.err
	}
	return contracts.LLMAnalysisResult{
		Model:     "fake",
		Status:    "ok",
		RawOutput: f.raw,
		Summary:   "optimization",
	}, nil
}

func TestOptimizePlanRequiresAnalysisReport(t *testing.T) {
	root := t.TempDir()
	_, err := NewService().OptimizePlan(context.Background(), OptimizeOptions{
		RunID:      "missing",
		OutputRoot: root,
		Force:      true,
	})
	if err == nil {
		t.Fatal("expected missing analysis report error")
	}
}

func TestOptimizePlanFromRulesWritesArtifacts(t *testing.T) {
	root := t.TempDir()
	runID := "run-opt-rules"
	writeOptimizationFixture(t, root, runID)

	plan, err := NewService().OptimizePlan(context.Background(), OptimizeOptions{
		RunID:      runID,
		OutputRoot: root,
		LLMEnabled: false,
		Force:      true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.RunID != runID || plan.Summary.ItemCount == 0 {
		t.Fatalf("unexpected plan summary: %+v", plan)
	}
	if plan.LLMStatus.Status != "disabled" {
		t.Fatalf("llm status = %s", plan.LLMStatus.Status)
	}
	if !hasOptimizationTarget(plan.Items, "environment") || !hasOptimizationTarget(plan.Items, "prompt") {
		t.Fatalf("expected environment and prompt items, got %+v", plan.Items)
	}
	for _, item := range plan.Items {
		if len(item.Actions) == 0 {
			t.Fatalf("item missing actions: %+v", item)
		}
		if len(item.Verification) == 0 {
			t.Fatalf("item missing verification: %+v", item)
		}
	}
	analysisDir := filepath.Join(root, "runs", runID, "analysis")
	for _, name := range []string{"optimization_plan.json", "optimization_plan.md"} {
		if _, err := os.Stat(filepath.Join(analysisDir, name)); err != nil {
			t.Fatalf("%s not written: %v", name, err)
		}
	}
}

func TestOptimizePlanMergesLLMItemsWithValidEvidence(t *testing.T) {
	root := t.TempDir()
	runID := "run-opt-llm"
	writeOptimizationFixture(t, root, runID)
	writeEvidenceBundleFixture(t, root, runID)
	raw := `{
	  "summary": "建议优先收紧 agent 环境命令。",
	  "items": [{
	    "priority": "P1",
	    "target": "agent_config",
	    "category": "efficiency",
	    "title": "限制 CodeBuddy 重复探索",
	    "detail": "CodeBuddy token 消耗偏高，应限制无关读取和重复验证。",
	    "applies_to": ["codebuddy__m__qta-ut"],
	    "expected_metrics": ["efficiency"],
	    "actions": [{"title":"限制探索轮次","detail":"在 agent_config 中设置最大工具调用或最大 token 预算。"}],
	    "risks": [{"description":"可能降低复杂样本探索深度","mitigation":"只在轻量评测集启用"}],
	    "verification": [{"manual_step":"重新运行同一小样本，确认 token 下降且测试仍通过。"}],
	    "evidence": [{"evidence_id":"ev-001"}]
	  }]
	}`
	plan, err := NewServiceWithLLM(fakeOptimizationLLM{raw: raw}).OptimizePlan(context.Background(), OptimizeOptions{
		RunID:      runID,
		OutputRoot: root,
		LLMEnabled: true,
		Force:      true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.LLMStatus.Status != "ok" {
		t.Fatalf("llm status = %s", plan.LLMStatus.Status)
	}
	if len(plan.Items) == 0 || plan.Items[0].Source != "llm" {
		t.Fatalf("llm item should be first: %+v", plan.Items)
	}
	if len(plan.Items[0].Evidence) != 1 || plan.Items[0].Evidence[0].EvidenceID != "ev-001" {
		t.Fatalf("llm evidence not resolved: %+v", plan.Items[0].Evidence)
	}
}

func TestOptimizePlanDowngradesLLMItemWithoutValidEvidence(t *testing.T) {
	root := t.TempDir()
	runID := "run-opt-invalid-evidence"
	writeOptimizationFixture(t, root, runID)
	writeEvidenceBundleFixture(t, root, runID)
	raw := `{"summary":"x","items":[{"priority":"P1","target":"prompt","category":"skill","title":"无证据优化","detail":"缺少有效 evidence。","evidence":[{"evidence_id":"missing"}]}]}`
	plan, err := NewServiceWithLLM(fakeOptimizationLLM{raw: raw}).OptimizePlan(context.Background(), OptimizeOptions{
		RunID:      runID,
		OutputRoot: root,
		LLMEnabled: true,
		Force:      true,
	})
	if err != nil {
		t.Fatal(err)
	}
	var found *contracts.OptimizationItem
	for i := range plan.Items {
		if plan.Items[i].Source == "llm" {
			found = &plan.Items[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("llm item missing: %+v", plan.Items)
	}
	if found.Priority != "P3" || found.Confidence > 0.35 || len(found.Evidence) != 0 {
		t.Fatalf("llm item not downgraded: %+v", *found)
	}
}

func writeOptimizationFixture(t *testing.T, root, runID string) {
	t.Helper()
	report := contracts.AnalysisReport{
		SchemaVersion: contracts.AnalysisSchemaVersion,
		RunID:         runID,
		GeneratedAt:   time.Now().UTC(),
		Summary: contracts.AnalysisSummary{
			ResultCount:  3,
			FindingCount: 3,
			Headline:     "发现 1 个阻断级问题，需要优先处理。",
		},
		Findings: []contracts.AnalysisFinding{
			{
				ID:             "finding-001",
				Severity:       "P0",
				Category:       "compile",
				SubjectID:      "opencode__m__no_skill",
				SampleID:       "boundary_000",
				Title:          "编译失败",
				Detail:         "sandbox policy violation: pip install pytest -q",
				Recommendation: "禁止 agent 安装依赖，优先使用镜像内工具链。",
				Source:         "rule",
				Evidence:       []contracts.EvidenceRef{{Kind: "evaluation", SubjectID: "opencode__m__no_skill", SampleID: "boundary_000"}},
			},
			{
				ID:             "finding-002",
				Severity:       "P2",
				Category:       "efficiency",
				SubjectID:      "codebuddy__m__qta-ut",
				SampleID:       "boundary_000",
				Title:          "token 消耗异常偏高",
				Detail:         "总 token 为 248285。",
				Recommendation: "限制重复读取和无效循环。",
				Source:         "rule",
				Evidence:       []contracts.EvidenceRef{{Kind: "evaluation", SubjectID: "codebuddy__m__qta-ut", SampleID: "boundary_000"}},
			},
		},
		Recommendations: []contracts.AnalysisRecommendation{
			{
				ID:             "rec-001",
				Priority:       "P1",
				Category:       "policy",
				Target:         "environment",
				Title:          "收紧 sandbox 和 prompt 约束",
				Detail:         "禁止修改业务源码和随意安装依赖。",
				ExpectedImpact: "减少环境污染和不可复现失败。",
				Risk:           "过强约束可能拦截确实需要额外依赖的样本。",
				AppliesTo:      []string{"opencode__m__no_skill"},
				Source:         "rule",
				Evidence:       []contracts.EvidenceRef{{Kind: "trajectory", SubjectID: "opencode__m__no_skill", SampleID: "boundary_000"}},
			},
			{
				ID:             "rec-002",
				Priority:       "P2",
				Category:       "skill",
				Target:         "prompt",
				Title:          "围绕覆盖率和变异分优化单测策略",
				Detail:         "显式枚举边界条件、异常路径和关键分支。",
				ExpectedImpact: "提升覆盖率和变异杀死率。",
				Risk:           "更强断言可能暴露源码行为理解错误。",
				AppliesTo:      []string{"opencode__m__no_skill"},
				Source:         "rule",
				Evidence:       []contracts.EvidenceRef{{Kind: "evaluation", SubjectID: "opencode__m__no_skill", SampleID: "boundary_000"}},
			},
		},
	}
	if err := contracts.WriteJSON(filepath.Join(root, "runs", runID, "analysis", "analysis_report.json"), report); err != nil {
		t.Fatal(err)
	}
}

func writeEvidenceBundleFixture(t *testing.T, root, runID string) {
	t.Helper()
	bundle := LLMEvidenceBundle{
		SchemaVersion: llmEvidenceSchemaVersion,
		RunID:         runID,
		Evidence: []LLMEvidenceItem{{
			EvidenceID: "ev-001",
			Kind:       "metrics",
			SubjectID:  "codebuddy__m__qta-ut",
			SampleID:   "boundary_000",
			Title:      "指标摘要",
			Excerpt:    "total_tokens=248285",
		}},
	}
	if err := contracts.WriteJSON(filepath.Join(root, "runs", runID, "analysis", "llm_evidence_bundle.json"), bundle); err != nil {
		t.Fatal(err)
	}
}

func hasOptimizationTarget(items []contracts.OptimizationItem, target string) bool {
	for _, item := range items {
		if item.Target == target {
			return true
		}
	}
	return false
}
