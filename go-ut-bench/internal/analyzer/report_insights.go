package analyzer

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"go-ut-bench/internal/contracts"
)

const reportEvidenceSchemaVersion = "report_evidence.v0.1.3"

type ReportEvidenceBundle struct {
	SchemaVersion     string                           `json:"schema_version"`
	RunID             string                           `json:"run_id"`
	GeneratedAt       time.Time                        `json:"generated_at"`
	Summary           contracts.ReportSummary          `json:"summary"`
	TopModels         []contracts.ModelRank            `json:"top_models,omitempty"`
	BottomModels      []contracts.ModelRank            `json:"bottom_models,omitempty"`
	ByLanguage        []contracts.LanguageDim          `json:"by_language,omitempty"`
	WeakScenarios     []contracts.ModelScenarioDim     `json:"weak_scenarios,omitempty"`
	SkillUplifts      []contracts.SkillUpliftRow       `json:"skill_uplifts,omitempty"`
	Efficiency        contracts.EfficiencyStats        `json:"efficiency,omitempty"`
	RootCauses        []contracts.AnalysisRootCause    `json:"root_causes,omitempty"`
	OptimizationItems []contracts.OptimizationItem     `json:"optimization_items,omitempty"`
	Evidence          []contracts.AnalysisEvidenceItem `json:"evidence"`
}

func (s *Service) buildReportInsights(ctx context.Context, opts Options, analysis *contracts.AnalysisReport, eval contracts.EvaluationResultSet, analysisDir string) ([]contracts.ReportInsight, *contracts.EvolutionPlan, []contracts.AnalysisEvidenceItem, contracts.LLMAnalysisStatus) {
	reportLLMEnabled := opts.LLMEnabled && len(opts.SelectedSubjects) == 0 && !opts.CompareMode
	status := contracts.LLMAnalysisStatus{
		Enabled: true,
		Status:  mapBool(reportLLMEnabled, "skipped", "ok"),
		Model:   opts.LLMModel,
	}
	reportPayload := readReportPayload(analysis.SourceFiles.ReportPath)
	optimization := readOptionalOptimizationPlan(filepath.Join(analysisDir, "optimization_plan.json"))
	bundle, evidenceMap := buildReportEvidenceBundle(analysis, reportPayload, optimization)
	status.EvidenceCount = len(bundle.Evidence)
	status.EvidenceSubjectCount = len(analysis.Subjects)
	_ = contracts.WriteJSON(filepath.Join(analysisDir, "report_evidence_bundle.json"), bundle)

	var insights []contracts.ReportInsight
	var evolution *contracts.EvolutionPlan
	if opts.RuleEnabled {
		insights, evolution = buildRuleReportInsights(analysis, reportPayload, eval, optimization, evidenceMap)
	} else {
		evolution = &contracts.EvolutionPlan{}
	}
	if reportLLMEnabled {
		llmInsights, llmEvolution, raw, llmStatus := s.runReportInsightLLM(ctx, opts, bundle, evidenceMap)
		status = llmStatus
		status.EvidenceCount = len(bundle.Evidence)
		status.EvidenceSubjectCount = len(analysis.Subjects)
		if raw != "" {
			_ = os.WriteFile(filepath.Join(analysisDir, "report_insights_llm_raw.txt"), []byte(raw), 0o644)
		}
		insights = append(insights, llmInsights...)
		evolution.Items = append(evolution.Items, llmEvolution...)
		if status.Status == "" {
			status.Status = "ok"
		}
	}
	insights = dedupeReportInsights(insights)
	sortReportInsights(insights)
	if len(insights) > 8 {
		insights = insights[:8]
	}
	evolution.Items = dedupeEvolutionItems(evolution.Items)
	sortEvolutionItems(evolution.Items)
	if len(evolution.Items) > 5 {
		evolution.Items = evolution.Items[:5]
	}
	evolution.Summary = buildEvolutionSummary(evolution.Items)
	return insights, evolution, bundle.Evidence, status
}

