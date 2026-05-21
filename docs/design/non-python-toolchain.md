# 非 Python Repo-Level 工具链补齐说明

## 当前状态

Go、Java、C++ 的 repo-level 任务已经在 `dataset/repo_level_tasks.json` 中登记，但默认仍为 `runnable: false`。当前阻塞点主要是仓库快照不完整，而不是整仓 runner 本身。

现有非 Python 快照多为目标源码子集，缺少项目级构建入口和测试依赖，因此不能代表真实整仓评测。

## Go

当前问题：

- 缺少 `go.mod`、`go.sum`。
- 缺少完整包结构、测试目录和生成资产。
- `go test ./...` 无法作为稳定项目级验证命令执行。

解决方案：

- 按固定 tag 重新同步完整仓库快照。
- 保留 `go.mod`、`go.sum` 和测试文件。
- 对大型仓库可使用 scoped package test command，但必须从完整仓库根目录运行。

设为 `runnable: true` 的条件：

- Docker 镜像内 `go test ./...` 或任务定义的 scoped `go test` 通过。
- `allowed_change_globs` 只允许 `**/*_test.go`。
- `forbidden_change_globs` 禁止非测试 Go 源码。

## Java

当前问题：

- 缺少 `pom.xml` 或 `build.gradle` / `settings.gradle`。
- 缺少 Maven/Gradle wrapper 或多模块构建配置。
- Spring/JUnit 这类项目需要完整模块关系和测试依赖。

解决方案：

- 按固定 release/tag 同步完整仓库快照。
- 保留 Maven/Gradle wrapper，或在 Docker 镜像中固定 Maven/Gradle 版本。
- 对大型多模块项目使用 scoped test command，例如 `./gradlew <module>:test`。

设为 `runnable: true` 的条件：

- Docker 镜像内 `./mvnw test`、`./gradlew test` 或任务定义的 scoped test command 通过。
- `allowed_change_globs` 只允许 `src/test/**/*.java` 或 `**/src/test/**/*.java`。
- `forbidden_change_globs` 禁止 `src/main/**/*.java` 或 `**/src/main/**/*.java`。

## C++

当前问题：

- 缺少 `CMakeLists.txt`、Bazel `BUILD` / `WORKSPACE` 等构建入口。
- 缺少测试目录、子模块、生成资产或第三方依赖。
- CMake/Bazel configure/build/test 无法稳定执行。

解决方案：

- 按固定 release/tag 同步完整仓库快照。
- 保留 CMake/Bazel 构建文件、测试目录和必要子模块。
- Docker 镜像补齐 `cmake`、`ninja`、C++ 编译器，必要时补齐 Bazel。

设为 `runnable: true` 的条件：

- Docker 镜像内 configure/build/test 全链路通过。
- `allowed_change_globs` 只允许测试文件，例如 `test/**/*.cc`、`**/*test*.cc`。
- `forbidden_change_globs` 禁止 include/src 下的产品代码。

## Docker 镜像要求

`utbench-agent-base:latest` 至少需要：

- opencode。
- Python 和 pytest。
- Go toolchain。
- JDK。
- Maven 或 Gradle，或支持项目 wrapper 运行。
- CMake、Ninja、C++ 编译器。
- 任务所需的系统依赖。

## 纳入正式统计的规则

非 Python 任务只有同时满足以下条件，才能把 `runnable` 改为 `true`：

- `workspace_root` 是完整仓库快照。
- `verify-only` 在 Docker 内通过。
- 变更策略只允许测试文件。
- 项目级 `ci_command` 稳定、可复现、无外部网络或凭证依赖。

## Snapshot Preparation Script

Use `scripts/prepare_repo_snapshots.py` to replace partial snapshots with complete upstream repositories.
The script is intentionally conservative: it does not replace an existing snapshot unless `--force` is
provided, and it does not change `runnable` unless `--verify --mark-runnable` both succeed.

```bash
python scripts/prepare_repo_snapshots.py --lang go --dry-run
python scripts/prepare_repo_snapshots.py --task-id go_cobra_test_gap_001 --force --verify
python scripts/prepare_repo_snapshots.py --task-id go_cobra_test_gap_001 --force --verify --mark-runnable
```

The same flow should be used for Java and C++ tasks after confirming the Docker image contains the
needed wrapper/runtime/toolchain. Large repositories may need a scoped `ci_command`, but the command
must still run from the complete repository root.
