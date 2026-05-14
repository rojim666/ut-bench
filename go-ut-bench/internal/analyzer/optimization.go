package analyzer

import (
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
)

const optimizationPromptVersion = "optimization-prompt.v0.1.0"

type OptimizeOptions struct {
	RunID      string
	OutputRoot string
	ConfigPath string
	LLMEnabled bool
	LLMModel   string
	Force      bool
}

func (s *Service) OptimizePlan(ctx context.Context, opts OptimizeOptions) (*contracts.OptimizationPlan, error) {
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
	planPath := filepath.Join(analysisDir, "optimization_plan.json")
	if !opts.Force {
		if plan, err := ReadOptimizationPlan(planPath); err == nil {
			return plan, nil
		}
	}

	report, err := ReadReport(analysisPath)
	if err != nil {
		return nil, fmt.Errorf("analysis_report.json not available; run AI analysis first: %w", err)
	}

	plan := &contracts.OptimizationPlan{
		SchemaVersion:  contracts.OptimizationPlanSchemaVersion,
		RunID:          opts.RunID,
		GeneratedAt:    time.Now().UTC(),
		SourceAnalysis: analysisPath,
		LLMStatus: contracts.LLMAnalysisStatus{
			Enabled: opts.LLMEnabled,
			Status:  mapBool(opts.LLMEnabled, "skipped", "disabled"),
			Model:   opts.LLMModel,
		},
		Items: buildRuleOptimizationItems(report),
	}

	evidenceMap, evidenceItems := loadOptimizationEvidence(opts.OutputRoot, analysisDir, report)
	if opts.LLMEnabled {
		llmItems, raw, status := s.runOptimizationLLM(ctx, opts, report, evidenceItems, evidenceMap)
		plan.RawLLMOutput = raw
		plan.LLMStatus = status
		if status.Message != "" {
			plan.LLMError = status.Message
		}
		plan.Items = append(plan.Items, llmItems...)
	}

	plan.Items = dedupeOptimizationItems(plan.Items)
	sortOptimizationItems(plan.Items)
	plan.Summary = buildOptimizationSummary(plan)

	if err := contracts.WriteJSON(planPath, plan); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(analysisDir, "optimization_plan.md"), []byte(renderOptimizationMarkdown(plan)), 0o644); err != nil {
		return nil, err
	}
	return plan, nil
}

func ReadOptimizationPlan(path string) (*contracts.OptimizationPlan, error) {
	var plan contracts.OptimizationPlan
	if err := readJSON(path, &plan); err != nil {
		return nil, err
	}
	return &plan, nil
}

func buildRuleOptimizationItems(report *contracts.AnalysisReport) []contracts.OptimizationItem {
	var items []contracts.OptimizationItem
	for _, rec := range report.Recommendations {
		if rec.Source != "rule" && rec.Source != "" {
			continue
		}
		items = append(items, optimizationItemFromRecommendation(rec))
	}
	for _, finding := range report.Findings {
		if !shouldCreateOptimizationFromFinding(finding) {
			continue
		}
		items = append(items, optimizationItemFromFinding(finding))
	}
	return items
}

func shouldCreateOptimizationFromFinding(f contracts.AnalysisFinding) bool {
	if f.Source != "rule" && f.Source != "" {
		return false
	}
	if f.Severity == "P0" || f.Severity == "P1" || f.Severity == "P2" {
		return true
	}
	return f.Category == "policy" && strings.Contains(f.Title, "噪声")
}

func optimizationItemFromRecommendation(rec contracts.AnalysisRecommendation) contracts.OptimizationItem {
	target := firstNonEmpty(rec.Target, defaultTargetForCategory(rec.Category))
	return contracts.OptimizationItem{
		Priority:        firstNonEmpty(rec.Priority, "P2"),
		Target:          normalizeOptimizationTarget(target),
		Category:        firstNonEmpty(rec.Category, target),
		Title:           rec.Title,
		Detail:          rec.Detail,
		AppliesTo:       rec.AppliesTo,
		Source:          "rule",
		SourceRefs:      []string{rec.ID},
		ExpectedMetrics: expectedMetricsForCategory(rec.Category),
		Actions:         defaultActionsForOptimization(target, rec.Category, rec.Title, rec.Detail, rec.AppliesTo),
		Risks:           defaultRisks(rec.Risk, target),
		Verification:    defaultVerification(target, rec.Category, rec.AppliesTo),
		Evidence:        optimizationEvidenceFromAnalysis(rec.Evidence, "analysis_recommendation"),
		Confidence:      0.9,
	}
}