func readReportPayload(path string) *contracts.ReportPayload {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	var payload contracts.ReportPayload
	if err := readJSON(path, &payload); err != nil {
		return nil
	}
	return &payload
}

func readOptionalOptimizationPlan(path string) *contracts.OptimizationPlan {
	var plan contracts.OptimizationPlan
	if err := readJSON(path, &plan); err != nil {
		return nil
	}
	return &plan
}

func buildReportEvidenceBundle(analysis *contracts.AnalysisReport, payload *contracts.ReportPayload, optimization *contracts.OptimizationPlan) (ReportEvidenceBundle, map[string]contracts.AnalysisEvidenceItem) {
	bundle := ReportEvidenceBundle{
		SchemaVersion: reportEvidenceSchemaVersion,
		RunID:         analysis.RunID,
		GeneratedAt:   time.Now().UTC(),
		RootCauses:    limitRootCauses(analysis.RootCauses, 8),
	}
	if payload != nil {
		bundle.Summary = payload.Summary
		bundle.TopModels = limitModelRanks(payload.TopModels, 5)
		bundle.BottomModels = bottomModelRanks(payload.TopModels, 5)
		bundle.ByLanguage = payload.Dimensions.ByLanguage
		bundle.WeakScenarios = weakScenarios(payload.ByModelScenario, 8)
		bundle.SkillUplifts = limitSkillUplifts(payload.SkillUplifts, 8)
		bundle.Efficiency = payload.EfficiencyStats
	}
	if optimization != nil {
		bundle.OptimizationItems = limitOptimizationItemsForReport(optimization.Items, 8)
	}
	evidenceMap := map[string]contracts.AnalysisEvidenceItem{}
	add := func(id, kind, title, excerpt string) {
		if strings.TrimSpace(excerpt) == "" {
			return
		}
		item := contracts.AnalysisEvidenceItem{
			EvidenceID: id,
			Kind:       kind,
			Title:      title,
			Excerpt:    trim(excerpt, 1200),
		}
		bundle.Evidence = append(bundle.Evidence, item)
		evidenceMap[id] = item
	}
	add("report-summary", "report_summary", "报告汇总指标", compactJSON(bundle.Summary))
	if len(bundle.TopModels) > 0 || len(bundle.BottomModels) > 0 {
		add("metric-gap-agent-ranking", "metric_gap", "agent 排名差距", compactJSON(map[string]any{"top_models": bundle.TopModels, "bottom_models": bundle.BottomModels}))
	}
	for _, lang := range bundle.ByLanguage {
		id := "metric-gap-language-" + safeEvidenceID(lang.Language)
		add(id, "metric_gap", "语言指标短板 "+lang.Language, compactJSON(lang))
	}
	for _, scenario := range bundle.WeakScenarios {
		id := "metric-gap-scenario-" + safeEvidenceID(scenario.Model+"-"+scenario.Language+"-"+scenario.Scenario)
		add(id, "metric_gap", "场景指标短板 "+scenario.Language+"/"+scenario.Scenario, compactJSON(scenario))
	}
	for _, uplift := range bundle.SkillUplifts {
		id := "skill-uplift-" + safeEvidenceID(uplift.SubjectID+"-"+uplift.Language)
		add(id, "skill_uplift", "Skill uplift "+uplift.SubjectID, compactJSON(uplift))
	}
	if len(bundle.Efficiency.TokenEfficiency) > 0 || len(bundle.Efficiency.TimeEfficiency) > 0 || bundle.Efficiency.CostEstimate.TotalTokens > 0 {
		add("cost-efficiency", "cost_efficiency", "成本与效率摘要", compactJSON(bundle.Efficiency))
	}
	for _, rc := range bundle.RootCauses {
		add("root-cause-cluster-"+safeEvidenceID(rc.ID), "root_cause_cluster", "根因聚类 "+rc.Title, compactJSON(rc))
	}
	return bundle, evidenceMap
}

