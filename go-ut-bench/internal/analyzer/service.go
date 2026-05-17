package analyzer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"go-ut-bench/internal/contracts"
	"go-ut-bench/internal/runner"
)

const promptVersion = "analysis-prompt.v0.1.1"

type Options struct {
	RunID            string
	OutputRoot       string
	ConfigPath       string
	LLMEnabled       bool
	LLMModel         string
	Force            bool
	SelectedSubjects []contracts.AnalysisSubjectSelector
	CompareMode      bool
	Progress         func(phase string)
}

type Service struct {
	llm LLMClient
}

type LLMClient interface {
	Analyze(ctx context.Context, req LLMRequest) (contracts.LLMAnalysisResult, error)
}

type LLMRequest struct {
	ConfigPath string
	ModelName  string
	Prompt     string
}

func NewService() *Service {
	return &Service{llm: &HTTPClient{}}
}

func NewServiceWithLLM(llm LLMClient) *Service {
	return &Service{llm: llm}
}

func reportProgress(fn func(string), phase string) {
	if fn != nil && strings.TrimSpace(phase) != "" {
		fn(phase)
	}
}

func (s *Service) Analyze(ctx context.Context, opts Options) (*contracts.AnalysisReport, error) {
	reportProgress(opts.Progress, "准备证据")
	opts.RunID = strings.TrimSpace(opts.RunID)
	if opts.RunID == "" {
		return nil, errors.New("run id is required")
	}
	if strings.TrimSpace(opts.OutputRoot) == "" {
		opts.OutputRoot = "./artifacts"
	}
	runDir := filepath.Join(opts.OutputRoot, "runs", opts.RunID)
	analysisDir := filepath.Join(runDir, "analysis")
	analysisPath := filepath.Join(analysisDir, "analysis_report.json")
	if !opts.Force {
		if report, err := ReadReport(analysisPath); err == nil {
			return report, nil
		}
	}

	evalPath := filepath.Join(runDir, "evaluation", "evaluation_result.json")
	var eval contracts.EvaluationResultSet
	if err := readJSON(evalPath, &eval); err != nil {
		return nil, fmt.Errorf("read evaluation result: %w", err)
	}

	reportProgress(opts.Progress, "规则分析")
	manifestPath := filepath.Join(runDir, "generated", "generated_manifest.json")
	var manifest contracts.GeneratedManifest
	_ = readJSON(manifestPath, &manifest)
	generatedByKey := map[string]contracts.GeneratedCase{}
	for _, c := range manifest.Cases {
		generatedByKey[resultKey(c.SubjectID, c.Model, c.Language, c.SampleID)] = c
	}

	reportPath := filepath.Join(runDir, "report", "report_summary.json")
	report := &contracts.AnalysisReport{
		SchemaVersion: contracts.AnalysisSchemaVersion,
		RunID:         opts.RunID,
		GeneratedAt:   time.Now().UTC(),
		SourceFiles: contracts.AnalysisSourceFiles{
			ManifestPath:   manifestPath,
			EvaluationPath: evalPath,
			ReportPath:     reportPath,
		},
		LLMStatus: contracts.LLMAnalysisStatus{
			Enabled: opts.LLMEnabled,
			Status:  mapBool(opts.LLMEnabled, "skipped", "disabled"),
			Model:   opts.LLMModel,
		},
	}

	var findings []contracts.AnalysisFinding
	for _, res := range eval.Results {
		gen := generatedByKey[resultKey(res.SubjectID, res.Model, res.Language, res.SampleID)]
		subject, subjectFindings := s.analyzeResult(opts.OutputRoot, res, gen)
		report.Subjects = append(report.Subjects, subject)
		findings = append(findings, subjectFindings...)
		accumulateTraceQuality(&report.TraceQuality, subject)
	}
	report.TraceQuality.TotalSubjects = len(report.Subjects)
	report.Findings = append(report.Findings, findings...)
	report.Recommendations = buildRecommendations(report.Findings, report.Subjects)
	report.Summary = buildSummary(report)

	selection, err := validateAnalysisSelection(report.Subjects, opts.SelectedSubjects, opts.CompareMode)
	if err != nil {
		return nil, err
	}
	report.Selection = selection

	if opts.LLMEnabled {
		llmResult, evidenceCount, evidenceSubjectCount := s.runLLM(ctx, opts, report, analysisDir)
		report.LLM = &llmResult
		report.LLMStatus.Status = firstNonEmpty(llmResult.Status, "degraded")
		report.LLMStatus.Model = firstNonEmpty(llmResult.Model, opts.LLMModel)
		report.LLMStatus.EvidenceCount = evidenceCount
		report.LLMStatus.EvidenceSubjectCount = evidenceSubjectCount
		if llmResult.Error != "" {
			report.LLMStatus.Message = llmResult.Error
		}
		report.Findings = append(report.Findings, llmResult.Findings...)
		report.Recommendations = append(report.Recommendations, llmResult.Recommendations...)
	}

	report.Findings = dedupeFindings(report.Findings)
	report.Recommendations = dedupeRecommendations(report.Recommendations)
	sortFindings(report.Findings)
	sortRecommendations(report.Recommendations)
	report.EvidenceIndex = buildEvidenceIndex(opts.OutputRoot, report)
	report.RootCauses = buildRootCauses(report)
	report.ComparisonSummary = buildComparisonSummary(report)
	report.Summary = buildSummary(report)

	reportProgress(opts.Progress, "写入报告")
	if err := os.MkdirAll(analysisDir, 0o755); err != nil {
		return nil, err
	}
	if err := contracts.WriteJSON(analysisPath, report); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(analysisDir, "analysis_report.md"), []byte(renderMarkdown(report)), 0o644); err != nil {
		return nil, err
	}
	return report, nil
}

