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
	}
	session := &contracts.AnalysisChatSession{
		SessionID:        "chat-1",
		RunID:            "run-chat",
		CreatedAt:        time.Now(),
		SelectedSubjects: []contracts.AnalysisSubjectSelector{{SubjectID: "agent-a", SampleID: "s1", Language: "python"}},
	}
	_, user := buildAnalysisChatPrompt(report, session, "它为什么没完成验证？")
	for _, want := range []string{"它为什么没完成验证？", "agent-a / s1 / python", "pytest", "未观察到测试执行步骤"} {
		if !strings.Contains(user, want) {
			t.Fatalf("prompt missing %q:\n%s", want, user)
		}
	}
}