func buildRuleReportInsights(analysis *contracts.AnalysisReport, payload *contracts.ReportPayload, eval contracts.EvaluationResultSet, optimization *contracts.OptimizationPlan, evidence map[string]contracts.AnalysisEvidenceItem) ([]contracts.ReportInsight, *contracts.EvolutionPlan) {
	var insights []contracts.ReportInsight
	var items []contracts.EvolutionItem
	addInsight := func(priority, category, title, detail string, metrics map[string]float64, evidenceIDs []string, confidence float64) {
		insights = append(insights, contracts.ReportInsight{
			Priority:    priority,
			Category:    category,
			Title:       title,
			Detail:      detail,
			Metrics:     metrics,
			EvidenceIDs: validReportEvidenceIDs(evidenceIDs, evidence),
			Source:      "rule",
			Confidence:  confidence,
		})
	}
	addItem := func(priority, target, title, reason string, metrics []string, risks []string, verification []string, refs []string, evidenceIDs []string, confidence float64) {
		items = append(items, contracts.EvolutionItem{
			Priority:                priority,
			Target:                  normalizeEvolutionTarget(target),
			Title:                   title,
			Reason:                  reason,
			ExpectedMetrics:         metrics,
			Risks:                   risks,
			ManualVerification:      verification,
			RelatedOptimizationRefs: refs,
			EvidenceIDs:             validReportEvidenceIDs(evidenceIDs, evidence),
			Source:                  "rule",
			Confidence:              confidence,
		})
	}

	if payload != nil {
		score := reportHealthScore(payload.Summary)
		priority := "P2"
		if score < 55 {
			priority = "P1"
		}
		addInsight(priority, "health", "整体健康度 "+fmt.Sprintf("%.1f", score), fmt.Sprintf("编译 %.1f%%、样本测试 %.1f%%、覆盖 %.1f%%、变异 %.1f%%，下一轮应优先处理拖累综合得分的主指标。", pct(payload.Summary.CompilePassRate), pct(payload.Summary.SampleTestPassRate), pct(payload.Summary.AvgLineCoverage), pct(payload.Summary.AvgMutationScore)), map[string]float64{"health_score": score}, []string{"report-summary"}, 0.95)
	}

	if payload != nil && len(payload.TopModels) > 1 {
		best := payload.TopModels[0]
		worst := payload.TopModels[len(payload.TopModels)-1]
		addInsight("P2", "agent_gap", "最佳与最差 agent 差距明显", fmt.Sprintf("最佳 %s 综合 %.1f，最差 %s 综合 %.1f，差距 %.1f 分。", bestLabel(best), pct(best.CompositeScore), bestLabel(worst), pct(worst.CompositeScore), pct(best.CompositeScore-worst.CompositeScore)), map[string]float64{"best_score": pct(best.CompositeScore), "worst_score": pct(worst.CompositeScore)}, []string{"metric-gap-agent-ranking"}, 0.9)
	}

	if payload != nil {
		if lang, ok := weakestLanguage(payload.Dimensions.ByLanguage); ok {
			ev := "metric-gap-language-" + safeEvidenceID(lang.Language)
			priority := "P2"
			if normMetric(lang.AvgMutationScore) < 0.45 || normMetric(lang.AvgTestPassRate) < 0.5 {
				priority = "P1"
			}
			addInsight(priority, "language_gap", lang.Language+" 语言指标短板", fmt.Sprintf("%s 的测试通过 %.1f%%、覆盖 %.1f%%、变异 %.1f%%，是当前语言维度的优先改进对象。", lang.Language, pct(lang.AvgTestPassRate), pct(lang.AvgLineCoverage), pct(lang.AvgMutationScore)), map[string]float64{"test_pass_rate": pct(lang.AvgTestPassRate), "mutation_score": pct(lang.AvgMutationScore)}, []string{ev}, 0.9)
			addItem(priority, "prompt", "补强 "+lang.Language+" 测试生成策略", "语言维度短板会同时影响覆盖率、变异得分和通过率，应优先把该语言的断言策略和验证步骤写入 skill/prompt。", []string{"test", "coverage", "mutation"}, []string{"过强语言专用规则可能拖累其他语言。"}, []string{"用相同样本重新跑该语言小样本评测，确认测试通过率、覆盖率和变异得分不回退。"}, optimizationRefsByTarget(optimization, "prompt"), []string{ev}, 0.85)
		}
		if scenario, ok := weakestScenario(payload.ByModelScenario); ok {
			ev := "metric-gap-scenario-" + safeEvidenceID(scenario.Model+"-"+scenario.Language+"-"+scenario.Scenario)
			addInsight("P1", "scenario_gap", scenario.Language+"/"+scenario.Scenario+" 场景短板", fmt.Sprintf("%s 在 %s/%s 的编译 %.1f%%、测试 %.1f%%、变异 %.1f%%，建议作为下一轮回归切片。", scenario.Model, scenario.Language, scenario.Scenario, pct(scenario.CompilePassRate), pct(scenario.AvgTestPassRate), pct(scenario.AvgMutationScore)), map[string]float64{"compile_pass_rate": pct(scenario.CompilePassRate), "test_pass_rate": pct(scenario.AvgTestPassRate), "mutation_score": pct(scenario.AvgMutationScore)}, []string{ev}, 0.85)
		}
		if uplift, ok := bestSkillUplift(payload.SkillUplifts); ok {
			ev := "skill-uplift-" + safeEvidenceID(uplift.SubjectID+"-"+uplift.Language)
			addInsight("P1", "skill_uplift", "Skill uplift 可转化为下一轮优化", fmt.Sprintf("%s 相对 %s 在 %s 上覆盖提升 %.1f、变异提升 %.1f，可沉淀为优先优化方向。", uplift.SubjectID, uplift.BaselineSubjectID, firstNonEmpty(uplift.Language, "全局"), pct(uplift.LineCoverageDelta), pct(uplift.MutationScoreDelta)), map[string]float64{"coverage_delta": pct(uplift.LineCoverageDelta), "mutation_delta": pct(uplift.MutationScoreDelta)}, []string{ev}, 0.9)
			addItem("P1", "skill", "沉淀高 uplift skill 策略", "已有 skill uplift 说明该 skill 的测试策略相对 no_skill 有可复用收益，应优先分析其 prompt/工具使用模式并推广到弱项语言或场景。", []string{"coverage", "mutation"}, []string{"直接泛化到所有语言可能引入不适配规则。"}, []string{"选择 uplift 对应语言和一个弱项语言各跑小样本，对比 no_skill 与改后 skill 的指标差。"}, optimizationRefsByTarget(optimization, "skill"), []string{ev}, 0.9)
		}
		if inefficient, ok := inefficientModel(payload.Dimensions.ByModel); ok {
			addInsight("P2", "efficiency", "高 token 未换来对应指标收益", fmt.Sprintf("%s 平均 token %.0f，但综合得分 %.1f，ROI 低于同批次水平。", firstNonEmpty(inefficient.SubjectID, inefficient.Model), inefficient.AvgTotalTokens, pct(inefficient.CompositeScore)), map[string]float64{"avg_total_tokens": inefficient.AvgTotalTokens, "composite_score": pct(inefficient.CompositeScore)}, []string{"cost-efficiency"}, 0.85)
			addItem("P2", "agent_config", "收紧低 ROI agent 预算", "高 token 没有转化为编译、通过率或变异收益，应限制无效探索、重复读取和过长输出。", []string{"efficiency", "cost"}, []string{"预算过紧可能影响复杂样本。"}, []string{"对该 agent 先跑 3-5 个代表样本，确认 token/耗时下降且主要质量指标不回退。"}, optimizationRefsByTarget(optimization, "agent_config"), []string{"cost-efficiency"}, 0.8)
		}
	}

	for _, rc := range limitRootCauses(analysis.RootCauses, 3) {
		if severityValue(rc.Severity) > 1 {
			continue
		}
		ev := "root-cause-cluster-" + safeEvidenceID(rc.ID)
		addItem(rc.Severity, defaultTargetForCategory(rc.Category), "优先处理根因："+rc.Title, firstNonEmpty(rc.RecommendedAction, rc.Detail), expectedMetricsForCategory(rc.Category), []string{"修复单一根因可能暴露下游 evaluator 或环境问题。"}, []string{"重新生成 AI 分析，确认相关 root cause 和 P0/P1 finding 数量下降。"}, optimizationRefsByTarget(optimization, defaultTargetForCategory(rc.Category)), []string{ev}, 0.85)
	}

	if payload == nil && len(eval.Results) > 0 {
		addInsight("P2", "health", "缺少 report_summary，已使用 evaluation 兜底", fmt.Sprintf("当前 run 有 %d 条 evaluation result，但未读取到 report_summary.json，报告级洞察只能使用有限指标。", len(eval.Results)), map[string]float64{"result_count": float64(len(eval.Results))}, nil, 0.7)
	}
	return insights, &contracts.EvolutionPlan{Items: items}
}