func validateAnalysisSelection(subjects []contracts.AnalysisSubject, selected []contracts.AnalysisSubjectSelector, compareMode bool) (contracts.AnalysisSelection, error) {
	out := contracts.AnalysisSelection{CompareMode: compareMode}
	if len(selected) == 0 {
		return out, nil
	}
	if len(selected) > 3 {
		return out, fmt.Errorf("selected_subjects supports at most 3 items, got %d", len(selected))
	}
	available := map[string]contracts.AnalysisSubject{}
	for _, subject := range subjects {
		available[analysisSubjectKey(subject.SubjectID, subject.SampleID, subject.Language)] = subject
	}
	seen := map[string]bool{}
	for _, item := range selected {
		selector := contracts.AnalysisSubjectSelector{
			SubjectID: strings.TrimSpace(item.SubjectID),
			SampleID:  strings.TrimSpace(item.SampleID),
			Language:  strings.TrimSpace(item.Language),
		}
		key := analysisSubjectKey(selector.SubjectID, selector.SampleID, selector.Language)
		if selector.SubjectID == "" || selector.SampleID == "" || selector.Language == "" {
			return out, fmt.Errorf("selected_subjects contains empty subject_id/sample_id/language")
		}
		if _, ok := available[key]; !ok {
			return out, fmt.Errorf("selected subject not found: subject_id=%s sample_id=%s language=%s", selector.SubjectID, selector.SampleID, selector.Language)
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		out.SelectedSubjects = append(out.SelectedSubjects, selector)
	}
	if len(out.SelectedSubjects) > 1 {
		out.CompareMode = true
	}
	return out, nil
}

func analysisSubjectKey(subjectID, sampleID, language string) string {
	return strings.Join([]string{strings.TrimSpace(subjectID), strings.TrimSpace(sampleID), strings.TrimSpace(language)}, "\x00")
}

func (s *Service) analyzeResult(outputRoot string, res contracts.EvaluationResult, gen contracts.GeneratedCase) (contracts.AnalysisSubject, []contracts.AnalysisFinding) {
	subjectID := firstNonEmpty(res.SubjectID, gen.SubjectID, res.Model)
	subject := contracts.AnalysisSubject{
		SubjectID:         subjectID,
		Model:             res.Model,
		AgentFramework:    firstNonEmpty(res.AgentFramework, gen.AgentFramework),
		AgentModel:        firstNonEmpty(res.AgentModel, gen.AgentModel),
		SkillName:         firstNonEmpty(res.SkillName, gen.SkillName),
		Language:          res.Language,
		SampleID:          res.SampleID,
		CompilePass:       res.CompilePass,
		TestPass:          res.TestPass,
		LineCoverage:      res.LineCoverage,
		BranchCoverage:    res.BranchCoverage,
		MutationScore:     res.MutationScore,
		LatencyMS:         res.LatencyMS,
		TotalTokens:       res.TotalTokens,
		TrajectoryPath:    firstNonEmpty(res.TrajectoryPath, gen.TrajectoryPath),
		RawTracePath:      firstNonEmpty(res.RawTracePath, gen.RawTracePath),
		WorkspaceDiffPath: firstNonEmpty(res.WorkspaceDiffPath, gen.WorkspaceDiffPath),
		GeneratedTestPath: firstNonEmpty(res.GeneratedTestPath, gen.GeneratedTestPath),
		SourcePath:        firstNonEmpty(res.SourcePath, gen.SamplePath),
	}
	subject.TrajectoryPath = inferSiblingArtifactPath(outputRoot, subject.TrajectoryPath, subject.WorkspaceDiffPath, subject.SampleID, ".trajectory.json")
	subject.RawTracePath = inferSiblingArtifactPath(outputRoot, subject.RawTracePath, subject.WorkspaceDiffPath, subject.SampleID, ".raw_trace.jsonl")

	if subject.TrajectoryPath != "" {
		if traj, err := readTrajectory(resolvePath(outputRoot, subject.TrajectoryPath)); err == nil {
			subject.Trajectory = summarizeSteps(traj.Steps, 160)
			subject.TraceStepCount = len(traj.Steps)
			for _, step := range traj.Steps {
				analyzeStep(&subject, step, res)
			}
		}
	}
	if subject.WorkspaceDiffPath != "" {
		if changes := readWorkspaceChanges(resolvePath(outputRoot, subject.WorkspaceDiffPath)); len(changes) > 0 {
			for _, change := range changes {
				classifyWorkspaceChange(&subject, change)
			}
		}
	}
	if subject.GeneratedTestPath != "" {
		if _, err := os.Stat(resolvePath(outputRoot, subject.GeneratedTestPath)); err == nil {
			subject.HasTestWrite = true
		}
	}

	return subject, buildRuleFindings(res, subject)
}

func summarizeSteps(steps []runner.TrajectoryStep, max int) []contracts.AnalysisTrajectoryStep {
	if len(steps) > max {
		steps = steps[:max]
	}
	out := make([]contracts.AnalysisTrajectoryStep, 0, len(steps))
	for _, step := range steps {
		out = append(out, contracts.AnalysisTrajectoryStep{
			Index:         step.Index,
			Kind:          step.Kind,
			Role:          step.Role,
			Tool:          step.Tool,
			Success:       step.Success,
			ExitCode:      step.ExitCode,
			DurationMS:    step.DurationMS,
			Source:        step.Source,
			TextExcerpt:   trim(step.Text, 600),
			InputExcerpt:  trim(anyText(step.Input), 600),
			OutputExcerpt: trim(anyText(step.Output), 600),
		})
	}
	return out
}

func analyzeStep(subject *contracts.AnalysisSubject, step runner.TrajectoryStep, res contracts.EvaluationResult) {
	if step.Kind == "tool_call" || step.Kind == "command" {
		subject.ToolCallCount++
	}
	tool := strings.ToLower(step.Tool)
	text := strings.ToLower(step.Text + "\n" + anyText(step.Input) + "\n" + anyText(step.Output))
	sourceBase := strings.ToLower(filepath.Base(firstNonEmpty(res.SourcePath, subject.SourcePath)))
	if strings.Contains(tool, "read") || strings.Contains(text, "read_file") || strings.Contains(text, "cat ") || strings.Contains(text, "file_path") || strings.Contains(text, "filepath") {
		if sourceBase == "" || strings.Contains(text, sourceBase) || strings.Contains(text, "module_under_test") || strings.Contains(text, "/workspace/") {
			subject.HasSourceRead = true
		}
	}
	if strings.Contains(tool, "write") || strings.Contains(tool, "edit") || strings.Contains(text, "generated_test") || strings.Contains(text, "_test.") || strings.Contains(text, "test_") {
		subject.HasTestWrite = true
	}
	if !containsAny(text, []string{"pip install", "apt-get", "npm install"}) && containsAny(text, []string{"pytest", "go test", "mvn test", "mvn -q test", "ctest", "gradle test", "npm test"}) {
		subject.HasTestExecution = true
	}
	if containsAny(text, []string{"pip install", "apt-get", "yum install", "npm install -g", "curl |", "wget "}) {
		subject.PolicyCommandCount++
	}
}

func classifyWorkspaceChange(subject *contracts.AnalysisSubject, change string) {
	clean := filepath.ToSlash(strings.TrimSpace(change))
	lower := strings.ToLower(clean)
	if clean == "" {
		return
	}
	if isRuntimeNoise(lower) {
		subject.RuntimeNoiseCount++
		return
	}
	sourceBase := strings.ToLower(filepath.Base(subject.SourcePath))
	if sourceBase != "" && strings.HasSuffix(lower, sourceBase) {
		// 边界检查：确保 sourceBase 出现在路径分隔符之后或路径开头，
		// 避免 test_sample.py 误匹配 sourceBase=sample.py 的情况。
		idx := len(lower) - len(sourceBase)
		if idx == 0 || lower[idx-1] == '/' {
			subject.ModifiedSource = true
			subject.ModifiedSourcePaths = appendUniqueString(subject.ModifiedSourcePaths, clean)
			return
		}
	}
	if strings.Contains(lower, "generated_test") || strings.HasSuffix(lower, "_test.go") || strings.HasSuffix(lower, "test.java") || strings.Contains(lower, "test") {
		subject.HasTestWrite = true
	}
}

func buildRuleFindings(res contracts.EvaluationResult, subject contracts.AnalysisSubject) []contracts.AnalysisFinding {
	var findings []contracts.AnalysisFinding
	add := func(sev, cat, title, detail, rec string, ev ...contracts.EvidenceRef) {
		findings = append(findings, contracts.AnalysisFinding{
			ID:             fmt.Sprintf("rule-%03d", len(findings)+1),
			Severity:       sev,
			Category:       cat,
			SubjectID:      subject.SubjectID,
			SampleID:       subject.SampleID,
			Title:          title,
			Detail:         detail,
			Recommendation: rec,
			Confidence:     0.9,
			Source:         "rule",
			Evidence:       ev,
		})
	}
	baseEv := contracts.EvidenceRef{Kind: "evaluation", SubjectID: subject.SubjectID, SampleID: subject.SampleID}
	if !res.CompilePass {
		add("P0", "compile", "编译失败", firstNonEmpty(res.CompileError, "生成测试未通过编译"), "优先检查生成测试是否引用不存在的符号、缺少 import、测试框架不匹配，或破坏了项目结构。", baseEv)
	}
	if res.TestPass != nil && !*res.TestPass {
		add("P0", "test", "测试执行失败", firstNonEmpty(res.TestError, "生成测试运行失败"), "要求 agent 在提交前运行目标测试，并把失败输出作为修正依据。", baseEv)
	}
	if res.TestPass == nil {
		add("P1", "test", "测试未执行", "评测结果中 test_pass 为空，说明测试阶段没有得到有效结果。", "检查编译阶段、测试发现规则和 evaluator 日志。", baseEv)
	}
	if subject.LineCoverage != nil && normMetric(*subject.LineCoverage) < 0.7 {
		add("P1", "coverage", "行覆盖率偏低", fmt.Sprintf("当前行覆盖率为 %.1f%%。", normMetric(*subject.LineCoverage)*100), "补充正常路径、边界路径和异常路径断言。", baseEv)
	}
	if subject.MutationScore == nil {
		add("P1", "mutation", "缺少变异测试得分", firstNonEmpty(res.MutationError, "评测结果没有 mutation_score。"), "确认变异测试工具链可用，并检查生成测试是否能稳定运行。", baseEv)
	} else if normMetric(*subject.MutationScore) < 0.6 {
		title := "变异得分偏低"
		detail := fmt.Sprintf("当前变异得分为 %.1f%%。", normMetric(*subject.MutationScore)*100)
		if strings.TrimSpace(res.MutationError) != "" {
			title = "变异测试跳过或失败"
			detail += " mutation 工具反馈：" + trim(res.MutationError, 240)
		}
		add("P1", "mutation", title, detail, "增加对关键分支、边界条件和错误消息的强断言，并先确保 baseline 测试全量通过。", baseEv)
	}
	if subject.TraceStepCount == 0 {
		add("P1", "trace", "缺少 step-by-step trajectory", "没有读取到统一 trajectory，无法还原 agent 的逐步执行过程。", "优先修复对应 agent 的原始 trace 导出和 trajectory 适配。", evidenceRefFromPath("trajectory", subject.TrajectoryPath, subject.SubjectID, subject.SampleID))
	}
	if subject.TraceStepCount > 0 && !subject.HasSourceRead {
		add("P2", "trace", "未观察到源码读取步骤", "trajectory 中没有明确的源码读取行为。", "约束 agent 先读取目标源码，再生成测试。", evidenceRefFromPath("trajectory", subject.TrajectoryPath, subject.SubjectID, subject.SampleID))
	}
	if subject.TraceStepCount > 0 && !subject.HasTestExecution {
		add("P2", "trace", "未观察到测试执行步骤", "trajectory 中没有发现 pytest/go test/mvn test 等本地验证命令。", "要求 agent 写完测试后运行最小验证命令，并根据失败结果迭代。", evidenceRefFromPath("trajectory", subject.TrajectoryPath, subject.SubjectID, subject.SampleID))
	}
	if subject.ModifiedSource {
		detail := "workspace diff 显示 agent 修改了原始业务源码"
		if len(subject.ModifiedSourcePaths) > 0 {
			detail += "：" + strings.Join(subject.ModifiedSourcePaths, "、")
		}
		if !res.CompilePass || (res.TestPass != nil && !*res.TestPass) || subject.MutationScore == nil || (subject.MutationScore != nil && normMetric(*subject.MutationScore) == 0) {
			detail += "。本结果同时存在编译、测试或变异阶段失败，失败根因需要优先按源码污染排查。"
		} else {
			detail += "。即使本次指标通过，也应视为无效行为，因为评测应只接受生成测试文件。"
		}
		add("P0", "policy", "agent 修改了被测源码", detail, "强化执行约束：只允许写测试文件，不允许改业务源码；验证失败时只能修改生成测试。", evidenceRefFromPath("workspace_diff", subject.WorkspaceDiffPath, subject.SubjectID, subject.SampleID))
	}
	if subject.RuntimeNoiseCount > 0 {
		add("P3", "policy", "产生运行时噪声文件", fmt.Sprintf("workspace diff 中有 %d 个缓存、插件或临时文件。", subject.RuntimeNoiseCount), "继续过滤运行时噪声，必要时清理 agent 工作区后再采集 diff。", evidenceRefFromPath("workspace_diff", subject.WorkspaceDiffPath, subject.SubjectID, subject.SampleID))
	}
	if subject.PolicyCommandCount > 0 {
		add("P2", "policy", "执行了不推荐的环境命令", fmt.Sprintf("trajectory 中发现 %d 次安装或外部下载类命令。", subject.PolicyCommandCount), "在 prompt 和 sandbox 策略中禁止随意安装依赖，除非样本显式需要。", evidenceRefFromPath("trajectory", subject.TrajectoryPath, subject.SubjectID, subject.SampleID))
	}
	if subject.TotalTokens != nil && *subject.TotalTokens > 200000 {
		add("P2", "efficiency", "token 消耗异常偏高", fmt.Sprintf("总 token 为 %d。", *subject.TotalTokens), "检查 agent 是否反复读取无关文件或陷入无效循环。", baseEv)
	}
	return findings
}

func (s *Service) runLLM(ctx context.Context, opts Options, report *contracts.AnalysisReport, analysisDir string) (contracts.LLMAnalysisResult, int, int) {
	if s.llm == nil {
		return contracts.LLMAnalysisResult{Model: opts.LLMModel, PromptVersion: promptVersion, Status: "skipped", Error: "llm client not configured"}, 0, 0
	}
	reportProgress(opts.Progress, "构建证据包")
	bundle, evidenceMap := buildLLMEvidenceBundle(opts.OutputRoot, report)
	_ = contracts.WriteJSON(filepath.Join(analysisDir, "llm_evidence_bundle.json"), bundle)

	prompt, err := buildLLMPrompt(bundle)
	if err != nil {
		return contracts.LLMAnalysisResult{Model: opts.LLMModel, PromptVersion: promptVersion, Status: "degraded", Error: err.Error()}, len(bundle.Evidence), len(bundle.Subjects)
	}
	ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	reportProgress(opts.Progress, "LLM 生成中")
	result, err := s.llm.Analyze(ctx, LLMRequest{ConfigPath: opts.ConfigPath, ModelName: opts.LLMModel, Prompt: prompt})
	if err != nil {
		result = contracts.LLMAnalysisResult{Model: opts.LLMModel, PromptVersion: promptVersion, Status: "degraded", Error: err.Error()}
	} else {
		result.PromptVersion = promptVersion
		if result.Status == "" {
			result.Status = "ok"
		}
	}
	result.Findings = normalizeLLMFindings(result.Findings, evidenceMap)
	result.Recommendations = normalizeLLMRecommendations(result.Recommendations, evidenceMap)

	if strings.TrimSpace(result.RawOutput) != "" {
		_ = os.WriteFile(filepath.Join(analysisDir, "llm_raw_output.txt"), []byte(result.RawOutput), 0o644)
	}
	_ = contracts.WriteJSON(filepath.Join(analysisDir, "llm_diagnosis.json"), result)
	return result, len(bundle.Evidence), len(bundle.Subjects)
}

func buildLLMPrompt(bundle LLMEvidenceBundle) (string, error) {
	raw, err := json.MarshalIndent(bundle, "", "  ")
	if err != nil {
		return "", err
	}
	selectionHint := ""
	selectedCount := len(bundle.Selection.SelectedSubjects)
	if selectedCount == 1 {
		selectionHint = "\n10. 本次是用户定点分析：请围绕 selection.selected_subjects 中的对象解释行为链路、测试质量、trajectory 证据和改进建议，不要把重点转移到未选对象。\n"
	} else if selectedCount > 1 || bundle.Selection.CompareMode {
		selectionHint = "\n10. 本次是用户选择的横向对比：请比较 selection.selected_subjects 中 2-3 个对象的共同问题、差异点、最优/最差表现原因和可迁移策略。不要把重点转移到未选对象。\n"
	}
	return `你是 UTBench 的单元测试 agent 诊断器。请基于 evidence bundle 分析 agent/skill 的弱点，并给出能落地的优化建议。

硬性要求：
1. 只输出合法 JSON，不要输出 markdown、解释或代码块。
2. 所有 finding 和 recommendation 必须尽量引用 evidence_id。没有证据时只能作为低置信度建议。
3. 不要建议本阶段自动修改 skill 或自动复测；本阶段只做诊断与建议。
4. 优先区分 agent 行为问题、skill/prompt 问题、环境契约问题、evaluator 问题。
5. category 只能使用 compile/test/coverage/mutation/trace/policy/efficiency/skill/environment/evaluator/dataset。
6. recommendation.target 只能使用 skill/prompt/agent_config/environment/evaluator/dataset。
7. 不要复述 rule_findings，不要逐条复制规则诊断；只补充规则没有覆盖的归因、跨 agent 对比和优化建议。
8. findings 最多 5 条，recommendations 最多 5 条；每条 detail 控制在 120 字以内，recommendation/detail/expected_impact/risk 都要简短。
9. 如果最重要的问题已经在规则里出现，请在 LLM 中合并为更高层的归因，不要重复同名 finding。
` + selectionHint + `

输出 JSON 格式：
{
  "summary": "一句话总结",
  "findings": [
    {
      "severity": "P1",
      "category": "skill",
      "subject_id": "...",
      "sample_id": "...",
      "title": "...",
      "detail": "...",
      "recommendation": "...",
      "confidence": 0.8,
      "evidence": [{"evidence_id":"ev-001"}]
    }
  ],
  "recommendations": [
    {
      "priority": "P1",
      "category": "skill",
      "target": "prompt",
      "title": "...",
      "detail": "...",
      "expected_impact": "...",
      "risk": "...",
      "applies_to": ["subject_id"],
      "evidence": [{"evidence_id":"ev-001"}]
    }
  ]
}

Evidence bundle:
` + string(raw), nil
}

func buildRecommendations(findings []contracts.AnalysisFinding, subjects []contracts.AnalysisSubject) []contracts.AnalysisRecommendation {
	var out []contracts.AnalysisRecommendation
	add := func(priority, category, target, title, detail, impact, risk string, applies []string, evidence []contracts.EvidenceRef) {
		out = append(out, contracts.AnalysisRecommendation{
			ID:             fmt.Sprintf("rec-%03d", len(out)+1),
			Priority:       priority,
			Category:       category,
			Target:         target,
			Title:          title,
			Detail:         detail,
			ExpectedImpact: impact,
			Risk:           risk,
			AppliesTo:      applies,
			Source:         "rule",
			Evidence:       evidence,
		})
	}
	byCategory := map[string]int{}
	applies := map[string]map[string]bool{}
	evidenceByCategory := map[string][]contracts.EvidenceRef{}
	for _, f := range findings {
		byCategory[f.Category]++
		if applies[f.Category] == nil {
			applies[f.Category] = map[string]bool{}
		}
		if f.SubjectID != "" {
			applies[f.Category][f.SubjectID] = true
		}
		evidenceByCategory[f.Category] = append(evidenceByCategory[f.Category], f.Evidence...)
	}
	if byCategory["trace"] > 0 {
		add("P1", "trace", "agent_config", "把 agent 执行过程纳入强约束", "对没有源码读取或测试执行的 agent，要求先读源码、再写测试、最后运行最小验证命令。", "提高 trajectory 可解释性，并减少未验证测试进入评测阶段。", "部分 agent 原生 trace 能力有限，需要继续补适配。", sortedSet(applies["trace"]), evidenceByCategory["trace"])
	}
	if byCategory["policy"] > 0 {
		add("P1", "policy", "environment", "收紧 sandbox 和 prompt 约束", "禁止修改业务源码和随意安装依赖；diff 只保留测试文件和必要产物。", "减少环境污染和不可复现失败。", "过强约束可能拦截确实需要额外依赖的项目级样本，需要按样本声明放行。", sortedSet(applies["policy"]), evidenceByCategory["policy"])
	}
	if byCategory["mutation"] > 0 || byCategory["coverage"] > 0 {
		add("P2", "skill", "prompt", "围绕覆盖率和变异分优化单测策略", "让 skill 显式枚举边界条件、异常路径和关键分支，并要求断言具体返回值或异常消息。", "提升覆盖率和变异杀死率，减少只有烟测的测试。", "更强断言可能暴露源码行为理解错误，需要结合本地测试迭代。", mergeApplies(applies["mutation"], applies["coverage"]), append(evidenceByCategory["mutation"], evidenceByCategory["coverage"]...))
	}
	if len(out) == 0 && len(subjects) > 0 {
		add("P3", "skill", "skill", "保留当前生成策略并扩大样本验证", "本次规则诊断没有发现高优先级问题，下一步应在更多语言、场景和项目级样本上验证稳定性。", "确认当前策略不是只对小样本有效。", "样本不足时结论置信度有限。", nil, nil)
	}
	return out
}

func buildEvidenceIndex(outputRoot string, report *contracts.AnalysisReport) []contracts.AnalysisEvidenceItem {
	var out []contracts.AnalysisEvidenceItem
	add := func(id, kind, title string, subject contracts.AnalysisSubject, path string, stepIndex int, excerpt string) {
		if strings.TrimSpace(id) == "" {
			id = fmt.Sprintf("ev-%03d", len(out)+1)
		}
		out = append(out, contracts.AnalysisEvidenceItem{
			EvidenceID: id,
			Kind:       kind,
			Title:      title,
			SubjectID:  subject.SubjectID,
			SampleID:   subject.SampleID,
			Language:   subject.Language,
			Path:       path,
			StepIndex:  stepIndex,
			Excerpt:    trim(excerpt, 1200),
		})
	}
	subjects := map[string]contracts.AnalysisSubject{}
	for _, subject := range report.Subjects {
		subjects[analysisSubjectKey(subject.SubjectID, subject.SampleID, subject.Language)] = subject
		add("subject-"+safeEvidenceID(subject.SubjectID+"-"+subject.SampleID+"-"+subject.Language), "evaluation", "对象指标摘要", subject, "", 0, subjectEvidenceExcerpt(subject))
		if subject.WorkspaceDiffPath != "" {
			changes := readWorkspaceChanges(resolvePath(outputRoot, subject.WorkspaceDiffPath))
			if len(changes) > 0 {
				add("diff-"+safeEvidenceID(subject.SubjectID+"-"+subject.SampleID+"-"+subject.Language), "workspace_diff", "workspace diff 摘要", subject, subject.WorkspaceDiffPath, 0, strings.Join(limitStrings(changes, 30), "\n"))
			}
		}
		for _, step := range selectKeyAnalysisSteps(subject.Trajectory, 6) {
			excerpt := strings.TrimSpace(strings.Join(nonEmptyStrings(step.TextExcerpt, step.InputExcerpt, step.OutputExcerpt), "\n\n"))
			add(fmt.Sprintf("step-%s-%d", safeEvidenceID(subject.SubjectID+"-"+subject.SampleID+"-"+subject.Language), step.Index), "trajectory_step", stepEvidenceTitleForAnalysis(step), subject, subject.TrajectoryPath, step.Index, excerpt)
		}
		if snippet := readSnippet(resolvePath(outputRoot, subject.GeneratedTestPath), 1000); snippet != "" {
			add("test-"+safeEvidenceID(subject.SubjectID+"-"+subject.SampleID+"-"+subject.Language), "generated_test", "生成测试片段", subject, subject.GeneratedTestPath, 0, snippet)
		}
	}
	for _, finding := range report.Findings {
		subject := subjects[analysisSubjectKey(finding.SubjectID, finding.SampleID, subjectLanguageForFinding(report.Subjects, finding))]
		add("finding-"+safeEvidenceID(finding.ID), "finding", finding.Title, subject, "", 0, finding.Detail)
	}
	return out
}

func buildRootCauses(report *contracts.AnalysisReport) []contracts.AnalysisRootCause {
	type bucket struct {
		key      string
		severity string
		category string
		title    string
		detail   string
		action   string
		subjects []contracts.AnalysisSubjectSelector
		findings []string
		evidence []string
	}
	subjectByPair := map[string]contracts.AnalysisSubject{}
	for _, subject := range report.Subjects {
		subjectByPair[subject.SubjectID+"\x00"+subject.SampleID] = subject
	}
	buckets := map[string]*bucket{}
	order := []string{}
	for _, f := range report.Findings {
		key, category, title := rootCauseKey(f, subjectByPair[f.SubjectID+"\x00"+f.SampleID])
		if key == "" {
			continue
		}
		b, ok := buckets[key]
		if !ok {
			b = &bucket{key: key, severity: f.Severity, category: category, title: title, detail: f.Detail, action: f.Recommendation}
			buckets[key] = b
			order = append(order, key)
		}
		if severityValue(f.Severity) < severityValue(b.severity) {
			b.severity = f.Severity
		}
		if len([]rune(f.Detail)) > len([]rune(b.detail)) {
			b.detail = f.Detail
		}
		if b.action == "" {
			b.action = f.Recommendation
		}
		if f.SubjectID != "" && f.SampleID != "" {
			lang := subjectByPair[f.SubjectID+"\x00"+f.SampleID].Language
			b.subjects = appendUniqueSelector(b.subjects, contracts.AnalysisSubjectSelector{SubjectID: f.SubjectID, SampleID: f.SampleID, Language: lang})
		}
		b.findings = appendUniqueString(b.findings, f.ID)
		b.evidence = appendUniqueString(b.evidence, "finding-"+safeEvidenceID(f.ID))
		for _, ev := range f.Evidence {
			if ev.Kind == "workspace_diff" {
				b.evidence = appendUniqueString(b.evidence, "diff-"+safeEvidenceID(f.SubjectID+"-"+f.SampleID+"-"+subjectByPair[f.SubjectID+"\x00"+f.SampleID].Language))
			} else if ev.Kind == "trajectory" {
				b.evidence = appendUniqueString(b.evidence, "subject-"+safeEvidenceID(f.SubjectID+"-"+f.SampleID+"-"+subjectByPair[f.SubjectID+"\x00"+f.SampleID].Language))
			}
		}
	}
	var out []contracts.AnalysisRootCause
	for _, key := range order {
		b := buckets[key]
		detail := b.detail
		if len(b.subjects) > 1 {
			detail = fmt.Sprintf("共 %d 个对象出现同类问题。代表性证据：%s", len(b.subjects), b.detail)
		}
		out = append(out, contracts.AnalysisRootCause{
			ID:                fmt.Sprintf("rc-%03d", len(out)+1),
			Severity:          firstNonEmpty(b.severity, "P2"),
			Category:          b.category,
			Title:             b.title,
			Detail:            detail,
			RecommendedAction: b.action,
			AffectedSubjects:  b.subjects,
			EvidenceIDs:       b.evidence,
			RelatedFindings:   b.findings,
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		return severityValue(out[i].Severity) < severityValue(out[j].Severity)
	})
	return out
}

func buildComparisonSummary(report *contracts.AnalysisReport) *contracts.AnalysisComparisonSummary {
	selected := report.Selection.SelectedSubjects
	if len(selected) < 2 {
		return nil
	}
	subjects := selectedSubjectsForComparison(report.Subjects, selected)
	if len(subjects) < 2 {
		return nil
	}
	best, worst := subjects[0], subjects[0]
	issueCounts := map[string]int{}
	var differences []string
	for _, subject := range subjects {
		if analysisQualityScore(subject) > analysisQualityScore(best) {
			best = subject
		}
		if analysisQualityScore(subject) < analysisQualityScore(worst) {
			worst = subject
		}
		for _, issue := range subjectIssueLabels(subject) {
			issueCounts[issue]++
		}
		differences = append(differences, fmt.Sprintf("%s/%s/%s：编译=%t 测试=%s 覆盖=%s 变异=%s trace=%d", subject.SubjectID, subject.SampleID, subject.Language, subject.CompilePass, boolPtrLabelForAnalysis(subject.TestPass), metricText(subject.LineCoverage), metricText(subject.MutationScore), subject.TraceStepCount))
	}
	var common []string
	for issue, count := range issueCounts {
		if count >= 2 {
			common = append(common, issue)
		}
	}
	sort.Strings(common)
	return &contracts.AnalysisComparisonSummary{
		SelectedSubjects: selected,
		CommonIssues:     common,
		Differences:      differences,
		BestSubject:      best.SubjectID + " / " + best.SampleID + " / " + best.Language,
		WorstSubject:     worst.SubjectID + " / " + worst.SampleID + " / " + worst.Language,
		TransferableStrategies: []string{
			"迁移最好对象中稳定通过 baseline 的测试组织方式。",
			"对最差对象优先修复编译、源码污染、未运行测试等阻断项，再优化覆盖和变异。",
		},
	}
}

func buildSummary(report *contracts.AnalysisReport) contracts.AnalysisSummary {
	s := contracts.AnalysisSummary{
		SubjectCount:        len(report.Subjects),
		ResultCount:         len(report.Subjects),
		FindingCount:        len(report.Findings),
		RecommendationCount: len(report.Recommendations),
	}
	for _, f := range report.Findings {
		switch f.Severity {
		case "P0":
			s.CriticalCount++
		case "P1", "P2":
			s.WarningCount++
		}
	}
	switch {
	case s.CriticalCount > 0:
		s.Headline = fmt.Sprintf("发现 %d 个阻断级问题，需要优先处理。", s.CriticalCount)
	case s.WarningCount > 0:
		s.Headline = fmt.Sprintf("发现 %d 个需要优化的问题。", s.WarningCount)
	default:
		s.Headline = "规则诊断未发现明显阻断问题。"
	}
	s.KeyPoints = append(s.KeyPoints,
		fmt.Sprintf("已分析 %d 个 subject / sample 结果。", s.ResultCount),
		fmt.Sprintf("trajectory 覆盖 %d/%d。", report.TraceQuality.SubjectsWithTrajectory, report.TraceQuality.TotalSubjects),
	)
	modifiedSourceCount := 0
	for _, subject := range report.Subjects {
		if subject.ModifiedSource {
			modifiedSourceCount++
		}
	}
	if modifiedSourceCount > 0 {
		s.KeyPoints = append(s.KeyPoints, fmt.Sprintf("发现 %d 个结果修改了被测源码，相关失败应优先按源码污染排查。", modifiedSourceCount))
	}
	if report.LLMStatus.Enabled {
		point := "LLM 诊断状态：" + report.LLMStatus.Status
		if report.LLMStatus.Model != "" {
			point += "，模型：" + report.LLMStatus.Model
		}
		if report.LLMStatus.EvidenceCount > 0 {
			point += fmt.Sprintf("，证据 %d 条", report.LLMStatus.EvidenceCount)
		}
		s.KeyPoints = append(s.KeyPoints, point)
	}
	return s
}

func accumulateTraceQuality(q *contracts.AnalysisTraceQuality, subject contracts.AnalysisSubject) {
	if subject.TraceStepCount > 0 {
		q.SubjectsWithTrajectory++
	}
	if subject.RawTracePath != "" {
		q.SubjectsWithRawTrace++
	}
	q.TotalSteps += subject.TraceStepCount
	for _, step := range subject.Trajectory {
		switch step.Kind {
		case "tool_call", "command":
			q.ToolCallSteps++
		case "message":
			q.MessageSteps++
		case "thinking":
			q.ThinkingSteps++
		}
	}
}

func ReadReport(path string) (*contracts.AnalysisReport, error) {
	var report contracts.AnalysisReport
	if err := readJSON(path, &report); err != nil {
		return nil, err
	}
	return &report, nil
}

func readTrajectory(path string) (*runner.AgentTrajectory, error) {
	var traj runner.AgentTrajectory
	if err := readJSON(path, &traj); err != nil {
		return nil, err
	}
	return &traj, nil
}

func readWorkspaceChanges(path string) []string {
	var payload struct {
		Changes []string `json:"changes"`
	}
	if err := readJSON(path, &payload); err == nil {
		return payload.Changes
	}
	var raw []string
	if err := readJSON(path, &raw); err == nil {
		return raw
	}
	return nil
}

func readJSON(path string, out any) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("invalid json %s: %w", path, err)
	}
	return nil
}

