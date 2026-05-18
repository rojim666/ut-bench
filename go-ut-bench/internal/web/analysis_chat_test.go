package web

import (
	"strings"
	"testing"
	"time"

	"go-ut-bench/internal/contracts"
)

func TestNormalizeChatSelectionUsesReportSelectionAndValidatesSubjects(t *testing.T) {
	report := &contracts.AnalysisReport{
		Selection: contracts.AnalysisSelection{
			SelectedSubjects: []contracts.AnalysisSubjectSelector{{SubjectID: "agent-a", SampleID: "s1", Language: "go"}},
		},
		Subjects: []contracts.AnalysisSubject{
			{SubjectID: "agent-a", SampleID: "s1", Language: "go"},
			{SubjectID: "agent-b", SampleID: "s1", Language: "go"},
		},
	}
	got, err := normalizeChatSelection(report, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].SubjectID != "agent-a" {
		t.Fatalf("selection = %+v", got)
	}
	_, err = normalizeChatSelection(report, []contracts.AnalysisSubjectSelector{{SubjectID: "missing", SampleID: "s1", Language: "go"}})
	if err == nil || !strings.Contains(err.Error(), "selected subject not found") {
		t.Fatalf("expected missing subject error, got %v", err)
	}
}

func TestBuildAnalysisChatPromptIncludesSelectedTrajectoryAndQuestion(t *testing.T) {
	ok := true
	report := &contracts.AnalysisReport{
		RunID: "run-chat",
		Summary: contracts.AnalysisSummary{
			Headline: "存在 trace 差异",
		},
		Subjects: []contracts.AnalysisSubject{{
			SubjectID:        "agent-a",
			SampleID:         "s1",
			Language:         "python",
			CompilePass:      true,
			TestPass:         &ok,
			TraceStepCount:   2,
			HasSourceRead:    true,
			HasTestWrite:     true,
			HasTestExecution: false,
			Trajectory: []contracts.AnalysisTrajectoryStep{
				{Index: 1, Kind: "tool_call", Tool: "read_file", TextExcerpt: "read source"},
				{Index: 2, Kind: "command", Tool: "shell", InputExcerpt: "pytest"},
			},
		}},
		Findings: []contracts.AnalysisFinding{{
			Severity:  "P2",
			Source:    "rule",
			Category:  "trace",
			SubjectID: "agent-a",
			SampleID:  "s1",
			Title:     "未观察到测试执行步骤",
			Detail:    "trajectory 中没有发现测试命令。",
		}},
		ReportInsights: []contracts.ReportInsight{{
			Priority: "P1",
			Source:   "rule",
			Category: "skill_uplift",
			Title:    "下一轮先改 qta-ut",
			Detail:   "skill uplift 明显，优先沉淀高收益策略。",
		}},
		EvolutionPlan: &contracts.EvolutionPlan{
			Summary: "优先优化 skill",
			Items: []contracts.EvolutionItem{{
				Priority: "P1",
				Source:   "rule",
				Target:   "skill",
				Title:    "沉淀高 uplift skill 策略",
				Reason:   "覆盖和变异提升空间最大。",
			}},
		},
	}
	session := &contracts.AnalysisChatSession{
		SessionID:        "chat-1",
		RunID:            "run-chat",
		CreatedAt:        time.Now(),
		SelectedSubjects: []contracts.AnalysisSubjectSelector{{SubjectID: "agent-a", SampleID: "s1", Language: "python"}},
	}
	_, user := buildAnalysisChatPrompt(report, session, "它为什么没完成验证？", "", "")
	for _, want := range []string{"它为什么没完成验证？", "agent-a / s1 / python", "pytest", "未观察到测试执行步骤", "报告级洞察", "自进化建议", "沉淀高 uplift skill 策略"} {
		if !strings.Contains(user, want) {
			t.Fatalf("prompt missing %q:\n%s", want, user)
		}
	}
}

