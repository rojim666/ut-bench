# UTBench Trace + Report AI 分析设计

## 1. 当前阶段定位

当前已进入 **AI 分析 v1.2 可执行优化方案**。v1.1 的 Diagnose Loop 负责把评测结果、trajectory、workspace diff、生成测试文件和规则诊断整合成可追溯诊断报告；v1.2 在此基础上生成可人工审核、可落地执行、可复测验收的结构化优化方案。

v1.1 只做：

- 展示 agent 的 step-by-step trajectory。
- 用规则诊断识别确定性问题。
- 用 LLM 基于压缩证据包解释问题、归因并给出优化建议。
- 保存分析产物，供 Web 和 CLI 复用。

v1.1 不做：

- 不自动修改 skill。
- 不自动生成 patch。
- 不自动重跑评测。
- 不把分析结果写入 SQLite schema。
- 不把完整 raw trace 直接塞给 LLM。

v1.2 仍不做自动修改和自动复测，只新增“应该怎么改”的结构化清单。

## 2. 输入数据

分析入口以 `run_id` 为核心，从以下路径读取数据：

- `artifacts/runs/<run_id>/generated/generated_manifest.json`
- `artifacts/runs/<run_id>/evaluation/evaluation_result.json`
- `artifacts/runs/<run_id>/report/report_summary.json`
- 每个结果关联的 `trajectory.json`
- 每个结果关联的 `workspace_diff.json`
- 每个结果关联的 `raw_trace.jsonl`
- 每个结果关联的生成测试文件

其中，`trajectory.json` 是 Web 展示和 LLM 证据的核心。`raw_trace.jsonl` 保留为调试材料，不直接进入 LLM 输入。

## 3. 输出产物

分析结果保存到：

```text
artifacts/runs/<run_id>/analysis/
```

核心文件：

- `analysis_report.json`：Web 和 CLI 读取的主报告。
- `analysis_report.md`：便于人工阅读的 Markdown 摘要。
- `llm_evidence_bundle.json`：压缩后的 LLM 证据包。
- `llm_raw_output.txt`：LLM 原始文本输出，不包含 API key。
- `llm_diagnosis.json`：解析和归一化前后的 LLM 诊断结果。
- `optimization_plan.json`：v1.2 的结构化优化方案。
- `optimization_plan.md`：便于人工审核的优化方案摘要。

## 4. 规则诊断

规则诊断是基线真相，LLM 只做解释、归因和补充建议。规则诊断覆盖：

- 编译失败。
- 测试失败或测试未执行。
- 覆盖率低。
- 变异得分缺失或偏低。
- 缺少 step-by-step trajectory。
- trajectory 中未观察到源码读取。
- trajectory 中未观察到测试执行。
- agent 修改被测源码。
- 产生运行时噪声文件。
- 执行 `pip install`、`apt-get`、外部下载等不推荐命令。
- token 或耗时异常偏高。

finding 统一字段：

- `severity`: `P0/P1/P2/P3`
- `category`: `compile/test/coverage/mutation/trace/policy/efficiency/skill/environment/evaluator/dataset`
- `source`: `rule` 或 `llm`
- `subject_id`
- `sample_id`
- `title`
- `detail`
- `recommendation`
- `evidence`

## 5. LLM Evidence Bundle

LLM 不直接接收完整 raw trace，而是接收压缩后的 `LLMEvidenceBundle`。

证据包包含：

- run 级 summary。
- trace 覆盖质量。
- 被选中的 subject/sample。
- 与 subject 相关的规则 findings。
- 关键 trajectory steps。
- workspace diff 摘要。
- 生成测试片段。
- 指标摘要。

每条 evidence 都有稳定 `evidence_id`，例如：

```json
{
  "evidence_id": "ev-001",
  "kind": "trajectory_step",
  "subject_id": "opencode__mimo-v2.5__no_skill",
  "sample_id": "boundary_000",
  "path": "artifacts/runs/.../boundary_000.trajectory.json",
  "step_index": 3,
  "title": "read",
  "excerpt": "..."
}
```

LLM finding 和 recommendation 必须引用有效 `evidence_id`。如果没有引用有效 evidence，则降级为低置信度建议，不作为关键结论展示。

## 6. 证据选择策略

默认只选择有分析价值的样本，避免 token 失控：

