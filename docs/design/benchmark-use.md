# Benchmark 使用说明

## 基础轨道

`self_contained` 和 `module_level` 在当前轻量仓库中仍是数据定义和占位入口，真实模型 runner 尚未接入。

```bash
bash scripts/run_benchmark.sh --dataset-class self_contained --lang python --dry-run
bash scripts/run_benchmark.sh --dataset-class module_level --lang go --dry-run
```

## Repo-Level 整仓 Agent 入口

`repo_level` 只表示整仓测试生成任务。runner 会从 `dataset/repo_level_tasks.json` 选择任务，复制完整仓库快照，让模型自主定位测试文件并生成或修改测试，最后运行项目级 `ci_command`。

```bash
bash scripts/run_benchmark.sh \
  --dataset-class repo_level \
  --lang python \
  --level small \
  --model deepseek \
  --max-tasks 1
```

Windows 或没有 bash 的环境可直接运行 Python runner：

```bash
python scripts/run_repo_agent_benchmark.py --lang python --level small --model deepseek --max-tasks 1
```

执行流程：

1. 从 `dataset/repo_level_tasks.json` 选择 `runnable: true` 的整仓任务。
2. 复制完整仓库快照到 `artifacts/runs/<run-id>/<model>/<skill>/<task-id>/workspace`。
3. 写入 `utbench_repo_task.md`，包含 test gap brief、验收标准、变更策略和 CI 命令。
4. 通过 Docker 中的 opencode 调用模型。
5. 运行任务定义的 `ci_command` 做项目级验证。
6. 对 workspace diff 做 policy 校验，确保通过样本没有修改业务源码。
7. 输出 `summary.json`、agent 日志、verify 日志、每个 case 的 `result.json` 和原完整 HTML 报告。

原报告链路仍由 `scripts/gen_repo_agent_report.py` 生成：

```text
artifacts/runs/<run-id>/report/report.html
artifacts/runs/<run-id>/report/report.json
artifacts/runs/<run-id>/report/report_summary.json
artifacts/runs/<run-id>/generated/generated_manifest.json
artifacts/runs/<run-id>/evaluation/evaluation_result.json
```

`report.html` 保持原布局；policy 结果写入 JSON 产物。

## 常用命令

查看可运行整仓任务：

```bash
bash scripts/run_benchmark.sh --dataset-class repo_level --lang python --dry-run
python scripts/run_repo_agent_benchmark.py --lang python --dry-run
```

只验证项目级测试命令：

```bash
python scripts/run_repo_agent_benchmark.py --lang python --level small --verify-only --max-tasks 1
```

运行仓库级 smoke 验收：

```bash
python scripts/run_repo_agent_smoke.py --mode verify-only
```

同时跑 deepseek 和 minimax：

```bash
python scripts/run_repo_agent_benchmark.py --lang python --level small --model all --max-tasks 1
```

生成或重新生成原完整报告：

```bash
bash scripts/gen_report.sh artifacts/runs/<run-id>
```

## 环境要求

- Docker 可用。
- Docker 镜像 `utbench-agent-base:latest` 存在，且包含 opencode、Python/Go/Java/C++ 基础工具链。
- deepseek 需要 `DEEPSEEK_API_KEY`。
- minimax 需要 `MINIMAX_API_KEY`。

未设置 API key 时，runner 会把 agent 阶段标记为 skipped，并返回非零退出码，避免产生误导性通过结果。

## Repo-Level Skills and Metrics

Repo-level runs support an explicit skill dimension:

```bash
python scripts/run_repo_agent_benchmark.py --lang python --level small --model all --skill all --max-tasks 1
```

Available skills:

- `test_gap_baseline`
- `coverage_focused`
- `mutation_focused`
- `all`

Case artifacts are written under:

```text
artifacts/runs/<run-id>/<model>/<skill>/<task-id>/
```

The original HTML report layout is unchanged. Machine-readable outputs include `skill_name`,
`line_coverage`, `branch_coverage`, `mutation_score`, `mutation_status`, and `case_result`.

Metrics can be disabled for fast smoke checks:

```bash
python scripts/run_repo_agent_smoke.py --mode verify-only --skip-mutation
python scripts/run_repo_agent_benchmark.py --lang python --verify-only --skip-metrics
```

Python coverage is collected with `coverage run --branch -m pytest ... && coverage json`.
Python mutation uses scoped `mutmut` when a target can be inferred. Go coverage is collected
with `go test ./... -coverprofile` after full Go snapshots are present. Java and C++ coverage
are currently reported as `unsupported` until task-specific JaCoCo / gcov / llvm-cov wiring is
available.

## Non-Python Snapshot Readiness

Go, Java, and C++ tasks remain `runnable: false` until complete repository snapshots and Docker
toolchains pass project-level verification. Use:

```bash
python scripts/prepare_repo_snapshots.py --lang go --dry-run
python scripts/prepare_repo_snapshots.py --task-id go_cobra_test_gap_001 --force --verify
python scripts/prepare_repo_snapshots.py --task-id go_cobra_test_gap_001 --force --verify --mark-runnable
```

`--mark-runnable` only updates `dataset/repo_level_tasks.json` when `--verify` passes.