func resolvePath(outputRoot, path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if _, err := os.Stat(path); err == nil {
		return path
	}
	clean := filepath.ToSlash(path)
	if strings.HasPrefix(clean, "/app/artifacts/") {
		return filepath.Join(outputRoot, filepath.FromSlash(strings.TrimPrefix(clean, "/app/artifacts/")))
	}
	if strings.HasPrefix(clean, "artifacts/") {
		base := filepath.Dir(filepath.Clean(outputRoot))
		return filepath.Join(base, filepath.FromSlash(clean))
	}
	if !filepath.IsAbs(path) {
		candidate := filepath.Join(filepath.Dir(filepath.Clean(outputRoot)), filepath.FromSlash(clean))
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return path
}

func inferSiblingArtifactPath(outputRoot, current, diffPath, sampleID, suffix string) string {
	if strings.TrimSpace(current) != "" {
		return current
	}
	diffPath = strings.TrimSpace(diffPath)
	if diffPath == "" {
		return ""
	}
	resolvedDiff := resolvePath(outputRoot, diffPath)
	dir := filepath.Dir(resolvedDiff)
	candidates := []string{}
	if sampleID != "" {
		candidates = append(candidates, filepath.Join(dir, sampleID+suffix))
	}
	if strings.HasSuffix(strings.ToLower(resolvedDiff), ".diff.json") {
		candidates = append(candidates, strings.TrimSuffix(resolvedDiff, ".diff.json")+suffix)
	}
	for _, candidate := range candidates {
		if _, err := os.Stat(candidate); err == nil {
			return toArtifactPath(outputRoot, candidate)
		}
	}
	return ""
}

func toArtifactPath(outputRoot, path string) string {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	absOutput, err := filepath.Abs(outputRoot)
	if err != nil {
		return path
	}
	if rel, err := filepath.Rel(filepath.Dir(absOutput), absPath); err == nil && !strings.HasPrefix(rel, "..") {
		return rel
	}
	return path
}

func renderMarkdown(report *contracts.AnalysisReport) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# UTBench AI 分析报告\n\n")
	fmt.Fprintf(&b, "- Run: `%s`\n", report.RunID)
	fmt.Fprintf(&b, "- 生成时间: `%s`\n", report.GeneratedAt.Format(time.RFC3339))
	fmt.Fprintf(&b, "- LLM: `%s`\n", report.LLMStatus.Status)
	if report.LLMStatus.Model != "" {
		fmt.Fprintf(&b, "- LLM 模型: `%s`\n", report.LLMStatus.Model)
	}
	if report.LLMStatus.Message != "" {
		fmt.Fprintf(&b, "- LLM 降级原因: `%s`\n", report.LLMStatus.Message)
	}
	fmt.Fprintf(&b, "\n## 总结\n\n%s\n\n", report.Summary.Headline)
	for _, p := range report.Summary.KeyPoints {
		fmt.Fprintf(&b, "- %s\n", p)
	}
	fmt.Fprintf(&b, "\n## 规则诊断\n\n")
	for _, f := range report.Findings {
		if f.Source != "rule" {
			continue
		}
		fmt.Fprintf(&b, "- **[%s][%s] %s** `%s/%s`: %s\n", f.Severity, f.Category, f.Title, f.SubjectID, f.SampleID, f.Detail)
	}
	fmt.Fprintf(&b, "\n## LLM 诊断\n\n")
	for _, f := range report.Findings {
		if f.Source != "llm" {
			continue
		}
		fmt.Fprintf(&b, "- **[%s][%s] %s** `%s/%s`: %s\n", f.Severity, f.Category, f.Title, f.SubjectID, f.SampleID, f.Detail)
	}
	fmt.Fprintf(&b, "\n## 优化建议\n\n")
	for _, r := range report.Recommendations {
		fmt.Fprintf(&b, "- **[%s][%s][%s] %s**: %s\n", r.Priority, r.Source, firstNonEmpty(r.Target, r.Category), r.Title, r.Detail)
	}
	return b.String()
}