- 失败样本。
- 低覆盖率样本。
- 低变异得分或缺失变异得分样本。
- 有 policy 行为的样本。
- 缺少 trajectory 的样本。
- token 消耗异常的样本。
- 每个 agent 的代表样本。

trajectory step 只选关键步骤：

- 读取源码。
- 写入或编辑测试。
- 执行测试命令。
- 失败命令。
- policy violation。
- 错误输出。
- 安装依赖或外部下载命令。

## 7. LLM 调用与降级

模型选择规则：

- Web/CLI 传入 `llm_model` 时使用指定模型。
- 未指定时，从 `configs/models.yaml` 中选择第一个 enabled 且 API key 环境变量存在的模型。
- 找不到可用模型、缺 API key、HTTP 失败、超时、JSON 解析失败，都不会影响规则诊断。

降级策略：

- `llm_status.status=degraded`
- `llm_status.message` 保存原因。
- 仍写出 `analysis_report.json`。
- 如果已生成 evidence bundle，则仍保存 `llm_evidence_bundle.json`。

LLM 输出必须是 JSON：

```json
{
  "summary": "...",
  "findings": [],
  "recommendations": []
}
```

## 8. Web 展示

任务详情页新增 `AI 分析` tab。

页面展示：

- LLM 状态、模型、降级原因。
- evidence bundle 规模。
- 总体结论。
- 规则诊断和 LLM 诊断。
- finding 过滤：source、severity、category、subject。
- Agent 对比表。
- step-by-step trajectory。
- 优化建议：target、expected impact、risk、evidence。
- 可执行优化方案：按 skill、prompt、agent_config、environment、evaluator 分类展示具体修改清单、验收方式、风险和 evidence。

页面刷新时优先读取已保存的 `analysis_report.json`，不会重复请求 LLM。只有点击“重新分析”才会重新生成。
优化方案使用单独按钮生成，刷新页面时优先读取已保存的 `optimization_plan.json`。

## 9. CLI 入口

```bash
./utbench analyze --run-id <run_id> --llm
./utbench analyze --run-id <run_id> --no-llm
./utbench analyze --run-id <run_id> --llm --llm-model deepseek
./utbench optimize-plan --run-id <run_id> --llm
./utbench optimize-plan --run-id <run_id> --no-llm
```

CLI 主要用于调试和自动化；Web 是主要使用入口。

## 10. 优化方案数据结构

`OptimizationPlan` 依赖已有 `analysis_report.json`。如果分析报告不存在，接口和 CLI 会返回明确错误，不会隐式跑完整 analyze。

每个 `OptimizationItem` 必须包含：

- 优化目标：`skill/prompt/agent_config/environment/evaluator`。
- 关联 agent、skill、sample。
- 来源：rule finding、LLM finding 或 recommendation。
- 具体修改清单。
- 预期改善指标。
- 风险和回退建议。
- 手工验收步骤或复测命令。
- evidence 引用。

LLM 只接收压缩输入：analysis summary、top findings、recommendations 和 evidence 摘要，不接收完整 trajectory/raw trace。LLM 失败时标记 degraded，但规则生成的基础方案仍写出。

## 11. 验收标准

使用最新三 agent run 验收：

- 能看到 ClaudeCode、CodeBuddy、OpenCode 的规则诊断。
- 能看到可用 agent 的 step-by-step trajectory。
- OpenCode 的 policy/environment 问题能被解释为 agent 行为与环境契约问题，而不是单纯编译失败。
- 能生成 `optimization_plan.json/md`。
- OpenCode policy/environment 问题能生成对应修改清单。
- CodeBuddy token 偏高能生成 agent_config/prompt 优化项。
- ClaudeCode 运行时噪声能生成 environment/diff 过滤建议。
- 关闭 API key 后仍能生成规则分析。
- 配置 API key 后能生成 LLM 总结和优化建议。
- 刷新 Web 页面后直接读取已保存结果。

## 12. 后续方向

v1.2 仍停留在可执行建议阶段。下一阶段 Optimize Loop 才考虑：

- 把诊断结果转成 skill/prompt patch 草案。
- 人工审核 patch。
- 重新运行评测。
- 比较优化前后指标。
- 最终形成“分析 -> 建议 -> 修改 -> 复测 -> 对比”的闭环。
