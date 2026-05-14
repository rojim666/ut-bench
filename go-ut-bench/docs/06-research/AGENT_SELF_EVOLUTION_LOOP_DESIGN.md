# UTBench Agent 自进化闭环设计

## 1. 背景

周会中明确了评测报告的核心价值：评测不是只看总分排名，而是要定位 agent 或 skill 在哪些指标上表现弱，再反向指导优化。trace/trajectory 则提供了 agent 实际执行过程的证据，可以解释“为什么失败”和“为什么某个 agent 更好”。

因此 UTBench 的长期方向是形成闭环：

```text
评测结果 + trajectory + diff + 生成测试
        -> 问题诊断
        -> 优化建议
        -> skill/prompt/agent config 改进
        -> 重新评测
        -> 对比指标变化
```

## 2. 阶段划分

### v1.0：看得见

目标是让用户能看到 agent 的执行过程。

能力：

- 收集原始 trace。
- 适配统一 trajectory。
- Web 展示 step-by-step trajectory。
- 规则识别基础问题。

### v1.1：诊断清楚

当前阶段是 **Diagnose Loop**。

能力：

- 构建稳定 LLM evidence bundle。
- 规则诊断作为基线真相。
- LLM 基于 evidence 解释问题、归因和给建议。
- Web 展示规则诊断、LLM 诊断、证据引用和优化建议。
- LLM 失败时可降级，不影响规则诊断。

边界：

- 不自动修改 skill。
- 不生成 patch。
- 不自动复测。

### v1.2：建议可执行

目标是把建议进一步结构化，让人能直接决定是否改。

已落地能力：

- 将建议拆成 prompt、skill、agent_config、environment、evaluator 等目标。
- 生成 `optimization_plan.json` 和 `optimization_plan.md`。
- 每个优化项包含具体修改清单、预期改善指标、风险和回退建议。
- 每个优化项包含手工验收步骤或复测命令。
- LLM 方案生成失败时保留规则生成的基础方案。
- Web `AI 分析` tab 可单独生成和刷新优化方案。

边界：

- 不自动修改 skill。
- 不生成 patch。
- 不自动复测。

### v2.0：进入 Optimize Loop

目标是半自动或自动改进。

可能能力：

- 根据诊断结果生成 skill/prompt patch。
- 由人工审核 patch。
- 应用 patch 后自动跑小样本回归评测。
- 比较优化前后的 compile/test/coverage/mutation/trace 指标。
- 保留每次优化的证据链和评测结果。

## 3. 与 Hermes/自进化思路的关系

业界“自进化”类工作通常包含三类关键机制：

- 有可度量反馈：评测集、任务成功率、错误分类、指标趋势。
- 有可解释轨迹：模型或 agent 在任务中的中间行为、工具调用、失败点。
- 有迭代动作：根据反馈改 prompt、策略、工具使用或数据，再重新评测。

UTBench 当前要落地的是工程化版本：

- 反馈来自 UTBench 的评测指标。
- 轨迹来自 agent trajectory 和 workspace diff。
- 迭代动作先停留在“建议”，后续再进入 patch 和复测。

这比直接自动改 skill 更稳，因为单测生成 agent 的风险主要在：

- 可能为追求通过率修改被测源码。
- 可能生成只覆盖表面路径的弱测试。
- 可能依赖本地偶然环境。
- 可能在不同语言和项目级样本上行为不一致。

因此 v1.1 必须先把证据链打扎实。

## 4. 数据闭环

### 输入

- `generated_manifest.json`
- `evaluation_result.json`
- `report_summary.json`
- `trajectory.json`
- `workspace_diff.json`
- `raw_trace.jsonl`
- 生成测试文件

### 中间产物

- `llm_evidence_bundle.json`
- `llm_raw_output.txt`
- `llm_diagnosis.json`

### 输出

- `analysis_report.json`
- `analysis_report.md`
- `optimization_plan.json`
- `optimization_plan.md`
- Web AI 分析页

