# Agent 自迭代优化闭环设计

本文档聚焦当前负责方向：利用 UT-Bench 已有评测数据、报告指标、失败日志、生成测试、Agent Trace/Trajectory，让 AI 自动分析问题、给出优化建议，进一步生成 Skill 修改方案，并通过再次评测验证效果，形成“自迭代”或“类自进化”的闭环。

本方向暂不负责评测集建设。评测集管理、分级、补样本是平台另一条主线；本文只假设已有一批可运行的 evaluation/report/trace 数据。

## 1. 目标定义

目标不是让模型“自己变聪明”，也不是训练模型权重，而是让 UT-Bench 具备一个围绕 Skill/Prompt/Agent 配置的外部优化循环：

```text
运行评测
  -> 收集结果指标、失败日志、生成测试、trajectory
  -> AI 诊断失败原因和低分指标
  -> 生成可执行优化建议
  -> 产出 Skill/Prompt 修改 patch
  -> 跑 smoke/small 回归评测
  -> 对比优化前后指标
  -> 人工确认后合入
```

这里的“自进化”更准确地说是：

- 不更新底层模型权重。
- 不直接让 AI 无限制改项目。
- 通过评测反馈不断改进外部策略资产，例如 Skill、Prompt、Agent 配置、工具使用约束。
- 每次改动都必须经过评测验证。

## 2. 当前已有输入

UT-Bench 已经具备较多可用于分析的数据，这些就是闭环第一版的燃料：

| 输入 | 来源 | 用途 |
| --- | --- | --- |
| `evaluation_result.json` | evaluator | 判断编译、测试、覆盖率、变异得分、失败阶段 |
| `report_summary.json` | reporter | 聚合维度、排名、skill uplift、agent comparison |
| `generated_manifest.json` | runner | 找到生成测试文件、prompt、metadata、trace 路径 |
| `*.trace.jsonl` | runner | 当前摘要型 Agent 行为记录 |
| `*.trajectory.json` | runner | step-by-step 行为轨迹 |
| `*.raw_trace.jsonl` | runner | 官方 CLI 原始事件流 |
| `*.stdout.log` / `*.stderr.log` | runner | 完整日志与错误输出 |
| `*.diff.json` | runner | 判断 Agent 是否修改源文件或异常写文件 |
| 生成的测试文件 | runner | 分析测试质量、断言、覆盖策略、错误写法 |
| 源文件与样本元数据 | dataset | 理解被测代码和语言场景 |

第一版不要追求“全自动改好”。更现实的目标是：先让 AI 能基于这些证据给出可靠诊断，并把建议落到具体 Skill 修改点。

## 3. 参考方法论

### 3.1 Reflexion

