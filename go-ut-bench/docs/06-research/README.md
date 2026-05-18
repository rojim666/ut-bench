# 研究资料

本目录是 UT-Bench 研究类文档入口，用于沉淀行业调研、方案设计、差距分析和后续演进路线。

## 使用规则

- 本目录文档用于理解背景、路线和方法论。
- 如文档描述与当前代码行为冲突，以 `go-ut-bench/` 下当前代码和主文档为准。
- 新增研究类文档优先放在本目录，并在下方索引表中登记。

## 文档入口

| 文档 | 内容定位 |
| --- | --- |
| [UTBENCH_PLATFORM_UPGRADE_ROADMAP.md](UTBENCH_PLATFORM_UPGRADE_ROADMAP.md) | UT-Bench 平台化升级总纲：评测工具、评测集、报告诊断、Trace、AI 优化闭环的整体路线。 |
| [AGENT_SELF_EVOLUTION_LOOP_DESIGN.md](AGENT_SELF_EVOLUTION_LOOP_DESIGN.md) | 基于评测结果、Trace/Trajectory、生成测试的 Agent 诊断、Skill 优化建议与自迭代闭环方案。 |
| [RESEARCH_REPORT.md](RESEARCH_REPORT.md) | Agent 评测生态调研主入口。 |
| [AGENT_BENCHMARK_SURVEY.md](AGENT_BENCHMARK_SURVEY.md) | benchmark 生态扫描，包含项目和论文清单。 |
| [UTBENCH_GAP_ANALYSIS.md](UTBENCH_GAP_ANALYSIS.md) | 当前 UT-Bench 与行业方案的差距分析和路线图。 |
| [ANTHROPIC_EVAL_OPTIMIZATION.md](ANTHROPIC_EVAL_OPTIMIZATION.md) | 基于 Anthropic agent eval 方法论的架构优化建议。 |
| [TRACE_REPORT_AI_ANALYSIS_DESIGN.md](TRACE_REPORT_AI_ANALYSIS_DESIGN.md) | 评测报告与 Agent Trace AI 分析、失败归因、Skill 优化闭环设计方案。 |
| [OFFICIAL_AGENT_TRACE_SOURCES.md](OFFICIAL_AGENT_TRACE_SOURCES.md) | Claude Code、CodeBuddy、OpenCode 官方 CLI trace 能力与 UT-Bench 适配策略。 |
| [REPO_LEVEL_EVALUATION_RESEARCH.md](REPO_LEVEL_EVALUATION_RESEARCH.md) | repo-level 评测研究。 |
| [research_agent_benchmark_architecture.md](research_agent_benchmark_architecture.md) | 英文架构模式研究。 |

## 后续建议

- 不再新增第二套研究入口，所有研究类补充文档都放在本目录。
- 调研文档用于方法论判断，不直接覆盖当前代码行为。
- 面向实现的方案文档应尽量写清楚模块边界、数据结构、命令入口、产物格式和阶段计划。