func anyText(v any) string {
	if v == nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return t
	default:
		raw, _ := json.Marshal(t)
		return string(raw)
	}
}

func trim(s string, max int) string {
	s = strings.TrimSpace(s)
	if len([]rune(s)) <= max {
		return s
	}
	r := []rune(s)
	return string(r[:max]) + "..."
}

func containsAny(s string, items []string) bool {
	for _, item := range items {
		if strings.Contains(s, item) {
			return true
		}
	}
	return false
}

func safeEvidenceID(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "global"
	}
	return out
}

func appendUniqueString(items []string, item string) []string {
	item = strings.TrimSpace(item)
	if item == "" {
		return items
	}
	for _, existing := range items {
		if existing == item {
			return items
		}
	}
	return append(items, item)
}

func appendUniqueSelector(items []contracts.AnalysisSubjectSelector, item contracts.AnalysisSubjectSelector) []contracts.AnalysisSubjectSelector {
	if item.SubjectID == "" || item.SampleID == "" {
		return items
	}
	key := analysisSubjectKey(item.SubjectID, item.SampleID, item.Language)
	for _, existing := range items {
		if analysisSubjectKey(existing.SubjectID, existing.SampleID, existing.Language) == key {
			return items
		}
	}
	return append(items, item)
}

func rootCauseKey(f contracts.AnalysisFinding, subject contracts.AnalysisSubject) (string, string, string) {
	base := f.SubjectID + "\x00" + f.SampleID
	if subject.ModifiedSource && (f.Category == "policy" || f.Category == "compile" || f.Category == "test" || f.Category == "mutation") {
		return "source_modified", "policy", "修改源码导致评测结果失真"
	}
	if f.Category == "mutation" && containsAny(strings.ToLower(f.Detail), []string{"baseline", "skip", "跳过", "failed"}) {
		return "mutation_skipped", "mutation", "变异测试跳过或 baseline 失败"
	}
	if f.Category == "compile" {
		return "compile\x00" + base, "compile", "编译失败"
	}
	if f.Category == "test" {
		return "test\x00" + base, "test", f.Title
	}
	if f.Category == "trace" && strings.Contains(f.Title, "未观察到测试执行") {
		return "no_test_execution", "trace", "未观察到本地测试验证"
	}
	if f.Category == "trace" && strings.Contains(f.Title, "未观察到源码读取") {
		return "no_source_read", "trace", "未观察到源码读取"
	}
	if f.Category == "coverage" {
		return "coverage_low", f.Category, "覆盖率不足"
	}
	if f.Category == "mutation" {
		return "mutation_low", f.Category, "变异得分不足"
	}
	if severityValue(f.Severity) <= 1 {
		return f.Category + "\x00" + base + "\x00" + f.Title, f.Category, f.Title
	}
	return "", "", ""
}