参考：[Reflexion: Language Agents with Verbal Reinforcement Learning](https://arxiv.org/abs/2303.11366)

核心思想：

- Agent 不通过训练权重学习，而是通过自然语言反思学习。
- 每次任务执行后，把反馈信号和执行轨迹交给反思模块。
- 反思结果存入 episodic memory，用于下一次尝试。

对 UT-Bench 的启发：

- 评测结果就是反馈信号。
- trajectory 就是执行轨迹。
- Skill 优化建议可以看作“可持久化的反思记忆”。
- 不应该只保存“本次失败原因”，还要保存“下次遇到类似样本应怎么做”。

### 3.2 Self-Refine

参考：[Self-Refine: Iterative Refinement with Self-Feedback](https://arxiv.org/abs/2303.17651)

核心思想：

- 先生成初版输出。
- 再让模型对输出给反馈。
- 再根据反馈修改输出。
- 循环多轮，直到质量达到标准或达到上限。

对 UT-Bench 的启发：

- Skill 修改可以采用“候选 -> critique -> revise”的结构。
- 不要让一个模型一步到位改 Skill，至少要拆成诊断、建议、patch、审查四步。
- 每轮都要有停止条件，例如指标提升、无明显提升、成本上限、轮数上限。

### 3.3 DSPy Optimizers

参考：[DSPy Optimizers](https://github.com/stanfordnlp/dspy/blob/main/docs/docs/learn/optimization/optimizers.md)、[MIPROv2](https://github.com/stanfordnlp/dspy/blob/main/docs/docs/api/optimizers/MIPROv2.md)

核心思想：

- 把 prompt/instruction/few-shot examples 当作可优化参数。
- 用 metric 驱动优化，而不是只凭人工经验写 prompt。
- 可以从少量样本开始，通过候选生成和搜索找到更好的指令。

对 UT-Bench 的启发：

- Skill 本质上也是一组可优化指令和示例。
- `mutation_score`、`coverage`、`compile_success`、`pass_rate` 可以作为优化目标。
- 第一版不需要复杂贝叶斯优化，但可以保留候选池和评分表，为后续扩展做准备。

### 3.4 TextGrad

参考：[TextGrad](https://github.com/zou-group/textgrad)

核心思想：

- 用 LLM 生成文本反馈，近似“梯度”。
- 用这些反馈优化 prompt、方案、复杂 AI 系统中的文本变量。

对 UT-Bench 的启发：

- 低分指标可以转成“文本梯度”，例如“当前 Skill 没有要求 agent 优先运行现有测试入口”。
- 优化对象可以是 Skill 文档、Prompt 模板、Agent instruction。
- 反馈必须绑定证据，否则会变成空泛建议。

### 3.5 LangGraph Evaluator-Optimizer

参考：[LangGraph evaluator-optimizer workflow](https://docs.langchain.com/oss/python/langgraph/workflows-agents)

核心思想：

- 一个生成器产出候选。
- 一个评估器给结构化反馈。
- 如果不满足标准，就带着反馈再次生成。

对 UT-Bench 的启发：

- 可以把闭环拆成固定节点，而不是让一个 Agent 自由发挥。
- 推荐节点：`diagnose -> propose -> patch -> review -> evaluate -> compare -> decide`。
- 每个节点都输出结构化 JSON，便于入库和复盘。

### 3.6 OpenEvolve / AlphaEvolve 类思路

参考：[OpenEvolve](https://github.com/algorithmicsuperintelligence/openevolve)

核心思想：

- 保留候选程序群体。
- 用评估函数筛选更优候选。
- 多轮生成、评估、选择，让代码逐步优化。

对 UT-Bench 的启发：

- Skill patch 可以维护候选池，而不是只保留最后一个。
- 对候选按多目标评分：提升幅度、稳定性、成本、失败风险、可解释性。
- 不急着做完整进化算法，但可以先保留 `candidate_id`、`parent_id`、`score_delta` 等字段。

### 3.7 Hermes Agent Self-Evolution

参考：

- [Hermes Agent Self-Evolution](https://github.com/NousResearch/hermes-agent-self-evolution)
- [Hermes Agent Self-Evolution PLAN](https://github.com/NousResearch/hermes-agent-self-evolution/blob/main/PLAN.md)
- [Hermes Learning Loop](https://hermes-agent.ai/features/learning-loop)

Hermes 这一类方案和本项目最贴近。它的核心不是训练底层模型，而是把 skill、prompt、tool description、system prompt section 等文本资产当作可优化对象，通过执行轨迹、评测数据和约束门禁生成更好的候选版本。

Hermes Agent Self-Evolution 的公开 README 中描述的主循环大致是：

```text
读取当前 skill / prompt / tool
  -> 生成或读取 eval dataset
  -> 使用 GEPA / DSPy 读取 execution traces 并提出候选变体
  -> 评测候选变体
  -> 通过测试、大小、语义保持等约束门禁
  -> 产出最佳变体，并以 PR 形式进入人工审查
```

它对 UT-Bench 的启发非常直接：

- 我们的 `Skill.md` 可以视为 Hermes 里的可优化 skill file。
- 我们的 `evaluation_result.json`、`report_summary.json`、`trajectory.json` 可以视为 optimizer 的反馈数据。
- 我们不需要一开始实现完整 GEPA，但可以先实现“诊断 -> 候选修改 -> 评测 -> 选择”的轻量版本。
- 生成的 Skill 修改不能直接落主干，应该先生成候选 patch，再经过测试和人工确认。
- 约束门禁很重要：不能过拟合单个样本，不能让 Skill 无限膨胀，不能偏离“生成单测”这个语义目标。

Hermes 的 guardrails 也值得直接借鉴：

| Guardrail | 对 UT-Bench 的落地方式 |
| --- | --- |
| 测试必须通过 | 候选 Skill 至少跑 smoke/small 评测 |
| Skill 大小限制 | `SKILL.md` 设置最大长度，避免 prompt bloat |
| 语义保持 | 修改必须仍服务于单测生成，不引入无关流程 |
| 不在会话中途热替换 | 候选只影响下一轮评测，不污染当前 run |
| PR / 人工审查 | 自动生成 patch 和报告，但合入由人确认 |

因此，本项目更适合采用“Hermes-lite”路线：

```text
UT-Bench run
  -> analyzer 诊断
  -> skill_advisor 生成建议
  -> skill_candidate 生成 patch
  -> smoke/small re-run
  -> compare 指标变化
  -> human review
```

这条路线比“让 Agent 自己无限修改自己”稳得多，也更容易在周会或答辩里解释清楚。

## 4. 建议架构

建议新增模块：

```text
internal/analyzer/
  service.go
  loader.go
  rule_diagnoser.go
  llm_diagnoser.go
  skill_advisor.go
  patch_planner.go
  regression_planner.go
  markdown.go
```

建议新增命令：

```bash
./utbench analyze \
  --evaluation ./artifacts/runs/<run-id>/evaluation/evaluation_result.json \
  --report ./artifacts/runs/<run-id>/report/report_summary.json \
  --manifest ./artifacts/runs/<run-id>/generated/generated_manifest.json \
  --output ./artifacts/runs/<run-id>/analysis \
  --no-llm
```

后续带 LLM：

```bash
./utbench analyze \
  --evaluation ./artifacts/runs/<run-id>/evaluation/evaluation_result.json \
  --report ./artifacts/runs/<run-id>/report/report_summary.json \
  --manifest ./artifacts/runs/<run-id>/generated/generated_manifest.json \
  --llm-model deepseek \
  --output ./artifacts/runs/<run-id>/analysis
```

Skill 优化建议：

```bash
./utbench optimize-skill \
  --analysis ./artifacts/runs/<run-id>/analysis/analysis_report.json \
  --skill ./skills/unit_test_skill \
  --output ./artifacts/runs/<run-id>/skill_candidates
```

第一阶段可以只实现 `analyze --no-llm`，先把数据通道和结构化产物搭起来。

## 5. 闭环节点设计

### 5.1 Load

读取已有产物并聚合到统一上下文：

- run 信息
- subject 信息
- 每个 sample 的 evaluation result
- generated case
- generated test file
- trace/trajectory
- stdout/stderr
- workspace diff

输出：`AnalysisContext`

### 5.2 Diagnose

先做规则诊断，再做 LLM 诊断。

规则诊断示例：

| 规则 | 可能结论 |
| --- | --- |
| 编译失败且错误包含 import/module not found | 测试生成没有正确处理依赖或包路径 |
| 测试通过但覆盖率低 | 测试只覆盖 happy path 或入口选择错误 |
| 覆盖率高但变异得分低 | 断言弱，缺少边界条件和异常路径 |
| trajectory 中没有读源文件 | Agent 可能盲写测试 |
| trajectory 中没有运行测试 | Agent 缺少自验证步骤 |
| diff 中包含源文件修改 | Agent 可能污染被测代码 |

LLM 诊断输入应该包含：

- 指标摘要
- 失败日志片段
- trajectory 关键步骤
- 生成测试文件片段
- 被测源文件片段
- 已触发的规则 findings

LLM 输出必须结构化，并包含 evidence。

### 5.3 Recommend

把诊断结果转成优化建议。

建议类型：

- Skill 指令增强
- Prompt 模板调整
- Agent 配置调整
- 工具使用约束
- 报告解释增强
- 数据/环境问题标记

建议要按优先级排序：

| 优先级 | 含义 |
| --- | --- |
| P0 | 影响编译/测试基本通过，必须先修 |
| P1 | 影响覆盖率、变异得分、断言质量 |
| P2 | 影响效率、成本、可解释性 |
| P3 | 长期改进建议 |

### 5.4 Patch

把建议转成候选修改。

第一版建议只允许修改 Skill 目录，不允许改 runner/evaluator 核心代码：

```text
skills/<skill-name>/
  SKILL.md
  examples/
  templates/
```

每个候选 patch 要有：

- `candidate_id`
- `parent_skill_version`
- `target_files`
- `rationale`
- `expected_metric_delta`
- `risk`
- `patch`

### 5.5 Review

patch 生成后先做静态审查：

- 是否只改允许目录。
- 是否包含与证据无关的大改。
- 是否引入空泛表述。
- 是否破坏原有 Skill 结构。
- 是否过拟合单个样本。

第一版可以规则审查；第二版再加 LLM reviewer。

### 5.6 Re-evaluate

用小评测集验证候选。

建议顺序：

```text
smoke -> small -> medium -> full
```

每次优化默认只跑 smoke/small，避免成本失控。只有候选明显提升后，才进入 medium/full。

### 5.7 Compare

对比优化前后：

- 总分变化
- 编译通过率变化
- 测试通过率变化
- 覆盖率变化
- 变异得分变化
- token/耗时变化
- 失败样本新增或减少
- 是否出现回归

输出结论：

```text
accept / reject / needs_human_review
```

## 6. 数据结构建议

### 6.1 AnalysisReport

```go
type AnalysisReport struct {
    SchemaVersion string `json:"schema_version"`
    RunID string `json:"run_id"`
    GeneratedAt time.Time `json:"generated_at"`
    SourceEvaluation string `json:"source_evaluation"`
    SourceReport string `json:"source_report"`
    SourceManifest string `json:"source_manifest"`
    Summary AnalysisSummary `json:"summary"`
    Findings []AnalysisFinding `json:"findings"`
    SubjectAnalyses []SubjectAnalysis `json:"subject_analyses"`
    SampleAnalyses []SampleAnalysis `json:"sample_analyses"`
    SkillRecommendations []SkillRecommendation `json:"skill_recommendations"`
    OptimizationPlan OptimizationPlan `json:"optimization_plan"`
}
```

### 6.2 AnalysisFinding

```go
type AnalysisFinding struct {
    ID string `json:"id"`
    Severity string `json:"severity"` // critical / high / medium / low
    Category string `json:"category"` // compile / test / coverage / mutation / trace / skill
    SubjectID string `json:"subject_id,omitempty"`
    SampleID string `json:"sample_id,omitempty"`
    Title string `json:"title"`
    Detail string `json:"detail"`
    Evidence []EvidenceRef `json:"evidence"`
    Recommendation string `json:"recommendation"`
    Confidence float64 `json:"confidence"`
}
```

### 6.3 SkillRecommendation

```go
type SkillRecommendation struct {
    ID string `json:"id"`
    Skill string `json:"skill"`
    Priority string `json:"priority"`
    TargetMetric string `json:"target_metric"`
    Problem string `json:"problem"`
    ProposedChange string `json:"proposed_change"`
    Evidence []EvidenceRef `json:"evidence"`
    ExpectedImpact string `json:"expected_impact"`
    Risk string `json:"risk"`
}
```

### 6.4 SkillCandidate

```go
type SkillCandidate struct {
    CandidateID string `json:"candidate_id"`
    ParentCandidateID string `json:"parent_candidate_id,omitempty"`
    Skill string `json:"skill"`
    BasedOnAnalysis string `json:"based_on_analysis"`
    PatchPath string `json:"patch_path"`
    Rationale string `json:"rationale"`
    ExpectedMetricDelta map[string]float64 `json:"expected_metric_delta,omitempty"`
    Status string `json:"status"` // proposed / reviewed / evaluated / accepted / rejected
}
```

## 7. 第一版实现边界

第一版不要做太大，建议只做到：

1. `utbench analyze --no-llm`
2. 读取 evaluation/report/manifest/trajectory
3. 输出 `analysis_report.json`
4. 输出 `analysis_report.md`
5. 规则诊断失败样本和低分 subject
6. 给出 Skill 级优化建议，但不自动改文件

第二版再做：

1. LLM diagnoser
2. evidence-based prompt
3. 生成 Skill patch 候选
4. patch 静态审查

第三版再做：

1. 自动跑 smoke/small 回归
2. 优化前后对比
3. 候选 accept/reject
4. Web UI 展示优化历史

## 8. 风险控制

### 8.1 防止幻觉建议

所有 AI 结论必须引用证据：

- 指标
- 失败日志
- trajectory step
- 生成测试代码
- diff

没有证据的建议只能标记为 `hypothesis`，不能作为 patch 依据。

### 8.2 防止过拟合样本

Skill 优化不能只针对单个失败样本。建议至少满足：

- 多个样本出现相似问题。
- 或者某个问题影响 P0 基础能力。
- 或者在同一 subject 的多个语言/场景中重复出现。

### 8.3 防止无界自改

优化 agent 不能直接改核心代码。第一阶段只允许：

- 生成建议
- 生成 patch 文件
- 修改指定 Skill 目录

核心 runner/evaluator/reporter 修改必须人工确认。

### 8.4 防止成本失控

默认策略：

- 优先分析失败样本和低分 subject。
- 限制 LLM 分析样本数。
- 默认 smoke/small 回归。
- 记录每次优化成本。

## 9. 与当前代码的关系

当前已完成的 trace/trajectory 改造，是本闭环的证据输入层。

后续最自然的实现顺序：

```text
contracts: 增加 AnalysisReport 等结构
analyzer: 实现规则诊断
cmd: 增加 analyze 命令
report/web: 链接 analysis_report
llm: 增加诊断 prompt
optimizer: 生成 skill patch 候选
orchestrator: 支持小集回归验证
```

第一步建议先实现 `contracts + analyzer + analyze --no-llm`，因为它不依赖模型 API，也不需要先解决自动改 Skill 的风险。

## 10. 阶段验收标准

### V1：分析报告可用

- 给定一次 run，能生成 analysis report。
- 能解释主要失败类型。
- 能指出低分 subject 的弱项。
- 能读 trajectory 并识别关键行为问题。

### V2：AI 诊断可信

- LLM 诊断包含 evidence。
- 人工检查时，大部分结论能对应到真实日志或代码。
- 输出建议能落到 Skill/Prompt/Agent 配置，不只是泛泛而谈。

### V3：Skill 优化可验证

- 能生成候选 Skill patch。
- 能自动跑 smoke/small 回归。
- 能比较优化前后指标。
- 能给出 accept/reject/needs_review。

### V4：形成优化历史

- 每个候选都有来源、证据、patch、评测结果。
- 可以追溯某次 Skill 改动为什么做、带来什么变化。
- Web UI 或报告可以展示优化链路。

## 11. 推荐近期任务

接下来最值得做的是：

1. 定义 `AnalysisReport`、`AnalysisFinding`、`SkillRecommendation` 合约。
2. 实现 `utbench analyze --no-llm`。
3. 从现有 evaluation/report/manifest/trajectory 中生成规则诊断。
4. 输出 markdown 报告，方便周会展示。
5. 再设计 LLM diagnoser prompt 和 Skill patch candidate 格式。

这样做能最快把“自进化”从概念落成可演示能力：虽然第一版还不会自动改 Skill，但它已经能证明 UT-Bench 可以基于评测反馈给出结构化优化方向。

如果按 Hermes-lite 路线继续推进，建议把后续任务拆成两层：

| 层级 | 目标 | 产物 |
| --- | --- | --- |
| Diagnose Loop | 先证明系统能从评测数据中发现问题 | `analysis_report.json/md`、规则 findings、Skill 建议 |
| Optimize Loop | 再证明系统能生成候选修改并用评测筛选 | `skill_candidate.json`、patch 文件、优化前后对比报告 |

短期先完成 Diagnose Loop，之后再进入 Optimize Loop。这样能控制风险，也能让老师看到一条清晰演进路线：先“会分析”，再“会建议”，最后“会验证改动是否真的变好”。