func (s *Service) runReportInsightLLM(ctx context.Context, opts Options, bundle ReportEvidenceBundle, evidenceMap map[string]contracts.AnalysisEvidenceItem) ([]contracts.ReportInsight, []contracts.EvolutionItem, string, contracts.LLMAnalysisStatus) {
	status := contracts.LLMAnalysisStatus{Enabled: true, Status: "skipped", Model: opts.LLMModel, EvidenceCount: len(bundle.Evidence)}
	if s.llm == nil {
		status.Message = "llm client not configured"
		return nil, nil, "", status
	}
	prompt, err := buildReportInsightPrompt(bundle)
	if err != nil {
		status.Status = "degraded"
		status.Message = err.Error()
		return nil, nil, "", status
	}
	ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	reportProgress(opts.Progress, "LLM 报告洞察")
	result, err := s.llm.Analyze(ctx, LLMRequest{ConfigPath: opts.ConfigPath, ModelName: opts.LLMModel, Prompt: prompt})
	raw := result.RawOutput
	if err != nil {
		status.Status = "degraded"
		status.Message = err.Error()
		return nil, nil, raw, status
	}
	if strings.TrimSpace(raw) == "" {
		raw = compactJSON(result)
	}
	insights := result.ReportInsights
	items := []contracts.EvolutionItem{}
	if result.EvolutionPlan != nil {
		items = result.EvolutionPlan.Items
	}
	if len(insights) == 0 && len(items) == 0 {
		parsedInsights, parsedItems, parseErr := parseReportInsightLLMJSON(raw)
		if parseErr != nil {
			status.Status = "degraded"
			status.Message = parseErr.Error()
			return nil, nil, raw, status
		}
		insights = parsedInsights
		items = parsedItems
	}
	status.Status = "ok"
	status.Model = firstNonEmpty(result.Model, opts.LLMModel)
	return normalizeLLMReportInsights(insights, evidenceMap), normalizeLLMEvolutionItems(items, evidenceMap), raw, status
}