func optimizationItemFromFinding(f contracts.AnalysisFinding) contracts.OptimizationItem {
	target := defaultTargetForCategory(f.Category)
	title := f.Title
	if title == "" {
		title = "处理 " + f.Category + " 问题"
	}
	detail := firstNonEmpty(f.Recommendation, f.Detail)
	return contracts.OptimizationItem{
		Priority:        firstNonEmpty(f.Severity, "P2"),
		Target:          normalizeOptimizationTarget(target),
		Category:        firstNonEmpty(f.Category, target),
		Title:           title,
		Detail:          detail,
		AppliesTo:       compactStringList([]string{f.SubjectID}),
		Source:          "rule",
		SourceRefs:      []string{f.ID},
		ExpectedMetrics: expectedMetricsForCategory(f.Category),
		Actions:         defaultActionsForOptimization(target, f.Category, title, detail, compactStringList([]string{f.SubjectID})),
		Risks:           defaultRisks("", target),
		Verification:    defaultVerification(target, f.Category, compactStringList([]string{f.SubjectID})),
		Evidence:        optimizationEvidenceFromAnalysis(f.Evidence, "analysis_finding"),
		Confidence:      firstNonZero(f.Confidence, 0.9),
	}
}

func defaultActionsForOptimization(target, category, title, detail string, applies []string) []contracts.OptimizationAction {
	subjectHint := strings.Join(applies, ", ")
	switch normalizeOptimizationTarget(target) {
	case "environment":
		return []contracts.OptimizationAction{
			{Title: "收紧环境契约", Detail: firstNonEmpty(detail, "禁止 agent 在生成阶段修改环境、安装依赖或写入运行时噪声。"), FileHint: "configs/agents.yaml / docker/agents"},
			{Title: "补充允许列表", Detail: "把确实需要放行的依赖安装动作移动到 Docker 镜像或样本环境声明中，不让 agent 临时安装。"},
		}
	case "agent_config":
		return []contracts.OptimizationAction{
			{Title: "限制无效探索", Detail: firstNonEmpty(detail, "限制重复读取、重复执行和无关工具调用。"), FileHint: "configs/agents.yaml"},
			{Title: "增加预算阈值", Detail: "为高 token subject 设置工具调用次数、输出长度或总 token 预算告警。目标 subject：" + subjectHint},
		}
	case "prompt":
		return []contracts.OptimizationAction{
			{Title: "强化生成步骤", Detail: firstNonEmpty(detail, "要求先读源码、再生成测试、最后运行最小验证命令。"), FileHint: "internal/runner/prompt.go / skill prompt"},
			{Title: "补充断言策略", Detail: "在 prompt 中要求覆盖正常路径、边界路径、异常路径，并断言具体返回值或异常消息。"},
		}
	case "evaluator":
		return []contracts.OptimizationAction{
			{Title: "检查评测器归因", Detail: firstNonEmpty(detail, "确认失败归因、指标缺失和跳过原因能被 evaluator 明确输出。"), FileHint: "internal/evaluator"},
		}
	default:
		return []contracts.OptimizationAction{
			{Title: title, Detail: firstNonEmpty(detail, "根据诊断结果调整对应 skill 或 prompt。")},
		}
	}
}

func defaultVerification(target, category string, applies []string) []contracts.OptimizationVerification {
	appliesText := strings.Join(applies, ", ")
	if appliesText == "" {
		appliesText = "受影响 subject"
	}
	expected := "相关 finding 数量下降，且 compile/test/coverage/mutation 指标不回退。"
	switch normalizeOptimizationTarget(target) {
	case "environment":
		expected = "不再出现 policy/environment 类失败，workspace diff 中无新增运行时噪声。"
	case "agent_config":
		expected = "token 或耗时下降，且生成测试仍可通过评测。"
	case "prompt", "skill":
		expected = "覆盖率或变异得分提升，且生成测试仍能编译并通过。"
	}
	return []contracts.OptimizationVerification{{
		ManualStep: fmt.Sprintf("用相同样本重新运行 %s 的小样本评测，并重新生成 AI 分析。", appliesText),
		Expected:   expected,
	}}
}