## 5. 诊断维度

### 结果维度

- 编译是否通过。
- 测试是否通过。
- 覆盖率是否足够。
- 变异得分是否足够。
- 是否有缺失指标。

### 行为维度

- 是否读取源码。
- 是否写测试。
- 是否运行测试。
- 是否反复无效操作。
- 是否执行不推荐环境命令。
- 是否修改被测源码。
- 是否产生运行时噪声。

### 对比维度

- 不同 agent 在相同样本上的行为差异。
- 有 skill 与无 skill 的差异。
- 不同 skill 对覆盖率和变异分的影响。
- token/耗时与结果质量的关系。

## 6. 建议目标分类

LLM 和规则建议统一归类到以下目标：

- `skill`：单测生成 skill 的规则、流程、注意事项。
- `prompt`：提示词模板和硬性要求。
- `agent_config`：agent 启动参数、工具权限、trace 配置。
- `environment`：Docker、依赖、sandbox、网络、文件系统约束。
- `evaluator`：评测器和指标采集逻辑。
- `dataset`：样本质量、样本难度、样本元信息。

本阶段重点关注：

- `skill`
- `prompt`
- `agent_config`
- `environment`

## 7. 为什么 v1.1 不直接自动改 skill

原因：

- 当前 evidence 还在补强，ClaudeCode、CodeBuddy、OpenCode 的 trace 完整度仍可能不同。
- LLM 诊断需要先验证稳定性，不能让一次错误归因直接变成代码变更。
- skill patch 涉及行为策略变化，需要人工确认是否符合项目目标。
- 自动复测需要定义轻量、中量、全量评测集，本阶段暂不处理数据集管理。

因此 v1.1 的正确交付是：

- 诊断可追溯。
- 建议可落地。
- 失败可降级。
- Web 可验收。

## 8. Web 验收流程

1. 运行一个包含 ClaudeCode、CodeBuddy、OpenCode 的三 agent 任务。
2. 打开任务详情页。
3. 进入 `AI 分析` tab。
4. 点击“生成分析”。
5. 检查：
   - LLM 状态和模型是否显示。
   - evidence 数量是否显示。
   - findings 是否能按 source/severity/category/subject 过滤。
   - recommendations 是否包含 target、expected impact、risk、evidence。
   - step-by-step trajectory 是否可展开。
6. 刷新页面，确认直接读取已保存 `analysis_report.json`。
7. 关闭 API key 或选择不可用模型，确认规则分析仍生成，LLM 状态为 degraded。

## 9. v1.2 优化方案产物

`optimization_plan.json` 是 v1.2 的核心产物，依赖已有 `analysis_report.json`，不会自动触发完整 analyze。

每个优化项包含：

- `target`：`skill`、`prompt`、`agent_config`、`environment` 或 `evaluator`。
- `priority`：`P0/P1/P2/P3`。
- `source`：`rule` 或 `llm`。
- `source_refs`：关联的 finding 或 recommendation。
- `actions`：具体修改清单。
- `expected_metrics`：预期改善的 compile/test/coverage/mutation/trace/efficiency 等指标。
- `risks`：风险、缓解和回退建议。
- `verification`：人工验收步骤或复测命令。
- `evidence`：可追溯证据。

Web 使用单独按钮生成优化方案，刷新页面后优先读取已保存的 `optimization_plan.json`，不会重复请求 LLM。

## 10. 后续 Optimize Loop 草案

未来进入 v2.0 时，可以引入以下流程：

```text
analysis_report.json
  -> 提取 P0/P1 finding
  -> 生成 skill/prompt patch 草案
  -> 人工审核
  -> 应用 patch
  -> 跑小样本评测
  -> 对比 old/new 指标
  -> 决定是否合入
```

需要额外产物：

- `optimization_plan.json`
- `skill_patch.diff`
- `before_after_report.json`
- `optimization_decision.md`

这部分不属于 v1.1。