func buildReportInsightPrompt(bundle ReportEvidenceBundle) (string, error) {
	raw, err := json.MarshalIndent(bundle, "", "  ")
	if err != nil {
		return "", err
	}
	return `你是 UTBench 的报告级评测洞察分析器。请基于 report evidence bundle 给出下一轮最值得优化的方向。

硬性要求：
1. 只输出合法 JSON，不要输出 markdown、解释或代码块。
2. 不要生成 patch，不要要求自动修改 skill，不要自动复测。
3. 规则事实不可被覆盖；你只能补充解释、归因和优先级。
4. report_insights 最多 8 条，evolution_plan.items 最多 5 条。
5. 每条 insight 和 evolution item 必须尽量引用 evidence_ids；没有证据只能作为 P3 低置信度项。
6. target 只能是 skill/prompt/agent_config/environment/evaluator。
7. 必须回答：先改谁、为什么、改什么、预计影响哪些指标、风险是什么、怎么人工验收。

输出 JSON 格式：
{
  "report_insights": [
    {
      "priority": "P1",
      "category": "skill_uplift",
      "title": "...",
      "detail": "...",
      "metrics": {"mutation_delta": 12.3},
      "evidence_ids": ["skill-uplift-xxx"],
      "confidence": 0.8
    }
  ],
  "evolution_plan": {
    "summary": "一句话总结下一轮优化重点",
    "items": [
      {
        "priority": "P1",
        "target": "skill",
        "title": "...",
        "reason": "...",
        "expected_metrics": ["coverage", "mutation"],
        "risks": ["..."],
        "manual_verification": ["..."],
        "related_optimization_refs": ["opt-001"],
        "evidence_ids": ["skill-uplift-xxx"],
        "confidence": 0.8
      }
    ]
  }
}

Report evidence bundle:
` + string(raw), nil
}