func defaultRisks(riskText, target string) []contracts.OptimizationRisk {
	if strings.TrimSpace(riskText) != "" {
		return []contracts.OptimizationRisk{{Description: riskText, Mitigation: "先在单样本或轻量评测集验证，再扩大范围。", Rollback: "恢复对应配置或 prompt 修改。"}}
	}
	switch normalizeOptimizationTarget(target) {
	case "environment":
		return []contracts.OptimizationRisk{{Description: "过强环境约束可能拦截确实需要依赖安装的项目级样本。", Mitigation: "按样本元信息或 Docker 镜像预装依赖放行。", Rollback: "回退 sandbox allowlist 配置。"}}
	case "agent_config":
		return []contracts.OptimizationRisk{{Description: "限制探索可能降低复杂样本的生成质量。", Mitigation: "只对轻量评测或高 token subject 启用预算。", Rollback: "恢复原 agent_config。"}}
	default:
		return []contracts.OptimizationRisk{{Description: "更强约束可能让部分已有通过样本回退。", Mitigation: "先跑小样本回归。", Rollback: "恢复原 prompt/skill。"}}
	}
}

func expectedMetricsForCategory(category string) []string {
	switch category {
	case "compile":
		return []string{"compile"}
	case "test":
		return []string{"test"}
	case "coverage":
		return []string{"coverage"}
	case "mutation":
		return []string{"mutation"}
	case "trace":
		return []string{"trace"}
	case "policy":
		return []string{"trace", "environment"}
	case "efficiency":
		return []string{"efficiency"}
	case "skill":
		return []string{"coverage", "mutation"}
	default:
		return []string{category}
	}
}

func optimizationEvidenceFromAnalysis(items []contracts.EvidenceRef, source string) []contracts.OptimizationEvidenceRef {
	out := make([]contracts.OptimizationEvidenceRef, 0, len(items))
	for _, item := range items {
		out = append(out, contracts.OptimizationEvidenceRef{
			EvidenceID: item.EvidenceID,
			Kind:       item.Kind,
			Path:       item.Path,
			SubjectID:  item.SubjectID,
			SampleID:   item.SampleID,
			StepIndex:  item.StepIndex,
			Excerpt:    item.Excerpt,
			Source:     source,
		})
	}
	return out
}

func loadOptimizationEvidence(outputRoot, analysisDir string, report *contracts.AnalysisReport) (map[string]contracts.OptimizationEvidenceRef, []LLMEvidenceItem) {
	evidenceMap := map[string]contracts.OptimizationEvidenceRef{}
	var bundle LLMEvidenceBundle
	if err := readJSON(filepath.Join(analysisDir, "llm_evidence_bundle.json"), &bundle); err == nil {
		for _, item := range bundle.Evidence {
			ref := contracts.OptimizationEvidenceRef{
				EvidenceID: item.EvidenceID,
				Kind:       item.Kind,
				Path:       item.Path,
				SubjectID:  item.SubjectID,
				SampleID:   item.SampleID,
				StepIndex:  item.StepIndex,
				Excerpt:    item.Excerpt,
				Source:     "llm_evidence_bundle",
			}
			evidenceMap[item.EvidenceID] = ref
		}
		return evidenceMap, bundle.Evidence
	}
	var evidence []LLMEvidenceItem
	next := 1
	add := func(kind, subjectID, sampleID, path, excerpt string) {
		id := fmt.Sprintf("opt-ev-%03d", next)
		next++
		item := LLMEvidenceItem{EvidenceID: id, Kind: kind, SubjectID: subjectID, SampleID: sampleID, Path: path, Title: kind, Excerpt: trim(excerpt, 900)}
		evidence = append(evidence, item)
		evidenceMap[id] = contracts.OptimizationEvidenceRef{EvidenceID: id, Kind: kind, Path: path, SubjectID: subjectID, SampleID: sampleID, Excerpt: item.Excerpt, Source: "analysis_report"}
	}
	for _, f := range report.Findings {
		add("finding", f.SubjectID, f.SampleID, "", fmt.Sprintf("[%s/%s] %s: %s", f.Severity, f.Category, f.Title, f.Detail))
		for _, ev := range f.Evidence {
			add(ev.Kind, ev.SubjectID, ev.SampleID, resolvePath(outputRoot, ev.Path), ev.Excerpt)
		}
	}
	return evidenceMap, evidence
}

