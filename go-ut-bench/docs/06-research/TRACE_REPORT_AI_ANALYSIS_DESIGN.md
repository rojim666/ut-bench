# 评测报告与 Trace AI 分析设计方案

> 文档定位：本方案用于指导 UT-Bench 后续建设“评测结果 + Agent Trace”的自动诊断、优化建议与自迭代闭环能力。  
> 适用范围：报告分析、Trace 分析、Agent/Skill 对比、失败归因、Skill 优化建议、后续 Web UI 展示与自动化流程。

> 上层总纲：本方案是 [UTBENCH_PLATFORM_UPGRADE_ROADMAP.md](UTBENCH_PLATFORM_UPGRADE_ROADMAP.md) 中“Trace / Trajectory 证据层”和“AI 诊断与优化闭环”的子方案。不要把 Trace 视为独立主线；它服务于更大的平台化升级目标。

---

## 1. 背景与目标

UT-Bench 当前已经能够完成从样本发现、单测生成、评测执行到报告生成的完整流程。现有报告可以给出模型、Agent、Skill 在编译通过率、测试通过率、覆盖率、变异得分、断言密度、成本和耗时等维度上的结果。

但评测的核心价值不应该停留在“谁分数最高”。更重要的是回答：

1. 为什么某个 Agent 或 Skill 得分低？
2. 是生成代码质量差，还是执行环境、数据集、评测器本身有问题？
3. 某个 Skill 相比 no_skill 到底提升了什么，又拖累了什么？
4. 失败样本背后有没有共同模式？
5. Agent 的执行过程是否合理，例如是否读取源码、是否运行测试、是否修改了不该修改的文件？
6. 下一步应该如何优化 prompt、skill、agent 配置或数据集？

因此，本方向的目标是建设一个“诊断与优化闭环”：

```text
评测结果 + 报告指标 + Agent Trace + 生成文件 + 错误日志
        ↓
规则分析 + LLM 诊断
        ↓
问题归因 + 优先级排序 + 优化建议
        ↓
Skill / Prompt / Agent 配置改进
        ↓
再次评测，验证改进是否有效
```

最终希望 UT-Bench 不只是一个排行榜工具，而是一个能帮助开发者持续优化单测生成 Agent 的分析平台。

---

## 2. 当前代码现状

### 2.1 已有报告能力

当前报告生成逻辑主要位于：

- `internal/reporter/service.go`
- `internal/reporter/service_insights.go`
- `internal/reporter/ranking.go`
- `internal/contracts/results.go`

现有 `ReportPayload` 已经包含较丰富的数据：

- `Summary`：总体统计，例如样本数、编译通过率、测试通过率、覆盖率、变异得分、断言密度。
- `Dimensions`：按模型、语言、场景等维度聚合。
- `TopModels`：综合得分排名。
- `Failures`：失败样本列表。
- `Insights`：规则生成的洞察。
- `EfficiencyStats`：token、耗时、成本效率。
- `ErrorDiagnosis`：编译错误、测试错误的规则分类。
- `AgentComparisons`：Agent 相对纯模型 API 的提升。
- `SkillUplifts`：Skill 相对 no_skill 的提升。
- `ComparisonViews`：控制变量对比视图。

这说明报告侧已经具备“分析入口”，后续可以在其基础上新增 AI 分析层，而不需要重写报告系统。

### 2.2 已有 Trace 能力

当前 Agent Trace 结构主要位于：

- `internal/runner/adapter.go`
- `internal/runner/adapter_cli.go`

已有 `AgentTrace` 结构，字段包括：

- 基础标识：`subject_id`、`framework`、`model`、`skill`、`sample_id`、`language`
- 执行信息：`command`、`exit_code`、`duration_ms`、`started_at`、`finished_at`
- token 与成本：`prompt_tokens`、`completion_tokens`、`total_tokens`、`estimated_cost`
- 交互过程：`interaction_count`、`tool_calls`
- 文件行为：`files_read`、`files_written`
- 命令行为：`commands_executed`
- 输出：`stdout`、`stderr`
- 工作区变化：`workspace_diff`
- 环境信息：`sandbox_provider`、`sandbox_image`、`sandbox_fingerprint`
- 产物路径：`trace_path`、`workspace_diff_path`

