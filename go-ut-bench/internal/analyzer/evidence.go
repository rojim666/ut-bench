package analyzer

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go-ut-bench/internal/contracts"
)

const llmEvidenceSchemaVersion = "llm_evidence.v0.1.1"

type LLMEvidenceBundle struct {
	SchemaVersion   string                             `json:"schema_version"`
	RunID           string                             `json:"run_id"`
	GeneratedAt     time.Time                          `json:"generated_at"`
	Selection       contracts.AnalysisSelection        `json:"selection,omitempty"`
	Summary         contracts.AnalysisSummary          `json:"summary"`
	TraceQuality    contracts.AnalysisTraceQuality     `json:"trace_quality"`
	Subjects        []LLMEvidenceSubject               `json:"subjects"`
	RuleFindings    []contracts.AnalysisFinding        `json:"rule_findings"`
	Recommendations []contracts.AnalysisRecommendation `json:"recommendations"`
	Evidence        []LLMEvidenceItem                  `json:"evidence"`
}

type LLMEvidenceSubject struct {
	SubjectID           string   `json:"subject_id"`
	SampleID            string   `json:"sample_id"`
	AgentFramework      string   `json:"agent_framework,omitempty"`
	AgentModel          string   `json:"agent_model,omitempty"`
	SkillName           string   `json:"skill_name,omitempty"`
	Language            string   `json:"language"`
	CompilePass         bool     `json:"compile_pass"`
	TestPass            *bool    `json:"test_pass,omitempty"`
	LineCoverage        *float64 `json:"line_coverage,omitempty"`
	MutationScore       *float64 `json:"mutation_score,omitempty"`
	TraceStepCount      int      `json:"trace_step_count"`
	HasSourceRead       bool     `json:"has_source_read"`
	HasTestWrite        bool     `json:"has_test_write"`
	HasTestExecution    bool     `json:"has_test_execution"`
	ModifiedSource      bool     `json:"modified_source"`
	ModifiedSourcePaths []string `json:"modified_source_paths,omitempty"`
	RuntimeNoiseCount   int      `json:"runtime_noise_count"`
	PolicyCommandCount  int      `json:"policy_command_count"`
	TotalTokens         *int     `json:"total_tokens,omitempty"`
	EvidenceIDs         []string `json:"evidence_ids,omitempty"`
	FailureSummary      string   `json:"failure_summary,omitempty"`
	SelectedReason      []string `json:"selected_reason,omitempty"`
	ComparisonGroup     string   `json:"comparison_group,omitempty"`
	GeneratedTestPath   string   `json:"generated_test_path,omitempty"`
	TrajectoryPath      string   `json:"trajectory_path,omitempty"`
	WorkspaceDiffPath   string   `json:"workspace_diff_path,omitempty"`
}

type LLMEvidenceItem struct {
	EvidenceID string `json:"evidence_id"`
	Kind       string `json:"kind"`
	SubjectID  string `json:"subject_id,omitempty"`
	SampleID   string `json:"sample_id,omitempty"`
	Path       string `json:"path,omitempty"`
	StepIndex  int    `json:"step_index,omitempty"`
	Title      string `json:"title"`
	Excerpt    string `json:"excerpt"`
}

