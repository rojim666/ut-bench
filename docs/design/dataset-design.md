# 数据集设计

## 分层结构

ut-bench 使用三层数据集：

- `self_contained`：单文件、低环境依赖，用于稳定横评。
- `module_level`：模块或文件切片，用于测试模型理解局部项目上下文的能力。
- `repo_level`：完整仓库测试生成任务，用于测试 agent 面对 test gap brief 时的自主定位、测试编辑和项目级验证能力。

这三层不能混成一个分数。`repo_level` 的失败可能来自环境、依赖、CI、agent 工具调用或模型判断，需要单独分析。

## Repo-Level 定义

`repo_level` 只表示整仓测试生成任务，不再包含目标文件切片模式。真正的仓库级任务包含：

- 完整仓库快照或可恢复的完整仓库来源。
- 一个 `test_gap_brief`，说明需要补强的测试缺口。
- 模型自主定位相关源码、测试文件和 fixture 的空间。
- 允许新增或修改多个测试文件。
- 项目级 `ci_command` 作为验证标准。
- 变更策略校验，确保正式通过的 case 不修改业务源码。

核心文件：

```text
dataset/
  repos/<language>/<project-version>/
  repo_level_tasks.json
```

旧的 repo-level 目标文件切片入口已从主流程删除。

## Repo-Level Task 字段

每个整仓任务包含：

- `task_id`
- `language`
- `level`
- `repo_name`
- `repo_version`
- `workspace_root`
- `snapshot_completeness`
- `runnable`
- `task_type`
- `test_gap_brief`
- `success_criteria`
- `allowed_change_globs`
- `forbidden_change_globs`
- `setup_command`
- `test_command`
- `ci_command`

`task_type` 当前只用于测试缺口任务，值为 `test_gap`。

`runnable: false` 表示当前仓库快照还不是完整可测试项目，runner 默认不会选中它。

## 分级规则

- `small`：库/工具级项目，依赖少，CI 命令较轻。
- `medium`：框架或中等规模工程，存在多模块依赖、mock、fixture 或上下文定位需求。
- `large`：大型基础设施或企业级框架，依赖和构建复杂，适合压力与上限评估。

## 首批项目

| 语言 | small | medium | large |
|---|---|---|---|
| Python | `pallets/itsdangerous` | `pallets/click` | `psf/requests` |
| Go | `spf13/cobra` | `gin-gonic/gin` | `prometheus/prometheus` |
| Java | `spring-projects/spring-petclinic` | `junit-team/junit5` | `spring-projects/spring-framework` |
| C++ | `fmtlib/fmt` | `google/googletest` | `protocolbuffers/protobuf` |

当前整仓可运行样例优先落在 Python，小步验证 runner 契约。Go/Java/C++ 已保留任务定义，补齐完整快照和工具链后即可加入真实跑分。