func severityValue(sev string) int {
	switch sev {
	case "P0":
		return 0
	case "P1":
		return 1
	case "P2":
		return 2
	case "P3":
		return 3
	default:
		return 9
	}
}

func subjectEvidenceExcerpt(subject contracts.AnalysisSubject) string {
	return fmt.Sprintf("compile=%t test=%s coverage=%s mutation=%s trace_steps=%d source_read=%t test_write=%t test_execution=%t modified_source=%t policy_commands=%d runtime_noise=%d",
		subject.CompilePass,
		boolPtrLabelForAnalysis(subject.TestPass),
		metricText(subject.LineCoverage),
		metricText(subject.MutationScore),
		subject.TraceStepCount,
		subject.HasSourceRead,
		subject.HasTestWrite,
		subject.HasTestExecution,
		subject.ModifiedSource,
		subject.PolicyCommandCount,
		subject.RuntimeNoiseCount,
	)
}

func selectKeyAnalysisSteps(steps []contracts.AnalysisTrajectoryStep, max int) []contracts.AnalysisTrajectoryStep {
	var out []contracts.AnalysisTrajectoryStep
	for _, step := range steps {
		if isKeyAnalysisStep(step) {
			out = append(out, step)
			if len(out) >= max {
				return out
			}
		}
	}
	if len(out) == 0 && len(steps) > 0 {
		limit := len(steps)
		if limit > max {
			limit = max
		}
		out = append(out, steps[:limit]...)
	}
	return out
}