func buildLLMEvidenceBundle(outputRoot string, report *contracts.AnalysisReport) (LLMEvidenceBundle, map[string]contracts.EvidenceRef) {
	bundle := LLMEvidenceBundle{
		SchemaVersion:   llmEvidenceSchemaVersion,
		RunID:           report.RunID,
		GeneratedAt:     time.Now().UTC(),
		Selection:       report.Selection,
		Summary:         report.Summary,
		TraceQuality:    report.TraceQuality,
		RuleFindings:    compactFindingsForEvidence(report.Findings),
		Recommendations: compactRecommendationsForEvidence(report.Recommendations),
	}
	evidenceMap := map[string]contracts.EvidenceRef{}
	nextID := 1
	addEvidence := func(kind, title, subjectID, sampleID, path string, stepIndex int, excerpt string) string {
		excerpt = trim(excerpt, 1400)
		if strings.TrimSpace(excerpt) == "" {
			return ""
		}
		id := fmt.Sprintf("ev-%03d", nextID)
		nextID++
		item := LLMEvidenceItem{
			EvidenceID: id,
			Kind:       kind,
			SubjectID:  subjectID,
			SampleID:   sampleID,
			Path:       path,
			StepIndex:  stepIndex,
			Title:      title,
			Excerpt:    excerpt,
		}
		bundle.Evidence = append(bundle.Evidence, item)
		evidenceMap[id] = contracts.EvidenceRef{
			EvidenceID: id,
			Kind:       kind,
			Path:       path,
			SubjectID:  subjectID,
			SampleID:   sampleID,
			StepIndex:  stepIndex,
			Excerpt:    excerpt,
		}
		return id
	}

	selected := selectEvidenceSubjects(report.Subjects, report.Selection)
	for _, subject := range selected {
		reasons := evidenceSelectionReasons(subject)
		if isUserSelectedSubject(subject, report.Selection) {
			reasons = append([]string{"user_selected"}, reasons...)
		}
		view := LLMEvidenceSubject{
			SubjectID:           subject.SubjectID,
			SampleID:            subject.SampleID,
			AgentFramework:      subject.AgentFramework,
			AgentModel:          subject.AgentModel,
			SkillName:           subject.SkillName,
			Language:            subject.Language,
			CompilePass:         subject.CompilePass,
			TestPass:            subject.TestPass,
			LineCoverage:        subject.LineCoverage,
			MutationScore:       subject.MutationScore,
			TraceStepCount:      subject.TraceStepCount,
			HasSourceRead:       subject.HasSourceRead,
			HasTestWrite:        subject.HasTestWrite,
			HasTestExecution:    subject.HasTestExecution,
			ModifiedSource:      subject.ModifiedSource,
			ModifiedSourcePaths: subject.ModifiedSourcePaths,
			RuntimeNoiseCount:   subject.RuntimeNoiseCount,
			PolicyCommandCount:  subject.PolicyCommandCount,
			TotalTokens:         subject.TotalTokens,
			GeneratedTestPath:   subject.GeneratedTestPath,
			TrajectoryPath:      subject.TrajectoryPath,
			WorkspaceDiffPath:   subject.WorkspaceDiffPath,
			SelectedReason:      compactStringList(reasons),
		}
		if report.Selection.CompareMode && len(report.Selection.SelectedSubjects) > 1 {
			view.ComparisonGroup = "selected"
		}
		view.FailureSummary = failureSummaryForSubject(report.Findings, subject)
		view.EvidenceIDs = append(view.EvidenceIDs, addEvidence("metrics", "评测指标摘要", subject.SubjectID, subject.SampleID, "", 0, subjectMetricsExcerpt(subject, view.FailureSummary)))

		for _, step := range selectKeySteps(subject.Trajectory) {
			excerpt := strings.TrimSpace(strings.Join(nonEmptyStrings(step.TextExcerpt, step.InputExcerpt, step.OutputExcerpt), "\n\n"))
			view.EvidenceIDs = append(view.EvidenceIDs, addEvidence("trajectory_step", stepEvidenceTitle(step), subject.SubjectID, subject.SampleID, subject.TrajectoryPath, step.Index, excerpt))
		}
		if changes := readWorkspaceChanges(resolvePath(outputRoot, subject.WorkspaceDiffPath)); len(changes) > 0 {
			view.EvidenceIDs = append(view.EvidenceIDs, addEvidence("workspace_diff", "workspace diff 摘要", subject.SubjectID, subject.SampleID, subject.WorkspaceDiffPath, 0, strings.Join(limitStrings(changes, 30), "\n")))
		}
		if snippet := readSnippet(resolvePath(outputRoot, subject.GeneratedTestPath), 1800); snippet != "" {
			view.EvidenceIDs = append(view.EvidenceIDs, addEvidence("generated_test", "生成测试片段", subject.SubjectID, subject.SampleID, subject.GeneratedTestPath, 0, snippet))
		}
		view.EvidenceIDs = compactStringList(view.EvidenceIDs)
		bundle.Subjects = append(bundle.Subjects, view)
	}
	return bundle, evidenceMap
}

