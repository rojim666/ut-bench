# Benchmark Profiles

本文档定义 UT-Bench 的固定评测规格。

从当前版本开始，`small / medium / large` 的运行时真相源是 `datasets/l1~l3` 目录组合，而不是固定 manifest 文件。`configs/dataset_small.json`、`dataset_medium.json`、`dataset_large.json` 仍可作为兼容输入和物化来源，但不再是主入口。

## 规格总览

| 规格 | 数据来源 | 语言 | 类别 | level 组合 | 每语言每场景上限 | Mutation | 主要用途 |
| --- | --- | --- | --- | --- | ---: | --- | --- |
| `small` | `datasets/l1` | python, go | self_contained | `l1` | 5 | 否 | 冒烟验证 |
| `medium` | `datasets/l1 + datasets/l2` | python, go, java, cpp | self_contained | `l1,l2` | 30 | 是 | 日常对比实验 |
| `large` | `datasets/l1 + datasets/l2 + datasets/l3` | python, go, java, cpp | self_contained | `l1,l2,l3` | 50 | 是 | 正式基准报告 |

固定覆盖场景：

- `boundary`
- `simple_function`
- `complex_dependency`
- `interface_mock`

## 样本规模

| 规格 | 总样本数 | 每语言 | 每场景 |
| --- | ---: | --- | --- |
| `small` | 40 | go=20, python=20 | 每语言每场景 5 |
| `medium` | 480 | cpp/go/java/python 各 120 | 每语言每场景 30 |
| `large` | 800 | cpp/go/java/python 各 200 | 每语言每场景 50 |

## 推荐命令

### small

```powershell
.\utbench.exe run `
  --models deepseek `
  --benchmark-profile small `
  --dataset-root ..\datasets `
  --mutation-enabled=false `
  --config .\configs\models.yaml
```

### medium

```powershell
.\utbench.exe run `
  --models deepseek,qwen `
  --benchmark-profile medium `
  --dataset-root ..\datasets `
  --mutation-enabled=true `
  --config .\configs\models.yaml
```

### large

```powershell
.\utbench.exe run `
  --models deepseek,qwen `
  --benchmark-profile large `
  --dataset-root ..\datasets `
  --mutation-enabled=true `
  --ingest `
  --db-path .\storage\utbench.db `
  --config .\configs\models.yaml
```

## 物化与校验

若需要重建 `datasets/l1~l3`：

```powershell
.\utbench.exe dataset materialize-levels `
  --dataset-root ..\datasets `
  --config-root .\configs
```

仅校验当前目录是否和定义一致：

```powershell
.\utbench.exe dataset materialize-levels `
  --dataset-root ..\datasets `
  --config-root .\configs `
  --check
```

## 兼容说明

- `--dataset-manifest` 仍然可用，且优先级高于 `--benchmark-profile` / `--level`
- `--level l1` 会直接扫描 `datasets/l1`
- `--level l1,l2` 会组合扫描 `datasets/l1` 与 `datasets/l2`
- 如果只想在固定规格上做更小规模 smoke run，可以继续额外叠加 `--max-samples 1`