func isKeyAnalysisStep(step contracts.AnalysisTrajectoryStep) bool {
	text := strings.ToLower(strings.Join(nonEmptyStrings(step.Kind, step.Tool, step.TextExcerpt, step.InputExcerpt, step.OutputExcerpt), "\n"))
	return containsAny(text, []string{"read", "write", "edit", "bash", "pytest", "go test", "mvn", "ctest", "error", "failed", "policy", "generated_test"})
}

func stepEvidenceTitleForAnalysis(step contracts.AnalysisTrajectoryStep) string {
	label := firstNonEmpty(step.Tool, step.Role, step.Kind)
	if label == "" {
		label = "step"
	}
	return fmt.Sprintf("trajectory step #%d %s", step.Index, label)
}

func subjectLanguageForFinding(subjects []contracts.AnalysisSubject, finding contracts.AnalysisFinding) string {
	for _, subject := range subjects {
		if subject.SubjectID == finding.SubjectID && subject.SampleID == finding.SampleID {
			return subject.Language
		}
	}
	return ""
}

func selectedSubjectsForComparison(subjects []contracts.AnalysisSubject, selected []contracts.AnalysisSubjectSelector) []contracts.AnalysisSubject {
	keys := map[string]bool{}
	for _, item := range selected {
		keys[analysisSubjectKey(item.SubjectID, item.SampleID, item.Language)] = true
	}
	var out []contracts.AnalysisSubject
	for _, subject := range subjects {
		if keys[analysisSubjectKey(subject.SubjectID, subject.SampleID, subject.Language)] {
			out = append(out, subject)
		}
	}
	return out
}