func parseReportInsightLLMJSON(text string) ([]contracts.ReportInsight, []contracts.EvolutionItem, error) {
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
		ReportInsights []contracts.ReportInsight `json:"report_insights"`
		EvolutionPlan  *contracts.EvolutionPlan  `json:"evolution_plan"`
	}
	if err := json.Unmarshal([]byte(clean), &wire); err != nil {
		return nil, nil, err
	}
	var items []contracts.EvolutionItem
	if wire.EvolutionPlan != nil {
		items = wire.EvolutionPlan.Items
	}
	return wire.ReportInsights, items, nil
}

func normalizeLLMReportInsights(items []contracts.ReportInsight, evidenceMap map[string]contracts.AnalysisEvidenceItem) []contracts.ReportInsight {
	for i := range items {
		items[i].Source = "llm"
		if items[i].Priority == "" {
			items[i].Priority = "P2"
		}
		if items[i].Category == "" {
			items[i].Category = "report"
		}
		if items[i].Confidence == 0 {
			items[i].Confidence = 0.65
		}
		items[i].EvidenceIDs = validReportEvidenceIDs(items[i].EvidenceIDs, evidenceMap)
		if len(items[i].EvidenceIDs) == 0 {
			items[i].Priority = lowerPriority(items[i].Priority)
			items[i].Confidence = minFloat(items[i].Confidence, 0.35)
			if !strings.Contains(items[i].Detail, "证据不足") {
				items[i].Detail = strings.TrimSpace(items[i].Detail + "（证据不足，需人工确认。）")
			}
		}
	}
	return items
}

func normalizeLLMEvolutionItems(items []contracts.EvolutionItem, evidenceMap map[string]contracts.AnalysisEvidenceItem) []contracts.EvolutionItem {
	for i := range items {
		items[i].Source = "llm"
		items[i].Target = normalizeEvolutionTarget(items[i].Target)
		if items[i].Priority == "" {
			items[i].Priority = "P2"
		}
		if items[i].Confidence == 0 {
			items[i].Confidence = 0.65
		}
		items[i].EvidenceIDs = validReportEvidenceIDs(items[i].EvidenceIDs, evidenceMap)
		if len(items[i].EvidenceIDs) == 0 {
			items[i].Priority = lowerPriority(items[i].Priority)
			items[i].Confidence = minFloat(items[i].Confidence, 0.35)
			items[i].Risks = append(items[i].Risks, "缺少有效 evidence_id，需人工确认后再执行。")
		}
	}
	return items
}

func validReportEvidenceIDs(ids []string, evidence map[string]contracts.AnalysisEvidenceItem) []string {
	var out []string
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := evidence[id]; ok {
			out = appendUniqueString(out, id)
		}
	}
	return out
}

func reportHealthScore(s contracts.ReportSummary) float64 {
	compile := pct(s.CompilePassRate)
	test := pct(firstNonZero(s.SampleTestPassRate, s.TestPassRate))
	coverage := pct(s.AvgLineCoverage)
	mutation := pct(s.AvgMutationScore)
	return 0.3*compile + 0.3*test + 0.2*coverage + 0.2*mutation
}

func pct(v float64) float64 {
	if v <= 1 {
		return v * 100
	}
	return v
}

func bestLabel(row contracts.ModelRank) string {
	return firstNonEmpty(row.SubjectID, row.Model)
}