func (s *Service) runOptimizationLLM(ctx context.Context, opts OptimizeOptions, report *contracts.AnalysisReport, evidence []LLMEvidenceItem, evidenceMap map[string]contracts.OptimizationEvidenceRef) ([]contracts.OptimizationItem, string, contracts.LLMAnalysisStatus) {
	status := contracts.LLMAnalysisStatus{Enabled: true, Status: "skipped", Model: opts.LLMModel, EvidenceCount: len(evidence)}
	if s.llm == nil {
		status.Status = "skipped"
		status.Message = "llm client not configured"
		return nil, "", status
	}
	prompt, err := buildOptimizationPrompt(report, evidence)
	if err != nil {
		status.Status = "degraded"
		status.Message = err.Error()
		return nil, "", status
	}
	ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	result, err := s.llm.Analyze(ctx, LLMRequest{ConfigPath: opts.ConfigPath, ModelName: opts.LLMModel, Prompt: prompt})
	status.Model = firstNonEmpty(result.Model, opts.LLMModel)
	raw := result.RawOutput
	if err != nil {
		status.Status = "degraded"
		status.Message = err.Error()
		return nil, raw, status
	}
	if strings.TrimSpace(raw) == "" {
		raw = compactJSON(result)
	}
	items, err := parseOptimizationLLMJSON(raw)
	if err != nil {
		status.Status = "degraded"
		status.Message = err.Error()
		return nil, raw, status
	}
	status.Status = "ok"
	return normalizeLLMOptimizationItems(items, evidenceMap), raw, status
}

func buildOptimizationPrompt(report *contracts.AnalysisReport, evidence []LLMEvidenceItem) (string, error) {
	type bundle struct {
		RunID           string                             `json:"run_id"`
		Summary         contracts.AnalysisSummary          `json:"summary"`
		TopFindings     []contracts.AnalysisFinding        `json:"top_findings"`
		Recommendations []contracts.AnalysisRecommendation `json:"recommendations"`
		Evidence        []LLMEvidenceItem                  `json:"evidence"`
	}
	b := bundle{
		RunID:           report.RunID,
		Summary:         report.Summary,
		TopFindings:     limitFindingsForOptimization(report.Findings, 12),
		Recommendations: limitRecommendationsForOptimization(report.Recommendations, 10),
		Evidence:        limitOptimizationEvidence(evidence, 24),
	}
	raw, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return "", err
	}
	return `你是 UTBench 的单元测试 agent 优化方案生成器。请把诊断结果整理成可人工执行的优化清单。

硬性要求：
1. 只输出合法 JSON，不要输出 markdown。
2. 不要生成 diff，不要要求自动修改 skill，不要自动复测。
3. items 最多 5 条，优先覆盖 P0/P1、policy、efficiency、skill/prompt 问题。
4. 每个 item 必须包含 target、actions、risks、verification、expected_metrics、evidence。
5. target 只能是 skill/prompt/agent_config/environment/evaluator。
6. evidence 必须引用输入里的 evidence_id；没有证据只能作为 P3 低置信度项。

输出 JSON 格式：
{
  "summary": "一句话总结",
  "items": [
    {
      "priority": "P1",
      "target": "prompt",
      "category": "skill",
      "title": "...",
      "detail": "...",
      "applies_to": ["subject_id"],
      "expected_metrics": ["coverage", "mutation"],
      "actions": [{"title":"...","detail":"...","file_hint":"..."}],
      "risks": [{"description":"...","mitigation":"...","rollback":"..."}],
      "verification": [{"manual_step":"...","command":"","expected":"..."}],
      "evidence": [{"evidence_id":"ev-001"}],
      "confidence": 0.8
    }
  ]
}

输入：
` + string(raw), nil
}