func analysisQualityScore(subject contracts.AnalysisSubject) float64 {
	score := 0.0
	if subject.CompilePass {
		score += 100
	}
	if subject.TestPass != nil && *subject.TestPass {
		score += 100
	}
	if subject.LineCoverage != nil {
		score += normMetric(*subject.LineCoverage) * 70
	}
	if subject.MutationScore != nil {
		score += normMetric(*subject.MutationScore) * 100
	}
	if subject.HasTestExecution {
		score += 10
	}
	if subject.ModifiedSource {
		score -= 120
	}
	score -= float64(subject.PolicyCommandCount * 5)
	return score
}

func subjectIssueLabels(subject contracts.AnalysisSubject) []string {
	var out []string
	if !subject.CompilePass {
		out = append(out, "编译失败")
	}
	if subject.TestPass == nil || !*subject.TestPass {
		out = append(out, "测试未通过或未执行")
	}
	if subject.MutationScore == nil || normMetric(*subject.MutationScore) < 0.6 {
		out = append(out, "变异得分低或缺失")
	}
	if subject.LineCoverage != nil && normMetric(*subject.LineCoverage) < 0.7 {
		out = append(out, "覆盖率偏低")
	}
	if !subject.HasTestExecution {
		out = append(out, "未观察到本地测试验证")
	}
	if subject.ModifiedSource {
		out = append(out, "修改源码")
	}
	return out
}

func boolPtrLabelForAnalysis(v *bool) string {
	if v == nil {
		return "未执行"
	}
	if *v {
		return "通过"
	}
	return "失败"
}

func metricText(v *float64) string {
	if v == nil {
		return "NA"
	}
	return fmt.Sprintf("%.1f%%", normMetric(*v)*100)
}

func isRuntimeNoise(path string) bool {
	parts := []string{".pytest_cache/", "__pycache__/", ".codebuddy/", ".claude/", ".opencode/", ".git/", "node_modules/", "target/", "build/", ".utbench/"}
	return containsAny(path, parts)
}

func normMetric(v float64) float64 {
	if v > 1 {
		return v / 100
	}
	return v
}