func weakestLanguage(items []contracts.LanguageDim) (contracts.LanguageDim, bool) {
	if len(items) == 0 {
		return contracts.LanguageDim{}, false
	}
	bestIdx := 0
	bestScore := 999999.0
	for i, item := range items {
		score := 0.3*pct(item.CompilePassRate) + 0.3*pct(item.AvgTestPassRate) + 0.2*pct(item.AvgLineCoverage) + 0.2*pct(item.AvgMutationScore)
		if strings.EqualFold(item.Language, "java") && pct(item.AvgMutationScore) < 60 {
			score -= 10
		}
		if score < bestScore {
			bestScore = score
			bestIdx = i
		}
	}
	return items[bestIdx], true
}

func weakestScenario(items []contracts.ModelScenarioDim) (contracts.ModelScenarioDim, bool) {
	if len(items) == 0 {
		return contracts.ModelScenarioDim{}, false
	}
	weak := weakScenarios(items, 1)
	if len(weak) == 0 {
		return contracts.ModelScenarioDim{}, false
	}
	return weak[0], true
}

func weakScenarios(items []contracts.ModelScenarioDim, max int) []contracts.ModelScenarioDim {
	out := append([]contracts.ModelScenarioDim(nil), items...)
	sort.SliceStable(out, func(i, j int) bool {
		scoreI := 0.3*pct(out[i].CompilePassRate) + 0.3*pct(out[i].AvgTestPassRate) + 0.2*pct(out[i].AvgLineCoverage) + 0.2*pct(out[i].AvgMutationScore)
		scoreJ := 0.3*pct(out[j].CompilePassRate) + 0.3*pct(out[j].AvgTestPassRate) + 0.2*pct(out[j].AvgLineCoverage) + 0.2*pct(out[j].AvgMutationScore)
		return scoreI < scoreJ
	})
	if len(out) > max {
		out = out[:max]
	}
	return out
}

func bestSkillUplift(items []contracts.SkillUpliftRow) (contracts.SkillUpliftRow, bool) {
	var best contracts.SkillUpliftRow
	bestScore := 0.0
	for _, item := range items {
		score := pct(item.CompilePassDelta) + pct(item.TestPassDelta) + pct(item.LineCoverageDelta) + pct(item.MutationScoreDelta)
		if score > bestScore {
			best = item
			bestScore = score
		}
	}
	return best, bestScore > 0
}

func inefficientModel(items []contracts.ModelDim) (contracts.ModelDim, bool) {
	if len(items) < 2 {
		return contracts.ModelDim{}, false
	}
	var tokenValues []float64
	totalScore := 0.0
	for _, item := range items {
		if item.AvgTotalTokens > 0 {
			tokenValues = append(tokenValues, item.AvgTotalTokens)
		}
		totalScore += pct(item.CompositeScore)
	}
	if len(tokenValues) == 0 {
		return contracts.ModelDim{}, false
	}
	sort.Float64s(tokenValues)
	median := tokenValues[len(tokenValues)/2]
	if len(tokenValues)%2 == 0 {
		median = (tokenValues[len(tokenValues)/2-1] + tokenValues[len(tokenValues)/2]) / 2
	}
	avgScore := totalScore / float64(len(items))
	var best contracts.ModelDim
	found := false
	for _, item := range items {
		if item.AvgTotalTokens >= median*1.5 && pct(item.CompositeScore) <= avgScore {
			if !found || item.AvgTotalTokens > best.AvgTotalTokens {
				best = item
				found = true
			}
		}
	}
	return best, found
}

func limitModelRanks(items []contracts.ModelRank, max int) []contracts.ModelRank {
	if len(items) <= max {
		return items
	}
	return items[:max]
}

func bottomModelRanks(items []contracts.ModelRank, max int) []contracts.ModelRank {
	if len(items) <= max {
		return append([]contracts.ModelRank(nil), items...)
	}
	return append([]contracts.ModelRank(nil), items[len(items)-max:]...)
}