func TestLimitChatFindingsFiltersByFocusedRootCause(t *testing.T) {
	selected := []contracts.AnalysisSubjectSelector{
		{SubjectID: "agent-a", SampleID: "s1", Language: "go"},
		{SubjectID: "agent-b", SampleID: "s1", Language: "go"},
	}
	findings := []contracts.AnalysisFinding{
		{ID: "rule-001", Severity: "P0", Source: "rule", Category: "policy", SubjectID: "agent-a", SampleID: "s1", Title: "源码污染"},
		{ID: "rule-002", Severity: "P1", Source: "rule", Category: "compile", SubjectID: "agent-b", SampleID: "s1", Title: "编译失败"},
		{ID: "rule-003", Severity: "P2", Source: "rule", Category: "trace", SubjectID: "agent-a", SampleID: "s1", Title: "缺少轨迹"},
	}
	rootCauses := []contracts.AnalysisRootCause{
		{ID: "rc-001", RelatedFindings: []string{"rule-001", "rule-003"}},
		{ID: "rc-002", RelatedFindings: []string{"rule-002"}},
	}

	// 不聚焦时返回所有匹配 selection 的 findings
	all := limitChatFindings(findings, selected, "", rootCauses, 10)
	if len(all) != 3 {
		t.Fatalf("unfocused: expected 3 findings, got %d", len(all))
	}

	// 聚焦 rc-001 时只返回 rule-001 和 rule-003
	focused := limitChatFindings(findings, selected, "rc-001", rootCauses, 10)
	if len(focused) != 2 {
		t.Fatalf("focused rc-001: expected 2 findings, got %d: %+v", len(focused), focused)
	}
	ids := map[string]bool{}
	for _, f := range focused {
		ids[f.ID] = true
	}
	if !ids["rule-001"] || !ids["rule-003"] {
		t.Fatalf("focused rc-001: expected rule-001 and rule-003, got %+v", ids)
	}

	// 聚焦 rc-002 时只返回 rule-002
	focused2 := limitChatFindings(findings, selected, "rc-002", rootCauses, 10)
	if len(focused2) != 1 || focused2[0].ID != "rule-002" {
		t.Fatalf("focused rc-002: expected [rule-002], got %+v", focused2)
	}

	// 聚焦不存在的 root cause 时返回所有匹配 selection 的 findings
	fallback := limitChatFindings(findings, selected, "rc-missing", rootCauses, 10)
	if len(fallback) != 3 {
		t.Fatalf("missing rc: expected 3 findings, got %d", len(fallback))
	}
}

func TestBuildAnalysisChatPromptFocusesRootCauseAndEvidence(t *testing.T) {
	report := &contracts.AnalysisReport{
		RunID:   "run-chat",
		Summary: contracts.AnalysisSummary{Headline: "存在源码污染"},
		RootCauses: []contracts.AnalysisRootCause{{
			ID:                "rc-001",
			Severity:          "P0",
			Category:          "policy",
			Title:             "修改源码导致评测结果失真",
			Detail:            "agent 修改了 sample.go。",
			RecommendedAction: "禁止修改源码，只改测试。",
			RelatedFindings:   []string{"rule-001"},
		}},
		EvidenceIndex: []contracts.AnalysisEvidenceItem{{
			EvidenceID: "diff-agent-a-s1-go",
			Kind:       "workspace_diff",
			Title:      "workspace diff 摘要",
			SubjectID:  "agent-a",
			SampleID:   "s1",
			Language:   "go",
			Excerpt:    "sample.go\ngenerated_test.go",
		}},
		Findings: []contracts.AnalysisFinding{{
			ID:        "rule-001",
			Severity:  "P0",
			Source:    "rule",
			Category:  "policy",
			SubjectID: "agent-a",
			SampleID:  "s1",
			Title:     "agent 修改了被测源码",
			Detail:    "workspace diff 显示 agent 修改源码。",
		}},
	}
	session := &contracts.AnalysisChatSession{SessionID: "chat-1", RunID: "run-chat"}
	_, user := buildAnalysisChatPrompt(report, session, "这个证据说明什么？", "diff-agent-a-s1-go", "rc-001")
	for _, want := range []string{"当前聚焦根因", "修改源码导致评测结果失真", "当前聚焦证据", "sample.go", "agent 修改了被测源码"} {
		if !strings.Contains(user, want) {
			t.Fatalf("prompt missing %q:\n%s", want, user)
		}
	}
}