func resultKey(subjectID, model, lang, sample string) string {
	return firstNonEmpty(subjectID, model) + "|" + lang + "|" + sample
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func mapBool(ok bool, yes, no string) string {
	if ok {
		return yes
	}
	return no
}

func sortedSet(set map[string]bool) []string {
	if len(set) == 0 {
		return nil
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func mergeApplies(sets ...map[string]bool) []string {
	merged := map[string]bool{}
	for _, set := range sets {
		for k := range set {
			merged[k] = true
		}
	}
	return sortedSet(merged)
}

func sortFindings(items []contracts.AnalysisFinding) {
	rank := severityRank()
	sort.SliceStable(items, func(i, j int) bool {
		if sourceRank(items[i].Source) != sourceRank(items[j].Source) {
			return sourceRank(items[i].Source) < sourceRank(items[j].Source)
		}
		if rank[items[i].Severity] != rank[items[j].Severity] {
			return rank[items[i].Severity] < rank[items[j].Severity]
		}
		return items[i].SubjectID < items[j].SubjectID
	})
	for i := range items {
		if strings.TrimSpace(items[i].ID) == "" || strings.HasPrefix(items[i].ID, "rule-") || strings.HasPrefix(items[i].ID, "llm-") {
			items[i].ID = fmt.Sprintf("finding-%03d", i+1)
		}
	}
}

func sortRecommendations(items []contracts.AnalysisRecommendation) {
	rank := severityRank()
	sort.SliceStable(items, func(i, j int) bool {
		if sourceRank(items[i].Source) != sourceRank(items[j].Source) {
			return sourceRank(items[i].Source) < sourceRank(items[j].Source)
		}
		if rank[items[i].Priority] != rank[items[j].Priority] {
			return rank[items[i].Priority] < rank[items[j].Priority]
		}
		return items[i].Category < items[j].Category
	})
	for i := range items {
		if strings.TrimSpace(items[i].ID) == "" || strings.HasPrefix(items[i].ID, "rec-") || strings.HasPrefix(items[i].ID, "llm-rec-") {
			items[i].ID = fmt.Sprintf("rec-%03d", i+1)
		}
	}
}

func normalizeLLMFindings(items []contracts.AnalysisFinding, evidenceMap map[string]contracts.EvidenceRef) []contracts.AnalysisFinding {
	for i := range items {
		items[i].Source = "llm"
		if items[i].Severity == "" {
			items[i].Severity = "P2"
		}
		if items[i].Category == "" {
			items[i].Category = "skill"
		}
		if items[i].Confidence == 0 {
			items[i].Confidence = 0.65
		}
		items[i].Evidence = resolveEvidenceRefs(items[i].Evidence, evidenceMap)
		if len(items[i].Evidence) == 0 {
			items[i].Severity = lowerPriority(items[i].Severity)
			items[i].Confidence = minFloat(items[i].Confidence, 0.35)
			if !strings.Contains(items[i].Detail, "证据不足") {
				items[i].Detail = strings.TrimSpace(items[i].Detail + "（证据不足，需人工确认。）")
			}
		}
	}
	return items
}

func normalizeLLMRecommendations(items []contracts.AnalysisRecommendation, evidenceMap map[string]contracts.EvidenceRef) []contracts.AnalysisRecommendation {
	allowedTargets := map[string]bool{"skill": true, "prompt": true, "agent_config": true, "environment": true, "evaluator": true, "dataset": true}
	for i := range items {
		items[i].Source = "llm"
		if items[i].Priority == "" {
			items[i].Priority = "P2"
		}
		if items[i].Category == "" {
			items[i].Category = "skill"
		}
		if !allowedTargets[items[i].Target] {
			items[i].Target = defaultTargetForCategory(items[i].Category)
		}
		items[i].Evidence = resolveEvidenceRefs(items[i].Evidence, evidenceMap)
		if len(items[i].Evidence) == 0 {
			items[i].Priority = lowerPriority(items[i].Priority)
			if items[i].Risk == "" {
				items[i].Risk = "缺少可追溯 evidence_id，需人工确认后再落地。"
			}
		}
	}
	return items
}

func resolveEvidenceRefs(items []contracts.EvidenceRef, evidenceMap map[string]contracts.EvidenceRef) []contracts.EvidenceRef {
	var out []contracts.EvidenceRef
	for _, item := range items {
		if item.EvidenceID == "" {
			continue
		}
		ref, ok := evidenceMap[item.EvidenceID]
		if !ok {
			continue
		}
		if item.Kind != "" {
			ref.Kind = item.Kind
		}
		if item.Excerpt != "" {
			ref.Excerpt = item.Excerpt
		}
		out = append(out, ref)
	}
	return out
}

func dedupeFindings(items []contracts.AnalysisFinding) []contracts.AnalysisFinding {
	best := map[string]contracts.AnalysisFinding{}
	order := []string{}
	rank := severityRank()
	for _, item := range items {
		key := strings.Join([]string{item.SubjectID, item.SampleID, item.Category, strings.ToLower(strings.TrimSpace(item.Title))}, "|")
		if _, ok := best[key]; !ok {
			order = append(order, key)
			best[key] = item
			continue
		}
		current := best[key]
		replace := rank[item.Severity] < rank[current.Severity]
		if rank[item.Severity] == rank[current.Severity] && current.Source != "rule" && item.Source == "rule" {
			replace = true
		}
		if replace {
			best[key] = item
		}
	}
	out := make([]contracts.AnalysisFinding, 0, len(order))
	for _, key := range order {
		out = append(out, best[key])
	}
	return out
}

func dedupeRecommendations(items []contracts.AnalysisRecommendation) []contracts.AnalysisRecommendation {
	best := map[string]contracts.AnalysisRecommendation{}
	order := []string{}
	rank := severityRank()
	for _, item := range items {
		key := strings.Join([]string{item.Category, item.Target, strings.ToLower(strings.TrimSpace(item.Title)), strings.Join(item.AppliesTo, ",")}, "|")
		if _, ok := best[key]; !ok {
			order = append(order, key)
			best[key] = item
			continue
		}
		current := best[key]
		if rank[item.Priority] < rank[current.Priority] {
			best[key] = item
		}
	}
	out := make([]contracts.AnalysisRecommendation, 0, len(order))
	for _, key := range order {
		out = append(out, best[key])
	}
	return out
}

func severityRank() map[string]int {
	return map[string]int{"P0": 0, "P1": 1, "P2": 2, "P3": 3, "": 4}
}

func sourceRank(source string) int {
	switch source {
	case "llm":
		return 0
	case "rule":
		return 1
	default:
		return 2
	}
}

func lowerPriority(v string) string {
	switch v {
	case "P0", "P1", "P2":
		return "P3"
	default:
		return firstNonEmpty(v, "P3")
	}
}

func minFloat(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

func defaultTargetForCategory(category string) string {
	switch category {
	case "policy", "environment":
		return "environment"
	case "trace", "efficiency":
		return "agent_config"
	case "coverage", "mutation", "skill":
		return "prompt"
	case "evaluator":
		return "evaluator"
	case "dataset":
		return "dataset"
	default:
		return "skill"
	}
}

func compactJSON(v any) string {
	var buf bytes.Buffer
	_ = json.NewEncoder(&buf).Encode(v)
	return strings.TrimSpace(buf.String())
}