func selectEvidenceSubjects(subjects []contracts.AnalysisSubject, selection contracts.AnalysisSelection) []contracts.AnalysisSubject {
	if len(selection.SelectedSubjects) > 0 {
		byKey := map[string]contracts.AnalysisSubject{}
		for _, subject := range subjects {
			byKey[analysisSubjectKey(subject.SubjectID, subject.SampleID, subject.Language)] = subject
		}
		selected := make([]contracts.AnalysisSubject, 0, len(selection.SelectedSubjects))
		for _, item := range selection.SelectedSubjects {
			if subject, ok := byKey[analysisSubjectKey(item.SubjectID, item.SampleID, item.Language)]; ok {
				selected = append(selected, subject)
			}
		}
		return selected
	}
	var selected []contracts.AnalysisSubject
	seenAgent := map[string]bool{}
	for _, subject := range subjects {
		agentKey := firstNonEmpty(subject.SubjectID, subject.Model, subject.AgentFramework)
		need := len(evidenceSelectionReasons(subject)) > 0
		if !seenAgent[agentKey] {
			need = true
			seenAgent[agentKey] = true
		}
		if need {
			selected = append(selected, subject)
		}
	}
	if len(selected) > 12 {
		selected = selected[:12]
	}
	return selected
}

func isUserSelectedSubject(subject contracts.AnalysisSubject, selection contracts.AnalysisSelection) bool {
	for _, item := range selection.SelectedSubjects {
		if analysisSubjectKey(subject.SubjectID, subject.SampleID, subject.Language) == analysisSubjectKey(item.SubjectID, item.SampleID, item.Language) {
			return true
		}
	}
	return false
}

func evidenceSelectionReasons(subject contracts.AnalysisSubject) []string {
	var out []string
	if !subject.CompilePass {
		out = append(out, "compile_failed")
	}
	if subject.TestPass == nil {
		out = append(out, "test_not_executed")
	} else if !*subject.TestPass {
		out = append(out, "test_failed")
	}
	if subject.LineCoverage != nil && normMetric(*subject.LineCoverage) < 0.7 {
		out = append(out, "low_coverage")
	}
	if subject.MutationScore == nil || normMetric(*subject.MutationScore) < 0.6 {
		out = append(out, "low_or_missing_mutation")
	}
	if subject.TraceStepCount == 0 {
		out = append(out, "missing_trajectory")
	}
	if subject.ModifiedSource {
		out = append(out, "source_modified")
	}
	if subject.PolicyCommandCount > 0 {
		out = append(out, "policy_command")
	}
	if subject.TotalTokens != nil && *subject.TotalTokens > 200000 {
		out = append(out, "high_token_usage")
	}
	return out
}

func selectKeySteps(steps []contracts.AnalysisTrajectoryStep) []contracts.AnalysisTrajectoryStep {
	var out []contracts.AnalysisTrajectoryStep
	for _, step := range steps {
		if isKeyStep(step) {
			out = append(out, step)
		}
		if len(out) >= 14 {
			return out
		}
	}
	if len(out) == 0 && len(steps) > 0 {
		limit := len(steps)
		if limit > 6 {
			limit = 6
		}
		out = append(out, steps[:limit]...)
	}
	return out
}

func isKeyStep(step contracts.AnalysisTrajectoryStep) bool {
	text := strings.ToLower(strings.Join(nonEmptyStrings(step.Kind, step.Tool, step.TextExcerpt, step.InputExcerpt, step.OutputExcerpt), "\n"))
	if containsAny(text, []string{"read", "write", "edit", "bash", "command", "pytest", "go test", "mvn", "ctest", "policy", "violation", "error", "failed", "exception", "pip install", "apt-get"}) {
		return true
	}
	if step.Success != nil && !*step.Success {
		return true
	}
	if step.ExitCode != nil && *step.ExitCode != 0 {
		return true
	}
	return false
}