func parseOptimizationLLMJSON(text string) ([]contracts.OptimizationItem, error) {
	clean := strings.TrimSpace(text)
	if strings.HasPrefix(clean, "```") {
		clean = strings.TrimPrefix(clean, "```json")
		clean = strings.TrimPrefix(clean, "```JSON")
		clean = strings.TrimPrefix(clean, "```")
		clean = strings.TrimSuffix(clean, "```")
		clean = strings.TrimSpace(clean)
	}
	if !strings.HasPrefix(clean, "{") {
		start := strings.Index(clean, "{")
		end := strings.LastIndex(clean, "}")
		if start >= 0 && end > start {
			clean = clean[start : end+1]
		}
	}
	var wire struct {
		Items []contracts.OptimizationItem `json:"items"`
	}
	if err := json.Unmarshal([]byte(clean), &wire); err != nil {
		return nil, err
	}
	return wire.Items, nil
}

func normalizeLLMOptimizationItems(items []contracts.OptimizationItem, evidenceMap map[string]contracts.OptimizationEvidenceRef) []contracts.OptimizationItem {
	for i := range items {
		items[i].Source = "llm"
		if items[i].Priority == "" {
			items[i].Priority = "P2"
		}
		items[i].Target = normalizeOptimizationTarget(items[i].Target)
		if items[i].Category == "" {
			items[i].Category = items[i].Target
		}
		if items[i].Confidence == 0 {
			items[i].Confidence = 0.65
		}
		items[i].Evidence = resolveOptimizationEvidence(items[i].Evidence, evidenceMap)
		if len(items[i].Actions) == 0 {
			items[i].Actions = defaultActionsForOptimization(items[i].Target, items[i].Category, items[i].Title, items[i].Detail, items[i].AppliesTo)
		}
		if len(items[i].Verification) == 0 {
			items[i].Verification = defaultVerification(items[i].Target, items[i].Category, items[i].AppliesTo)
		}
		if len(items[i].ExpectedMetrics) == 0 {
			items[i].ExpectedMetrics = expectedMetricsForCategory(items[i].Category)
		}
		if len(items[i].Evidence) == 0 {
			items[i].Priority = lowerPriority(items[i].Priority)
			items[i].Confidence = minFloat(items[i].Confidence, 0.35)
			items[i].Risks = append(items[i].Risks, contracts.OptimizationRisk{Description: "缺少有效 evidence_id，需人工确认后再执行。", Mitigation: "回到 AI 分析页检查对应 finding 和 trajectory。"})
		}
	}
	return items
}

