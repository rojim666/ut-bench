# dogfood — go-ut-bench 自托管数据集

## 内容

`go-ut-bench/` 是本仓库的一份精简快照，作为一个**完整的 Go 项目级样本**，
用于评测 LLM 在「无单测基础的真实项目里给指定文件生成测试文件」这一任务上的表现。

## 来源

从仓库根 `go-ut-bench/` 通过 `robocopy /E` 拷贝得到，已排除：

- `datasets/`、`artifacts/`、`storage/`（运行产物，避免套娃）
- `.git/`、`.windsurf/`、`node_modules/`
- `*.exe`、`utbench`、`*.db`（二进制）

## 重建命令（Windows PowerShell）

如果需要刷新此快照（在仓库根目录下执行）：

```powershell
Remove-Item -Recurse -Force .\datasets\go\go_code_files_repo_level\dogfood\go-ut-bench -ErrorAction SilentlyContinue
robocopy . .\datasets\go\go_code_files_repo_level\dogfood\go-ut-bench `
  /E `
  /XD datasets artifacts storage .git node_modules .windsurf `
  /XF *.exe utbench *.db
```

## 后续接入

要让该项目作为评测样本被流水线发现，还需为具体的 `target_file`（如
`internal/config/loader.go`）建立 sample 子目录与 `meta.json`，schema 见
`internal/contracts/spec.go` 中的 `RepoLevelMeta`。