func stepEvidenceTitle(step contracts.AnalysisTrajectoryStep) string {
	name := firstNonEmpty(step.Tool, step.Kind, step.Role, "step")
	if step.ExitCode != nil && *step.ExitCode != 0 {
		return fmt.Sprintf("%s 失败 exit=%d", name, *step.ExitCode)
	}
	if step.Success != nil && !*step.Success {
		return name + " 失败"
	}
	return name
}

func subjectMetricsExcerpt(subject contracts.AnalysisSubject, failure string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "subject=%s sample=%s agent=%s skill=%s language=%s\n", subject.SubjectID, subject.SampleID, subject.AgentFramework, subject.SkillName, subject.Language)
	fmt.Fprintf(&b, "compile_pass=%v test_pass=%s line_coverage=%s mutation_score=%s trace_steps=%d\n", subject.CompilePass, boolPtrText(subject.TestPass), metricPtrText(subject.LineCoverage), metricPtrText(subject.MutationScore), subject.TraceStepCount)
	fmt.Fprintf(&b, "has_source_read=%v has_test_write=%v has_test_execution=%v modified_source=%v policy_commands=%d runtime_noise=%d\n", subject.HasSourceRead, subject.HasTestWrite, subject.HasTestExecution, subject.ModifiedSource, subject.PolicyCommandCount, subject.RuntimeNoiseCount)
	if subject.TotalTokens != nil {
		fmt.Fprintf(&b, "total_tokens=%d\n", *subject.TotalTokens)
	}
	if failure != "" {
		fmt.Fprintf(&b, "related_findings=%s\n", failure)
	}
	return b.String()
}

func failureSummaryForSubject(findings []contracts.AnalysisFinding, subject contracts.AnalysisSubject) string {
	var lines []string
	for _, finding := range findings {
		if finding.SubjectID == subject.SubjectID && finding.SampleID == subject.SampleID {
			lines = append(lines, fmt.Sprintf("[%s/%s] %s: %s", finding.Severity, finding.Category, finding.Title, trim(finding.Detail, 220)))
		}
	}
	return strings.Join(limitStrings(lines, 8), "\n")
}

func readSnippet(path string, max int) string {
	if strings.TrimSpace(path) == "" {
		return ""
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return trim(string(raw), max)
}

func compactFindingsForEvidence(items []contracts.AnalysisFinding) []contracts.AnalysisFinding {
	out := make([]contracts.AnalysisFinding, 0, len(items))
	for _, item := range items {
		item.Detail = trim(item.Detail, 500)
		item.Recommendation = trim(item.Recommendation, 260)
		if len(item.Evidence) > 3 {
			item.Evidence = item.Evidence[:3]
		}
		out = append(out, item)
	}
	return out
}

func compactRecommendationsForEvidence(items []contracts.AnalysisRecommendation) []contracts.AnalysisRecommendation {
	out := make([]contracts.AnalysisRecommendation, 0, len(items))
	for _, item := range items {
		item.Detail = trim(item.Detail, 500)
		if len(item.Evidence) > 4 {
			item.Evidence = item.Evidence[:4]
		}
		out = append(out, item)
	}
	return out
}

func boolPtrText(v *bool) string {
	if v == nil {
		return "null"
	}
	if *v {
		return "true"
	}
	return "false"
}

func metricPtrText(v *float64) string {
	if v == nil {
		return "null"
	}
	return fmt.Sprintf("%.2f", *v)
}

func nonEmptyStrings(values ...string) []string {
	out := make([]string, 0, len(values))
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			out = append(out, strings.TrimSpace(v))
		}
	}
	return out
}

func limitStrings(values []string, max int) []string {
	if len(values) <= max {
		return values
	}
	return values[:max]
}

func compactStringList(values []string) []string {
	out := values[:0]
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			out = append(out, v)
		}
	}
	return out
}

func evidenceRefFromPath(kind, path, subjectID, sampleID string) contracts.EvidenceRef {
	return contracts.EvidenceRef{
		Kind:      kind,
		Path:      filepath.ToSlash(path),
		SubjectID: subjectID,
		SampleID:  sampleID,
	}
}