func limitSkillUplifts(items []contracts.SkillUpliftRow, max int) []contracts.SkillUpliftRow {
	out := append([]contracts.SkillUpliftRow(nil), items...)
	sort.SliceStable(out, func(i, j int) bool {
		scoreI := pct(out[i].CompilePassDelta) + pct(out[i].TestPassDelta) + pct(out[i].LineCoverageDelta) + pct(out[i].MutationScoreDelta)
		scoreJ := pct(out[j].CompilePassDelta) + pct(out[j].TestPassDelta) + pct(out[j].LineCoverageDelta) + pct(out[j].MutationScoreDelta)
		return scoreI > scoreJ
	})
	if len(out) > max {
		out = out[:max]
	}
	return out
}

func limitRootCauses(items []contracts.AnalysisRootCause, max int) []contracts.AnalysisRootCause {
	if len(items) <= max {
		return items
	}
	return items[:max]
}

func limitOptimizationItemsForReport(items []contracts.OptimizationItem, max int) []contracts.OptimizationItem {
	if len(items) <= max {
		return items
	}
	return items[:max]
}

func optimizationRefsByTarget(plan *contracts.OptimizationPlan, target string) []string {
	if plan == nil {
		return nil
	}
	var out []string
	target = normalizeEvolutionTarget(target)
	for _, item := range plan.Items {
		if normalizeEvolutionTarget(item.Target) == target {
			out = appendUniqueString(out, item.ID)
		}
		if len(out) >= 3 {
			break
		}
	}
	return out
}

func normalizeEvolutionTarget(target string) string {
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

func dedupeReportInsights(items []contracts.ReportInsight) []contracts.ReportInsight {
	best := map[string]contracts.ReportInsight{}
	order := []string{}
	rank := severityRank()
	for _, item := range items {
		key := strings.Join([]string{item.Category, strings.ToLower(strings.TrimSpace(item.Title))}, "|")
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
	out := make([]contracts.ReportInsight, 0, len(order))
	for _, key := range order {
		out = append(out, best[key])
	}
	return out
}

func sortReportInsights(items []contracts.ReportInsight) {
	rank := severityRank()
	sort.SliceStable(items, func(i, j int) bool {
		if rank[items[i].Priority] != rank[items[j].Priority] {
			return rank[items[i].Priority] < rank[items[j].Priority]
		}
		if sourceRank(items[i].Source) != sourceRank(items[j].Source) {
			return sourceRank(items[i].Source) < sourceRank(items[j].Source)
		}
		return items[i].Category < items[j].Category
	})
	for i := range items {
		if strings.TrimSpace(items[i].ID) == "" {
			items[i].ID = fmt.Sprintf("ri-%03d", i+1)
		}
	}
}

func dedupeEvolutionItems(items []contracts.EvolutionItem) []contracts.EvolutionItem {
	best := map[string]contracts.EvolutionItem{}
	order := []string{}
	rank := severityRank()
	for _, item := range items {
		key := strings.Join([]string{normalizeEvolutionTarget(item.Target), strings.ToLower(strings.TrimSpace(item.Title))}, "|")
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
	out := make([]contracts.EvolutionItem, 0, len(order))
	for _, key := range order {
		out = append(out, best[key])
	}
	return out
}

func sortEvolutionItems(items []contracts.EvolutionItem) {
	rank := severityRank()
	sort.SliceStable(items, func(i, j int) bool {
		if rank[items[i].Priority] != rank[items[j].Priority] {
			return rank[items[i].Priority] < rank[items[j].Priority]
		}
		if sourceRank(items[i].Source) != sourceRank(items[j].Source) {
			return sourceRank(items[i].Source) < sourceRank(items[j].Source)
		}
		return items[i].Target < items[j].Target
	})
	for i := range items {
		if strings.TrimSpace(items[i].ID) == "" {
			items[i].ID = fmt.Sprintf("evo-%03d", i+1)
		}
	}
}

func buildEvolutionSummary(items []contracts.EvolutionItem) string {
	if len(items) == 0 {
		return "当前报告未形成明确自进化优先级。"
	}
	first := items[0]
	return fmt.Sprintf("下一轮优先处理 %s：%s。", first.Target, first.Title)
}
