# Benchmark Profiles

本文档定义 UT-Bench 固定评测规格。日常运行优先使用这里的 manifest，而不是临时依赖 `--max-samples` 抽样。

## 规格总览

| 规格 | 定位 | Manifest | 语言 | 类别 | 难度 | Mutation | 主要用途 |
| --- | --- | --- | --- | --- | --- | --- | --- |
| small | 冒烟验证 | `configs/dataset_small.json` | python, go | self_contained | l1 | 否 | 验证生成、评估、报告链路 |
| medium | 对比实验 | `configs/dataset_medium.json` | python, go, java, cpp | self_contained | l1,l2 | 是 | 比较模型、prompt、agent、skill |
| large | 正式基准 | `configs/dataset_large.json` | python, go, java, cpp | self_contained | l1,l2,l3 | 是 | 产出可复现正式报告 |

当前固定 manifest 覆盖 4 个样本场景：`boundary`、`complex_dependency`、`interface_mock`、`simple_function`。

## 样本规模

| 规格 | 总样本数 | 每语言 | 每场景 |
| --- | ---: | --- | --- |
| small | 40 | go=20, python=20 | 每语言每场景 5 |
| medium | 480 | cpp/go/java/python 各 120 | 每语言每场景 30 |
| large | 800 | cpp/go/java/python 各 200 | 每语言每场景 50 |

manifest 内的路径均相对 `datasets/`，避免绑定到某一台机器的绝对路径。

## 推荐命令

small 不启用 mutation，用来快速确认链路是否能跑通：

```powershell
.\utbench.exe run `
  --models deepseek `
  --langs python,go `
  --dataset-root .\datasets `
  --dataset-manifest .\configs\dataset_small.json `
  --mutation-enabled=false `
  --config .\configs\models.yaml
```

medium 启用 mutation 和断言密度统计，用作日常对比实验：

```powershell
.\utbench.exe run `
  --models deepseek,qwen `
  --langs python,go,java,cpp `
  --dataset-root .\datasets `
  --dataset-manifest .\configs\dataset_medium.json `
  --mutation-enabled=true `
  --config .\configs\models.yaml
```

large 启用 mutation 和入库，用于正式报告：

```powershell
.\utbench.exe run `
  --models deepseek,qwen `
  --langs python,go,java,cpp `
  --dataset-root .\datasets `
  --dataset-manifest .\configs\dataset_large.json `
  --mutation-enabled=true `
  --ingest `
  --db-path .\storage\utbench.db `
  --config .\configs\models.yaml
```

## 验证命令

改动 manifest 后，建议先验证数据集和构建：

```powershell
.\utbench.exe dataset validate --dataset-root .\datasets --langs python,go,java,cpp --class self_contained --strict
go build -o utbench.exe .\cmd\utbench\
```

如果只想做极小规模 smoke run，可以在固定 manifest 上额外加 `--max-samples 1`。注意 `--dry-run` 只跳过 API 调用，仍会进入评估和报告阶段。