CLI Agent 执行链路会：

1. 创建独立 workspace。
2. 注入 skill。
3. 执行 Agent 命令。
4. 保存 trace 文件。
5. 保存 workspace diff。
6. 从 stdout/stderr 中解析工具调用、命令、文件读写。
7. 将 trace 摘要写入 `GeneratedCase`。

这说明当前已经具备第一版 Trace 数据基础。后续重点是：

- 统一 Trace 分析模型。
- 将 Trace 与 `EvaluationResult`、`ReportPayload` 关联。
- 基于 Trace 做行为评分、失败归因和优化建议。

### 2.3 当前不足

当前能力仍有以下缺口：

| 缺口 | 影响 |
| --- | --- |
| Trace 还不是 step-by-step trajectory | 难以准确还原 Agent 每一步 action 和 observation |
| Trace 与评测失败结果缺少统一诊断层 | 报告知道哪里失败，但不知道为什么失败 |
| 规则洞察较粗 | 只能给出通用建议，难以针对某个 Skill 或样本给出具体优化 |
| 缺少 LLM-as-Judge | 不能自动判断 Agent 策略是否合理 |
| 缺少分析产物格式 | 目前没有独立的 `analysis_report.json/md` |
| 缺少 Skill 优化建议闭环 | 还不能从评测结果反向生成可执行的 Skill 改进建议 |
| Web UI 尚未展示 Trace 诊断 | 用户难以直观看到 Agent 行为模式 |

---

## 3. 业界参考

### 3.1 Anthropic Agent Evals 方法论