func resolveOptimizationEvidence(items []contracts.OptimizationEvidenceRef, evidenceMap map[string]contracts.OptimizationEvidenceRef) []contracts.OptimizationEvidenceRef {
	var out []contracts.OptimizationEvidenceRef
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

func limitFindingsForOptimization(items []contracts.AnalysisFinding, max int) []contracts.AnalysisFinding {
	out := make([]contracts.AnalysisFinding, 0, len(items))
	for _, item := range items {
		if item.Severity == "P0" || item.Severity == "P1" || item.Severity == "P2" || item.Source == "llm" {
			item.Detail = trim(item.Detail, 420)
			item.Recommendation = trim(item.Recommendation, 260)
			out = append(out, item)
		}
		if len(out) >= max {
			return out
		}
	}
	return out
}

func limitRecommendationsForOptimization(items []contracts.AnalysisRecommendation, max int) []contracts.AnalysisRecommendation {
	if len(items) <= max {
		return items
	}
	return items[:max]
}

func limitOptimizationEvidence(items []LLMEvidenceItem, max int) []LLMEvidenceItem {
	if len(items) <= max {
		return items
	}
	return items[:max]
}

func dedupeOptimizationItems(items []contracts.OptimizationItem) []contracts.OptimizationItem {
	best := map[string]contracts.OptimizationItem{}
	order := []string{}
	rank := severityRank()
	for _, item := range items {
		key := strings.Join([]string{item.Target, item.Category, strings.ToLower(strings.TrimSpace(item.Title)), strings.Join(item.AppliesTo, ",")}, "|")
		if _, ok := best[key]; !ok {
			order = append(order, key)
			best[key] = item
			continue
		}
		current := best[key]
		if sourceRank(item.Source) < sourceRank(current.Source) || (sourceRank(item.Source) == sourceRank(current.Source) && rank[item.Priority] < rank[current.Priority]) {
			best[key] = item
		}
	}
	out := make([]contracts.OptimizationItem, 0, len(order))
	for _, key := range order {
		out = append(out, best[key])
	}
	return out
}

func sortOptimizationItems(items []contracts.OptimizationItem) {
	rank := severityRank()
	sort.SliceStable(items, func(i, j int) bool {
		if sourceRank(items[i].Source) != sourceRank(items[j].Source) {
			return sourceRank(items[i].Source) < sourceRank(items[j].Source)
		}
		if rank[items[i].Priority] != rank[items[j].Priority] {
			return rank[items[i].Priority] < rank[items[j].Priority]
		}
		return items[i].Target < items[j].Target
	})
	for i := range items {
		if strings.TrimSpace(items[i].ID) == "" || strings.HasPrefix(items[i].ID, "opt-") {
			items[i].ID = fmt.Sprintf("opt-%03d", i+1)
		}
	}
}

func buildOptimizationSummary(plan *contracts.OptimizationPlan) contracts.OptimizationSummary {
	summary := contracts.OptimizationSummary{ItemCount: len(plan.Items)}
	targets := map[string]bool{}
	for _, item := range plan.Items {
		if item.Priority == "P0" || item.Priority == "P1" {
			summary.HighPriorityCount++
		}
		if item.Source == "llm" {
			summary.LLMItemCount++
		} else {
			summary.RuleItemCount++
		}
		if item.Target != "" {
			targets[item.Target] = true
		}
	}
	summary.Targets = sortedSet(targets)
	if summary.HighPriorityCount > 0 {
		summary.Headline = fmt.Sprintf("生成 %d 个高优先级优化项，建议先人工审核后执行。", summary.HighPriorityCount)
	} else if summary.ItemCount > 0 {
		summary.Headline = fmt.Sprintf("生成 %d 个可执行优化项。", summary.ItemCount)
	} else {
		summary.Headline = "当前分析结果未生成明确优化项。"
	}
	summary.KeyPoints = append(summary.KeyPoints,
		fmt.Sprintf("LLM 优化项 %d 个，规则优化项 %d 个。", summary.LLMItemCount, summary.RuleItemCount),
		fmt.Sprintf("覆盖目标：%s。", strings.Join(summary.Targets, ", ")),
	)
	return summary
}

func renderOptimizationMarkdown(plan *contracts.OptimizationPlan) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# UTBench 优化方案\n\n")
	fmt.Fprintf(&b, "- Run: `%s`\n", plan.RunID)
	fmt.Fprintf(&b, "- 生成时间: `%s`\n", plan.GeneratedAt.Format(time.RFC3339))
	fmt.Fprintf(&b, "- LLM: `%s`\n\n", plan.LLMStatus.Status)
	fmt.Fprintf(&b, "## 总结\n\n%s\n\n", plan.Summary.Headline)
	for _, item := range plan.Items {
		fmt.Fprintf(&b, "## [%s][%s][%s] %s\n\n", item.Priority, item.Source, item.Target, item.Title)
		fmt.Fprintf(&b, "%s\n\n", item.Detail)
		if len(item.Actions) > 0 {
			fmt.Fprintf(&b, "### 修改清单\n\n")
			for _, action := range item.Actions {
				fmt.Fprintf(&b, "- **%s**: %s\n", action.Title, action.Detail)
			}
			fmt.Fprintf(&b, "\n")
		}
		if len(item.Verification) > 0 {
			fmt.Fprintf(&b, "### 验收\n\n")
			for _, v := range item.Verification {
				if v.Command != "" {
					fmt.Fprintf(&b, "- `%s`，期望：%s\n", v.Command, v.Expected)
				} else {
					fmt.Fprintf(&b, "- %s，期望：%s\n", trimSentenceEnd(v.ManualStep), v.Expected)
				}
			}
			fmt.Fprintf(&b, "\n")
		}
	}
	return b.String()
}

func normalizeOptimizationTarget(target string) string {
	switch strings.TrimSpace(target) {
	case "skill", "prompt", "agent_config", "environment", "evaluator":
		return strings.TrimSpace(target)
	case "policy":
		return "environment"
	case "trace", "efficiency":
		return "agent_config"
	default:
		return "skill"
	}
}

func firstNonZero(v, fallback float64) float64 {
	if v != 0 {
		return v
	}
	return fallback
}

func trimSentenceEnd(s string) string {
	return strings.TrimRight(strings.TrimSpace(s), "。.!！")
}