参考文章：[Demystifying evals for AI agents](https://www.anthropic.com/engineering/demystifying-evals-for-ai-agents)

关键思想：

- 评测对象是一次完整 Trial，而不是单个模型输出。
- 每次 Trial 应保留 transcript，也就是完整执行记录。
- Outcome 和 transcript 要分开看：最终结果重要，执行过程也能帮助定位原因。
- Grader 可以分为 code-based、model-based、human 三类。
- 对 Agent 来说，不能只看 pass/fail，需要 partial credit 和多维度评分。
- 高质量 eval 应能支持 regression 检查，即版本升级后效果是否下降。

对 UT-Bench 的启发：

- `EvaluationResult` 对应 outcome。
- `AgentTrace` 对应 transcript。
- 当前编译、测试、覆盖率、变异测试是 code-based grader。
- 后续可以新增 `TraceGrader` 和 `LLMAnalysisGrader`。
- 分析结果不一定直接影响主排名，但应该用于解释和优化。

### 3.2 LangSmith / AgentEvals

参考：

- [LangSmith](https://docs.smith.langchain.com/)
- [AgentEvals](https://github.com/langchain-ai/agentevals)

关键思想：

- 对 Agent trajectory 进行评测。
- 支持工具调用路径匹配。
- 支持 LLM-as-Judge 判断 trajectory 是否合理。
- 可以将 trace、dataset、experiment、score 关联。

对 UT-Bench 的启发：

- 可以先实现规则型 trajectory 检查，例如是否读源码、是否运行测试、是否修改源文件。
- 再用 LLM 对失败样本做自然语言归因。
- Score 应该可以挂在 subject、sample、trace、run 四个层级上。

### 3.3 Langfuse / Phoenix / Braintrust

参考：

- [Langfuse](https://langfuse.com/docs)
- [Phoenix](https://phoenix.arize.com/)
- [Braintrust](https://www.braintrust.dev/docs)

关键思想：

- Trace 是一等数据对象。
- Score 可以关联到 trace、span、session、dataset run。
- 支持人工标注、规则评分、LLM-as-Judge。
- 支持实验对比和历史回归。

对 UT-Bench 的启发：

- `analysis_report` 应该是可持久化产物，不只是控制台输出。
- 后续数据库应支持保存分析结果，方便历史对比。
- Web UI 可以把 trace、score、recommendation 放在同一页面。

### 3.4 OpenTelemetry GenAI 规范

参考：[OpenTelemetry GenAI semantic conventions](https://opentelemetry.io/docs/specs/semconv/gen-ai/)

关键思想：

- 使用 trace/span/event 描述模型调用、工具调用、Agent 工作流。
- 每个 span 可以记录输入、输出、token、模型、工具、错误等信息。
- 标准化后可以接入通用可观测性系统。

对 UT-Bench 的启发：

- 短期继续使用当前 `AgentTrace` JSON 格式。
- 中期可以引入统一 `TrajectoryStep`。
- 长期可导出 OTLP/JSON，兼容外部 Trace 平台。

### 3.5 SWE-agent / OpenHands / AgentReplay

参考：

- [SWE-agent](https://github.com/SWE-agent/SWE-agent)
- [OpenHands](https://github.com/All-Hands-AI/OpenHands)
- [AgentReplay](https://github.com/agentreplay/agentreplay)

关键思想：

- SWE-agent 保留完整 trajectory，可用于复盘每一步命令和输出。
- OpenHands 使用 EventStream 表示 action/observation。
- AgentReplay 强调本地优先、OTLP 采集、Agent session 回放和成本分析。

对 UT-Bench 的启发：

- 未来 Trace 不应只存 stdout/stderr 摘要，而应该升级为结构化 action/observation 序列。
- Web UI 可以支持按时间线展示 Agent 行为。
- 对单测生成任务来说，最有价值的是识别“失败前 Agent 做错了哪一步”。

---

## 4. 建议的总体架构

建议新增一个独立模块：

```text
internal/analyzer/
```

职责：

1. 读取 `evaluation_result.json`。
2. 读取 `report_summary.json`。
3. 按 `trace_path` 读取 Agent Trace。
4. 聚合每个 subject / language / sample 的结果。
5. 运行规则型分析器。
6. 可选调用 LLM 生成诊断。
7. 输出 `analysis_report.json` 和 `analysis_report.md`。

推荐架构：

```text
contracts
  ├─ AnalysisReport
  ├─ AnalysisFinding
  ├─ TraceDiagnosis
  ├─ SkillRecommendation
  └─ OptimizationPlan

analyzer
  ├─ service.go
  ├─ loader.go
  ├─ trace_grader.go
  ├─ failure_classifier.go
  ├─ llm_diagnoser.go
  ├─ skill_advisor.go
  └─ markdown.go
```

建议新增 CLI：

```bash
./utbench analyze \
  --evaluation ./artifacts/runs/<run-id>/evaluation/evaluation_result.json \
  --report ./artifacts/runs/<run-id>/report/report_summary.json \
  --config ./configs/models.yaml \
  --llm-model deepseek \
  --output ./artifacts/runs/<run-id>/analysis
```

第一版也可以不调用 LLM：

```bash
./utbench analyze \
  --evaluation ./artifacts/runs/<run-id>/evaluation/evaluation_result.json \
  --report ./artifacts/runs/<run-id>/report/report_summary.json \
  --no-llm
```

---

## 5. 数据模型设计

### 5.1 AnalysisReport

建议新增结构：

```go
type AnalysisReport struct {
    SchemaVersion string `json:"schema_version"`
    RunID string `json:"run_id"`
    GeneratedAtUTC time.Time `json:"generated_at_utc"`

    SourceEvaluation string `json:"source_evaluation"`
    SourceReport string `json:"source_report"`

    Summary AnalysisSummary `json:"summary"`
    SubjectAnalyses []SubjectAnalysis `json:"subject_analyses"`
    FailureAnalyses []FailureAnalysis `json:"failure_analyses"`
    TraceAnalyses []TraceAnalysis `json:"trace_analyses"`
    SkillRecommendations []SkillRecommendation `json:"skill_recommendations"`
    OptimizationPlan OptimizationPlan `json:"optimization_plan"`
    LLMAnalysis *LLMAnalysisBlock `json:"llm_analysis,omitempty"`
}
```

### 5.2 AnalysisFinding

```go
type AnalysisFinding struct {
    ID string `json:"id"`
    Severity string `json:"severity"` // critical / high / medium / low
    Category string `json:"category"` // compile / test / coverage / mutation / trace / cost / skill
    Title string `json:"title"`
    Detail string `json:"detail"`
    Evidence []EvidenceRef `json:"evidence"`
    Recommendation string `json:"recommendation"`
}
```

### 5.3 TraceAnalysis

```go
type TraceAnalysis struct {
    SubjectID string `json:"subject_id"`
    Language string `json:"language"`
    SampleID string `json:"sample_id"`
    TracePath string `json:"trace_path"`

    TraceQualityScore float64 `json:"trace_quality_score"`
    StrategyScore float64 `json:"strategy_score"`
    EfficiencyScore float64 `json:"efficiency_score"`
    RiskScore float64 `json:"risk_score"`

    Flags []string `json:"flags"`
    Findings []AnalysisFinding `json:"findings"`
}
```

### 5.4 SkillRecommendation

```go
type SkillRecommendation struct {
    SkillName string `json:"skill_name"`
    Framework string `json:"framework"`
    Model string `json:"model"`
    Priority string `json:"priority"`
    Problem string `json:"problem"`
    Evidence []EvidenceRef `json:"evidence"`
    SuggestedChange string `json:"suggested_change"`
    TargetFile string `json:"target_file,omitempty"`
    PatchHint string `json:"patch_hint,omitempty"`
}
```

---

## 6. Trace Grader 设计

第一版建议先实现规则型 Trace Grader，不依赖 LLM，保证稳定可复现。

### 6.1 行为检查项

| 检查项 | 说明 | 风险 |
| --- | --- | --- |
| `read_source` | 是否读取被测源码 | 没读源码可能是盲写测试 |
| `write_test` | 是否写入测试文件 | 未产出测试直接失败 |
| `run_tests` | 是否运行测试命令 | 没有自验证，失败率可能高 |
| `modified_source` | 是否修改源代码 | 可能通过改源码绕过测试 |
| `forbidden_command` | 是否执行禁用命令 | 破坏环境或污染评测 |
| `excessive_steps` | 工具调用过多 | 效率低或陷入循环 |
| `no_file_read_before_write` | 写测试前没有读源码 | 策略风险高 |
| `stderr_error` | stderr 中出现明显错误 | Agent 执行阶段异常 |
| `empty_diff` | workspace 没有有效变化 | Agent 没有完成任务 |
| `test_file_not_found` | 未找到生成测试文件 | 输出约束未遵守 |

### 6.2 分数设计

Trace 分数不建议直接进入主排行榜，避免“惩罚创造性路径”。它更适合作为诊断指标。

建议：

```text
trace_quality_score = 0.35 * source_awareness
                    + 0.25 * verification_behavior
                    + 0.20 * output_discipline
                    + 0.10 * safety
                    + 0.10 * efficiency
```

各子项含义：

- `source_awareness`：是否读取源码、相关文件、prompt。
- `verification_behavior`：是否运行测试、是否根据错误继续修复。
- `output_discipline`：是否按要求生成测试文件，不输出无关文件。
- `safety`：是否避免修改源文件、安装依赖、执行危险命令。
- `efficiency`：工具调用、耗时、token 是否异常。

### 6.3 诊断示例

```json
{
  "subject_id": "codebuddy__deepseek-v4-flash__unit_test_skill",
  "language": "python",
  "sample_id": "boundary_001",
  "trace_quality_score": 0.62,
  "flags": ["no_test_run", "low_source_awareness"],
  "findings": [
    {
      "severity": "medium",
      "category": "trace",
      "title": "Agent 生成测试后未运行验证",
      "detail": "Trace 中没有检测到 pytest/go test/mvn test 等测试命令，后续评测阶段出现测试失败。",
      "recommendation": "在 skill 中明确要求生成测试后必须运行对应语言的测试命令，并根据错误修复一次。"
    }
  ]
}
```

---

## 7. 评测结果归因设计

### 7.1 归因层级

建议将失败原因拆为五类：

| 类别 | 说明 |
| --- | --- |
| `model_generation` | 模型生成的测试本身有语法、类型、断言逻辑问题 |
| `agent_strategy` | Agent 没有正确读取、验证、修复，执行策略有问题 |
| `skill_instruction` | Skill 指令不够明确，导致重复出现同类问题 |
| `environment` | 依赖、工具链、Docker、权限等环境问题 |
| `dataset_or_harness` | 样本定义、评测脚本、数据集结构存在问题 |

现有 `EvaluationResult.FailureOrigin` 已经有类似字段，可以继续沿用并细化。

### 7.2 指标到问题的映射

| 指标表现 | 可能原因 | 优化方向 |
| --- | --- | --- |
| 编译通过率低 | import 错误、包名错误、语法错误、类型误用 | 强化语言模板、包导入规则、示例 |
| 测试通过率低 | 断言逻辑错误、mock 错误、测试数据错误 | 强化行为理解、预期值推导、mock 策略 |
| 覆盖率低 | 只测 happy path，缺少边界/异常/分支 | 强化分支覆盖、边界条件、异常路径 |
| 变异得分低 | 断言弱、只执行不验证、测试过浅 | 强化断言质量、避免 smoke test |
| 断言密度异常高 | 堆砌断言，可能重复或无意义 | 引导有效断言，而不是数量优先 |
| token/耗时高但效果差 | Agent 反复尝试、上下文利用差 | 优化 agent workflow 和 stop 条件 |
| Trace 中未读源码 | Agent 没有理解目标代码 | Skill 强制先分析源码再生成 |
| Trace 中未跑测试 | 缺少自验证 | Skill 强制执行测试命令 |
| Trace 中修改源码 | 策略越界 | Skill 和 sandbox 均需禁止 |

---

## 8. LLM 诊断设计

### 8.1 输入内容

LLM 不应该直接吃完整所有日志，第一版应先做压缩摘要，避免成本过高。

推荐输入：

1. Run 基本信息。
2. 当前 subject 的指标摘要。
3. 与 baseline/no_skill 的 delta。
4. 失败样本 top N。
5. 每个失败样本的错误摘要。
6. Trace Grader 输出。
7. 关键 stdout/stderr 片段。
8. 当前 skill 指令摘要。

### 8.2 输出格式

要求 LLM 输出结构化 JSON：

```json
{
  "overall_diagnosis": "...",
  "root_causes": [
    {
      "category": "skill_instruction",
      "confidence": 0.78,
      "evidence": ["..."],
      "explanation": "..."
    }
  ],
  "recommendations": [
    {
      "priority": "high",
      "target": "skill",
      "change": "...",
      "expected_impact": "提高变异得分和覆盖率"
    }
  ],
  "risks": ["..."],
  "next_eval_plan": "..."
}
```

### 8.3 Prompt 要点

LLM 分析 prompt 应强调：

- 只能基于提供的评测结果和 trace 证据。
- 区分模型问题、Agent 策略问题、Skill 指令问题、环境问题、数据集问题。
- 不要只复述指标，要给出可执行建议。
- 建议必须和证据绑定。
- 如果证据不足，要明确说“不确定”。
- 不要建议为了得分而破坏评测公平性，例如修改源代码、跳过测试、降低断言。

---

## 9. Skill 优化闭环

### 9.1 优化对象

主要优化对象：

- `configs/skills/unit_test_skill/instructions.md`
- `configs/skills/unit_test_skill/checklist.md`
- `configs/skills/qta-ut/SKILL.md`
- 其他自定义 skill

### 9.2 优化原则

Skill 优化不能只追求某个指标，要避免过拟合数据集。

建议原则：

1. 优先修复高频、跨样本、跨语言的稳定问题。
2. 每条修改建议必须有评测证据。
3. 不针对单个样本硬编码。
4. 不降低测试质量要求。
5. 不鼓励修改被测源码。
6. 修改后必须重新跑同一评测集验证。
7. 如果某项指标上升但其他指标下降，需要在报告里说明 trade-off。

### 9.3 闭环流程

```text
Step 1: 跑 baseline/no_skill 和 skill 版本
Step 2: 生成 report_summary.json
Step 3: 运行 utbench analyze
Step 4: 输出 skill 优化建议
Step 5: 人工审查建议
Step 6: 修改 skill
Step 7: 重新评测
Step 8: 对比新旧 report
Step 9: 决定是否合入
```

### 9.4 后续自动化

成熟后可以新增：

```bash
./utbench optimize-skill \
  --analysis ./artifacts/runs/<run-id>/analysis/analysis_report.json \
  --skill ./configs/skills/unit_test_skill \
  --dry-run
```

第一阶段只生成 patch 建议，不自动改文件。第二阶段再支持生成候选 patch，由人工确认。

---

## 10. Web UI 设计建议

当前 Web UI 已有运行、报告、数据库等入口。后续可新增“分析”页面。

### 10.1 Run Detail 页面增强

在每个 run 页面新增：

- 综合诊断摘要
- Top 问题列表
- 优化建议列表
- Agent/Skill 对比解释
- Trace 风险告警

### 10.2 样本详情页

针对单个失败样本展示：

- `EvaluationResult` 指标
- 编译/测试/覆盖率/变异错误
- Agent Trace 摘要
- 工具调用时间线
- 文件读写列表
- workspace diff
- AI 归因结论

### 10.3 Skill 优化页

展示：

- 某个 skill 的提升项和拖累项
- 与 no_skill 的 delta
- 推荐修改
- 涉及证据样本
- 修改后再评测入口

---

## 11. 数据库与产物管理

### 11.1 文件产物

建议新增目录：

```text
artifacts/runs/<run-id>/analysis/
├── analysis_report.json
├── analysis_report.md
├── trace_scores.json
├── skill_recommendations.json
└── llm_diagnosis_raw.json
```

### 11.2 SQLite 扩展

后续可新增表：

```sql
analysis_reports(
  id,
  run_id,
  report_path,
  analysis_path,
  created_at_utc,
  analyzer_version
)

trace_scores(
  run_id,
  subject_id,
  language,
  sample_id,
  trace_quality_score,
  strategy_score,
  efficiency_score,
  risk_score,
  flags_json
)

skill_recommendations(
  run_id,
  skill_name,
  priority,
  problem,
  suggested_change,
  evidence_json,
  created_at_utc
)
```

第一版可以只写文件，不入库。第二版再入库，方便 Web 查询和跨 run 对比。

---

## 12. 实施路线图

### 阶段一：离线分析报告

目标：先把报告和 trace 串起来，生成可读分析文档。

任务：

- 新增 `internal/analyzer`。
- 新增 `utbench analyze` 命令。
- 读取 `evaluation_result.json`、`report_summary.json`、trace 文件。
- 实现规则型失败归因。
- 实现规则型 Trace Grader。
- 输出 `analysis_report.json` 和 `analysis_report.md`。

验收：

- 对已有 run 可生成分析报告。
- 报告能指出 top 问题、证据和建议。
- 不依赖外部 LLM 也能运行。

### 阶段二：LLM 诊断

目标：引入 AI 分析能力。

任务：

- 设计 LLM 诊断 prompt。
- 复用现有模型配置。
- 支持 `--llm-model` 和 `--no-llm`。
- 对失败样本和 subject 汇总做诊断。
- 输出结构化 JSON。

验收：

- 能解释低分原因。
- 建议能和 evidence 对齐。
- 成本可控，默认只分析 top N 问题。

### 阶段三：Skill 优化建议

目标：从诊断走向优化。

任务：

- 读取 skill 文件摘要。
- 将指标差异和 trace 归因映射为 skill 修改建议。
- 输出 `skill_recommendations.json`。
- 可选输出候选 patch。

验收：

- 能针对 coverage、mutation、compile、test failure 给出不同建议。
- 建议不硬编码样本。
- 人工可直接据此修改 skill。

### 阶段四：Web UI 集成

目标：让用户能直观看到分析结果。

任务：

- Run detail 增加“分析报告”入口。
- 样本详情展示 trace 时间线。
- Skill 页展示 uplift 和推荐修改。
- 数据库持久化分析结果。

验收：

- 用户无需打开 JSON 即可看到诊断。
- 能定位单个失败样本的 trace 和原因。

### 阶段五：自迭代闭环

目标：支持评测、分析、优化、再评测的半自动流程。

任务：

- 新增 `optimize-skill` 实验命令。
- 生成候选 patch。
- 自动触发小型评测集验证。
- 对比新旧报告。

验收：

- 能证明某次 skill 修改是否提升。
- 能识别指标 trade-off。
- 仍保留人工审核。

---

## 13. 风险与注意事项

### 13.1 LLM 诊断幻觉

LLM 可能给出没有证据的解释。解决方式：

- 所有结论必须引用 evidence。
- 输出 confidence。
- 对证据不足的情况允许“不确定”。
- 默认保留规则分析作为基线。

### 13.2 过拟合评测集

如果直接根据报告修改 skill，可能只对当前样本有效。解决方式：

- 修改建议必须来自多样本共性问题。
- 区分 capability eval 和 regression eval。
- 使用小型集验证后，还要用中型/全量集复测。

### 13.3 Trace 质量不稳定

不同 CLI Agent 输出格式差异较大。解决方式：

- 第一版只依赖通用字段：命令、文件读写、diff、stdout/stderr。
- 对不同 framework 增加 adapter-specific parser。
- 后续逐步引入统一 trajectory schema。

### 13.4 成本与性能

分析所有样本可能成本高。解决方式：

- 默认只分析失败样本和低分 subject。
- 支持 `--max-samples-for-llm`。
- 支持缓存 LLM 诊断结果。
- 支持 `--no-llm`。

### 13.5 主排名公平性

Trace 分数不建议直接进入主排名。原因是不同 Agent 的合理路径可能不同，过早把 trace 行为纳入总分会惩罚创造性策略。

建议：

- 主排名仍以 outcome 指标为主。
- Trace 分数作为诊断指标。
- 仅对明确违规行为做风险标注，例如修改源文件、执行禁用命令。

---

## 14. 推荐优先级

短期最值得做：

1. `utbench analyze --no-llm`
2. Trace Grader 规则集
3. Markdown 分析报告
4. Skill 优化建议模板

中期再做：

1. LLM 诊断
2. Web UI 展示
3. 数据库持久化
4. 跨 run 对比

长期方向：

1. 自动生成 skill patch。
2. 自动重跑小型评测集。
3. 形成 skill 自迭代闭环。
4. Trace 导出到 OTLP 或兼容外部平台。

---

## 15. 最小可交付版本

建议第一版交付范围控制为：

```text
命令：utbench analyze
输入：evaluation_result.json + report_summary.json + trace_path
输出：analysis_report.json + analysis_report.md
能力：
  - subject 级别指标解释
  - failure 级别归因
  - trace 行为评分
  - skill 优化建议
  - 不依赖 LLM 的规则分析
```

这样可以快速把会议中提到的“评测报告 + Trace 分析”落地为一个明确功能，并为后续 AI 自动优化 Skill 留出接口。

---

## 16. 官方 Agent Trace 适配策略

本项目当前要先解决“trace 是否足够完整”的问题。结论是：旧版 `AgentTrace` 更像摘要，适合看命令、文件读写、token 和 diff，但还不是业界常说的 step-by-step trajectory。后续分析 agent 需要同时保留两类信息：

1. 原始官方输出：不做裁剪，便于回放、补解析和排查 parser 漏洞。
2. 统一 trajectory：抽象成 message、tool_call、tool_result、command、raw_event 等步骤，便于跨 agent 对比。

三家官方 CLI 能力的详细调研见：[OFFICIAL_AGENT_TRACE_SOURCES.md](OFFICIAL_AGENT_TRACE_SOURCES.md)。

### 16.1 Claude Code

官方命令行支持 headless/非交互执行，并通过 `--output-format stream-json` 输出流式 JSON；通常需要配合 `--verbose` 才能拿到更完整的过程事件。可选的 partial message / hook event 信息也应尽量保留到原始输出中。

适配策略：
- 执行 Claude Code 时保存完整 stdout/stderr。
- 将 stdout/stderr 的 JSON 行落到 `raw_trace.jsonl`。
- 从 stream-json 中解析 assistant message、tool_use、tool_result、result/usage 等事件。
- 解析后的统一文件写入 `trajectory.json`。
- 所有无法识别的事件保留为 `raw_event`，避免官方字段升级后信息丢失。

### 16.2 CodeBuddy

CodeBuddy CLI 文档中 headless 场景支持 `--output-format stream-json`，输出从 init/system/user/assistant 到 result 的实时 JSON 事件，适合作为完整执行过程的来源。

适配策略：
- 与 Claude Code 共用 stream-json parser。
- 原始 stdout/stderr 必须完整保存，不能只保留当前 trace 中的截断摘要。
- 统一 trajectory 中保留 message、tool_call、tool_result 和 result/raw_event。
- 如果 CodeBuddy 后续输出更细的 usage、session、cost 字段，应优先放入 `raw_event`，再按需提升为结构化字段。

### 16.3 OpenCode

OpenCode 的更可靠来源是 session export：`opencode export <sessionID>` 可以导出 session JSON。运行输出中如果能得到 session id，应在执行结束后导出 session，并把导出文件路径写入 trace。

适配策略：
- 先从 stdout/stderr 中解析 session id。
- 执行后调用 OpenCode session export，保存 `session_export.json`。
- 优先从 session export 解析 message、tool_use、tool_result。
- 如果 session export 失败，则退回到 stdout/stderr 的文本日志解析。
- `trajectory.json` 中保留 `session_export_path`，方便后续人工检查和二次解析。

### 16.4 UT-Bench 统一产物约定

每个 agent 样本执行后建议保留以下产物：

```text
agent_traces/<subject>/<language>/<sample>.trace.jsonl
agent_traces/<subject>/<language>/<sample>.raw_trace.jsonl
agent_traces/<subject>/<language>/<sample>.stdout.log
agent_traces/<subject>/<language>/<sample>.stderr.log
agent_traces/<subject>/<language>/<sample>.trajectory.json
agent_traces/<subject>/<language>/<sample>.diff.json
```

字段含义：
- `trace.jsonl`：UT-Bench 自己的摘要型结构化 trace。
- `raw_trace.jsonl`：stdout/stderr 按行包装后的原始事件流，不裁剪。
- `stdout.log` / `stderr.log`：官方 CLI 原始输出，不裁剪。
- `trajectory.json`：跨 Claude Code、CodeBuddy、OpenCode 统一后的 step-by-step trajectory。
- `diff.json`：执行前后工作区变更。

短期判断标准：
- 能不能还原 agent 每一步读了什么、调用了什么工具、观察到什么结果。
- 能不能把失败样本对应到具体 action/observation。
- 能不能保留官方输出中的完整字段，即使第一版 parser 暂时不理解。
- 能不能把 `trajectory_path` 和 `raw_trace_path` 传到 manifest / evaluation，供后续 analyzer 和 Web UI 使用。

## 17. 参考资料

- Claude Code CLI Reference: [docs.claude.com/en/docs/claude-code/cli-reference](https://docs.claude.com/en/docs/claude-code/cli-reference)
- CodeBuddy CLI Headless Mode: [codebuddy.ai/docs/cli/headless](https://www.codebuddy.ai/docs/cli/headless)
- CodeBuddy CLI Reference: [codebuddy.ai/docs/cli/reference](https://www.codebuddy.ai/docs/cli/reference)
- OpenCode CLI: [opencode.ai/docs/cli](https://opencode.ai/docs/cli/)
- Anthropic: [Demystifying evals for AI agents](https://www.anthropic.com/engineering/demystifying-evals-for-ai-agents)
- LangChain AgentEvals: [github.com/langchain-ai/agentevals](https://github.com/langchain-ai/agentevals)
- LangSmith Docs: [docs.smith.langchain.com](https://docs.smith.langchain.com/)
- Langfuse Docs: [langfuse.com/docs](https://langfuse.com/docs)
- Arize Phoenix: [phoenix.arize.com](https://phoenix.arize.com/)
- Braintrust Docs: [braintrust.dev/docs](https://www.braintrust.dev/docs)
- OpenTelemetry GenAI Semantic Conventions: [opentelemetry.io/docs/specs/semconv/gen-ai](https://opentelemetry.io/docs/specs/semconv/gen-ai/)
- SWE-agent: [github.com/SWE-agent/SWE-agent](https://github.com/SWE-agent/SWE-agent)
- OpenHands: [github.com/All-Hands-AI/OpenHands](https://github.com/All-Hands-AI/OpenHands)
- AgentReplay: [github.com/agentreplay/agentreplay](https://github.com/agentreplay/agentreplay)
